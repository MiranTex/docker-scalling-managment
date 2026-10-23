package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"launcher/internal/store"
)

// routingFile é o ficheiro que este launcher escreve no diretório do file
// provider do Traefik. JSON é YAML válido, e é o Traefik que o lê.
const routingFile = "base-stack-projects.yml"

type traefikConfig struct {
	HTTP traefikHTTP `json:"http"`
}

type traefikHTTP struct {
	Routers     map[string]traefikRouter     `json:"routers"`
	Services    map[string]traefikService    `json:"services"`
	Middlewares map[string]traefikMiddleware `json:"middlewares,omitempty"`
}

type traefikRouter struct {
	Rule        string      `json:"rule"`
	EntryPoints []string    `json:"entryPoints"`
	Service     string      `json:"service"`
	Middlewares []string    `json:"middlewares,omitempty"`
	TLS         *traefikTLS `json:"tls,omitempty"`
}

type traefikMiddleware struct {
	RedirectScheme *struct {
		Scheme    string `json:"scheme"`
		Permanent bool   `json:"permanent"`
	} `json:"redirectScheme,omitempty"`
}

// redirectMiddleware manda HTTP para HTTPS quando há certificados. O
// challenge HTTP-01 da Let's Encrypt não é afetado: o Traefik dá ao seu
// router de ACME prioridade sobre qualquer outro na porta 80.
const redirectMiddleware = "bs-redirect-https"

type traefikTLS struct {
	CertResolver string `json:"certResolver"`
}

type traefikService struct {
	LoadBalancer traefikLB `json:"loadBalancer"`
}

type traefikLB struct {
	Servers        []traefikServer `json:"servers"`
	PassHostHeader bool            `json:"passHostHeader"`
}

type traefikServer struct {
	URL string `json:"url"`
}

// APIURL é o endereço público da API de plataforma (para CI), servido pelo
// Traefik em api.<domínio base> -- vazio sem domínio base.
func (s *Service) APIURL() string {
	if s.cfg.PublicBaseDomain == "" {
		return ""
	}
	return s.scheme() + "://api." + s.cfg.PublicBaseDomain
}

func (s *Service) scheme() string {
	if s.cfg.TLSCertResolver != "" {
		return "https"
	}
	return "http"
}

// defaultHost é <serviço>-<ambiente>-<projeto>.<domínio base>: um só
// nível, para funcionar com um DNS wildcard *.<domínio> e com nip.io.
func defaultHost(p store.Project, e store.Environment, svc store.Service, base string) string {
	return svc.Slug + "-" + e.Slug + "-" + p.Slug + "." + base
}

// serviceHosts são os hostnames por onde um serviço web responde: o
// default (se houver domínio base) e os domínios próprios ativos.
func (s *Service) serviceHosts(ctx context.Context, p store.Project, e store.Environment, svc store.Service) []string {
	var hosts []string
	if s.cfg.PublicBaseDomain != "" {
		hosts = append(hosts, defaultHost(p, e, svc, s.cfg.PublicBaseDomain))
	}
	hosts = append(hosts, s.activeDomains(ctx, svc.ID)...)
	return hosts
}

// activeDomains são os domínios próprios com DNS verificado.
func (s *Service) activeDomains(ctx context.Context, serviceID string) []string {
	domains, err := s.db.ListActiveDomains(ctx, serviceID)
	if err != nil {
		s.logger.Error("routing: domínios do serviço", "service_id", serviceID, "err", err)
		return nil
	}
	out := make([]string, len(domains))
	for i, d := range domains {
		out[i] = d.Hostname
	}
	return out
}

// routerName é o nome do router/serviço no Traefik -- e por isso a label
// "service" das métricas dele no Prometheus. "_" separa os slugs porque
// nenhum slug o pode conter: com "-", o projeto "a-b"/ambiente "c" e o
// projeto "a"/ambiente "b-c" dariam o mesmo nome.
func routerName(p store.Project, e store.Environment, svc store.Service) string {
	return "bs_" + p.Slug + "_" + e.Slug + "_" + svc.Slug
}

func hostRule(hosts []string) string {
	parts := make([]string, len(hosts))
	for i, h := range hosts {
		parts[i] = "Host(`" + h + "`)"
	}
	return strings.Join(parts, " || ")
}

// buildRouting monta a configuração a partir do estado atual: só réplicas
// da revisão atual, prontas e fora de drenagem, de serviços web ligados.
func (s *Service) buildRouting(ctx context.Context) (traefikConfig, error) {
	cfg := traefikConfig{HTTP: traefikHTTP{Routers: map[string]traefikRouter{}, Services: map[string]traefikService{}}}
	services, err := s.db.ListAllServices(ctx)
	if err != nil {
		return cfg, err
	}
	containers, err := s.docker.ListContainers(ctx, false, map[string][]string{"label": {LabelRole + "=" + RoleApp}})
	if err != nil {
		return cfg, err
	}
	byService := map[string][]string{}
	for _, c := range containers {
		if c.Labels[LabelRevision] == "" {
			continue
		}
		key := c.Labels[LabelService] + "/" + c.Labels[LabelRevision]
		byService[key] = append(byService[key], containerName(c))
	}

	scopes := s.newScopeCache()
	for _, svc := range services {
		if svc.Kind != store.ServiceWeb || svc.DesiredState != store.DesiredRunning || svc.CurrentRevisionID == nil {
			continue
		}
		rev, err := s.db.GetRevision(ctx, *svc.CurrentRevisionID)
		if err != nil {
			continue
		}
		var servers []traefikServer
		for _, name := range byService[svc.ID+"/"+rev.ID] {
			if s.rt.isReady(name) {
				servers = append(servers, traefikServer{URL: fmt.Sprintf("http://%s:%d", name, rev.Port)})
			}
		}
		if len(servers) == 0 {
			continue
		}
		p, e, err := scopes.get(ctx, svc.EnvironmentID)
		if err != nil {
			continue
		}
		hosts := s.serviceHosts(ctx, p, e, svc)
		if len(hosts) == 0 {
			continue
		}
		sort.Slice(servers, func(i, j int) bool { return servers[i].URL < servers[j].URL })
		name := routerName(p, e, svc)
		rule := hostRule(hosts)
		cfg.HTTP.Services[name] = traefikService{LoadBalancer: traefikLB{Servers: servers, PassHostHeader: true}}
		httpRouter := traefikRouter{Rule: rule, EntryPoints: []string{"web"}, Service: name}
		if s.cfg.TLSCertResolver != "" {
			httpRouter.Middlewares = []string{redirectMiddleware}
			cfg.HTTP.Routers[name+"-tls"] = traefikRouter{Rule: rule, EntryPoints: []string{"websecure"}, Service: name, TLS: &traefikTLS{CertResolver: s.cfg.TLSCertResolver}}
		}
		cfg.HTTP.Routers[name] = httpRouter
	}
	if s.cfg.TLSCertResolver != "" && len(cfg.HTTP.Routers) > 0 {
		var mw traefikMiddleware
		mw.RedirectScheme = &struct {
			Scheme    string `json:"scheme"`
			Permanent bool   `json:"permanent"`
		}{Scheme: "https", Permanent: true}
		cfg.HTTP.Middlewares = map[string]traefikMiddleware{redirectMiddleware: mw}
	}
	return cfg, nil
}

// syncRouting reescreve o ficheiro do Traefik se o conteúdo mudou. A
// escrita é atómica (temporário + rename) para o Traefik nunca ler meio
// ficheiro; a extensão .tmp é ignorada pelo file provider.
func (s *Service) syncRouting(ctx context.Context) {
	if s.cfg.TraefikDynamicDir == "" {
		return
	}
	cfg, err := s.buildRouting(ctx)
	if err != nil {
		s.logger.Error("routing: construindo configuração", "err", err)
		return
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		s.logger.Error("routing: serializando", "err", err)
		return
	}
	path := filepath.Join(s.cfg.TraefikDynamicDir, routingFile)
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		s.logger.Error("routing: escrevendo ficheiro", "path", tmp, "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		s.logger.Error("routing: trocando ficheiro", "path", path, "err", err)
		return
	}
	s.logger.Info("routing atualizado", "routers", len(cfg.HTTP.Routers))
}
