package platform

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"launcher/internal/store"
)

// recheckDomainsEvery: um domínio por verificar volta a ser testado com
// esta frequência, para ficar ativo sozinho depois de o DNS propagar.
const recheckDomainsEvery = 2 * time.Minute

var hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// DomainView é um domínio com as instruções de DNS para o pôr ativo.
type DomainView struct {
	store.Domain
	// Expected diz para onde o DNS tem de apontar: os IPs públicos (registo
	// A/AAAA) e, em alternativa, o host por defeito do serviço (CNAME).
	ExpectedIPs   []string `json:"expectedIps"`
	ExpectedCNAME string   `json:"expectedCname,omitempty"`
}

func (s *Service) domainView(p store.Project, e store.Environment, svc store.Service, d store.Domain) DomainView {
	v := DomainView{Domain: d, ExpectedIPs: s.cfg.PublicIPs}
	if v.ExpectedIPs == nil {
		v.ExpectedIPs = []string{}
	}
	if s.cfg.PublicBaseDomain != "" {
		v.ExpectedCNAME = defaultHost(p, e, svc, s.cfg.PublicBaseDomain)
	}
	return v
}

func (s *Service) ListDomains(ctx context.Context, projectSlug, envSlug, svcSlug string) ([]DomainView, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return nil, err
	}
	domains, err := s.db.ListDomains(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	out := make([]DomainView, 0, len(domains))
	for _, d := range domains {
		out = append(out, s.domainView(p, e, svc, d))
	}
	return out, nil
}

func (s *Service) AddDomain(ctx context.Context, projectSlug, envSlug, svcSlug, hostname, by string) (DomainView, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return DomainView{}, err
	}
	if svc.Kind != store.ServiceWeb {
		return DomainView{}, invalid("só serviços web recebem domínios")
	}
	hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if len(hostname) > 253 || !hostnameRe.MatchString(hostname) {
		return DomainView{}, invalid("domínio %q inválido -- só o hostname, ex: loja.pt ou www.loja.pt", hostname)
	}
	if base := s.cfg.PublicBaseDomain; base != "" && (hostname == base || strings.HasSuffix(hostname, "."+base)) {
		return DomainView{}, invalid("%s está dentro do domínio da plataforma (%s), que já tem host próprio por serviço", hostname, base)
	}
	err = s.db.CreateDomain(ctx, store.Domain{Hostname: hostname, ServiceID: svc.ID, CreatedBy: by})
	if errors.Is(err, store.ErrConflict) {
		return DomainView{}, fmt.Errorf("%w: o domínio %s já está associado a um serviço", ErrConflict, hostname)
	}
	if err != nil {
		return DomainView{}, err
	}
	s.verifyDomain(ctx, hostname)
	return s.getDomain(ctx, p, e, svc, hostname)
}

func (s *Service) getDomain(ctx context.Context, p store.Project, e store.Environment, svc store.Service, hostname string) (DomainView, error) {
	domains, err := s.db.ListDomains(ctx, svc.ID)
	if err != nil {
		return DomainView{}, err
	}
	for _, d := range domains {
		if d.Hostname == hostname {
			return s.domainView(p, e, svc, d), nil
		}
	}
	return DomainView{}, fmt.Errorf("%w: domínio %s", ErrNotFound, hostname)
}

// VerifyDomain volta a verificar o DNS agora (botão "verificar").
func (s *Service) VerifyDomain(ctx context.Context, projectSlug, envSlug, svcSlug, hostname string) (DomainView, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return DomainView{}, err
	}
	if _, err := s.getDomain(ctx, p, e, svc, hostname); err != nil {
		return DomainView{}, err
	}
	s.verifyDomain(ctx, hostname)
	return s.getDomain(ctx, p, e, svc, hostname)
}

func (s *Service) DeleteDomain(ctx context.Context, projectSlug, envSlug, svcSlug, hostname string) error {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return err
	}
	found, err := s.db.DeleteDomain(ctx, svc.ID, hostname)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: domínio %s", ErrNotFound, hostname)
	}
	s.syncRouting(ctx)
	return nil
}

// verifyDomain resolve o hostname e compara com os IPs públicos deste
// servidor. Resolver o A/AAAA já segue CNAMEs, por isso um CNAME para o
// host por defeito do serviço também passa. Sem LAUNCHER_PUBLIC_IPS não há
// com que comparar: o domínio fica ativo e o aviso fica no erro.
func (s *Service) verifyDomain(ctx context.Context, hostname string) {
	status, msg := store.DomainActive, ""
	if len(s.cfg.PublicIPs) == 0 {
		msg = "sem verificação de DNS: LAUNCHER_PUBLIC_IPS não está configurado"
	} else {
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err := s.lookupHost(lookupCtx, hostname)
		cancel()
		switch {
		case err != nil:
			status, msg = store.DomainFailed, "o DNS ainda não resolve este domínio: "+err.Error()
		case !slices.ContainsFunc(ips, func(ip string) bool { return slices.Contains(s.cfg.PublicIPs, ip) }):
			status, msg = store.DomainFailed, fmt.Sprintf("aponta para %s, e não para este servidor (%s)", strings.Join(ips, ", "), strings.Join(s.cfg.PublicIPs, ", "))
		}
	}
	if err := s.db.SetDomainStatus(ctx, hostname, status, msg); err != nil {
		s.logger.Error("domínios: gravando verificação", "domain", hostname, "err", err)
		return
	}
	if status == store.DomainActive {
		s.syncRouting(ctx)
	}
}

// recheckDomains é o passo do reconciliador para domínios por verificar.
func (s *Service) recheckDomains(ctx context.Context) {
	domains, err := s.db.ListUnverifiedDomains(ctx)
	if err != nil {
		s.logger.Error("domínios: listando por verificar", "err", err)
		return
	}
	for _, d := range domains {
		if d.CheckedAt != nil && time.Since(*d.CheckedAt) < recheckDomainsEvery {
			continue
		}
		s.verifyDomain(ctx, d.Hostname)
	}
}
