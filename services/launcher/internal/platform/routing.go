package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// routingFile é o ficheiro que este launcher escreve no diretório do file
// provider do Traefik. JSON é YAML válido, e é o Traefik que o lê.
const routingFile = "base-stack-projects.yml"

// traefikConfig: tudo omitempty porque o Traefik rejeita secções vazias
// ("routers cannot be a standalone element") -- sem rotas o ficheiro fica
// só "{}", que ele aceita.
type traefikConfig struct {
	HTTP *traefikHTTP        `json:"http,omitempty"`
	TLS  *traefikTLSSettings `json:"tls,omitempty"`
}

// traefikTLSSettings: certificados que o Traefik serve por SNI (ex: o do
// registry da plataforma, assinado pela CA da instalação).
type traefikTLSSettings struct {
	Certificates []TLSCertificate `json:"certificates"`
}

// TLSCertificate são caminhos vistos pelo Traefik, não pelo launcher.
type TLSCertificate struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

type traefikHTTP struct {
	Routers     map[string]traefikRouter     `json:"routers,omitempty"`
	Services    map[string]traefikService    `json:"services,omitempty"`
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
	Retry   *traefikRetry   `json:"retry,omitempty"`
	Headers *traefikHeaders `json:"headers,omitempty"`
}

// hstsMiddleware: com HTTPS, o browser passa a usar só HTTPS nos domínios
// das apps (6 meses).
const hstsMiddleware = "bs-hsts"

type traefikHeaders struct {
	STSSeconds int `json:"stsSeconds"`
}

// retryMiddleware: um pedido que não consegue ligar a uma réplica (ex: o
// nó dela morreu e ainda não saiu do routing) tenta noutra. Só volta a
// tentar quando a réplica não chegou a responder -- nunca repete um pedido
// que ela já processou.
const retryMiddleware = "bs-retry"

type traefikRetry struct {
	Attempts        int    `json:"attempts"`
	InitialInterval string `json:"initialInterval"`
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
	cfg := traefikConfig{HTTP: &traefikHTTP{Routers: map[string]traefikRouter{}, Services: map[string]traefikService{}}}
	services, err := s.db.ListAllServices(ctx)
	if err != nil {
		return cfg, err
	}
	containers, unknown, err := s.appReplicas(ctx, false)
	if err != nil {
		return cfg, err
	}
	if unknown {
		return cfg, errNodesPending
	}
	byService := map[string][]dockerclient.Container{}
	for _, c := range containers {
		if c.Labels[LabelRevision] == "" {
			continue
		}
		key := c.Labels[LabelService] + "/" + c.Labels[LabelRevision]
		byService[key] = append(byService[key], c)
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
		// As réplicas de um nó perdido só servem enquanto não houver outra
		// pronta (ex: o túnel caiu mas a máquina está viva, e as substitutas
		// ainda estão a arrancar).
		var servers, fallback []traefikServer
		for _, c := range byService[svc.ID+"/"+rev.ID] {
			if !s.rt.isReady(containerName(c)) {
				continue
			}
			// Pelo IP na rede do ambiente (ver replicaAddr).
			server := traefikServer{URL: fmt.Sprintf("http://%s:%d", replicaAddr(c), rev.Port)}
			if isLostPhantom(c) {
				fallback = append(fallback, server)
			} else {
				servers = append(servers, server)
			}
		}
		if len(servers) == 0 {
			servers = fallback
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
		httpRouter := traefikRouter{Rule: rule, EntryPoints: []string{"web"}, Service: name, Middlewares: []string{retryMiddleware}}
		if s.cfg.TLSCertResolver != "" {
			httpRouter.Middlewares = []string{redirectMiddleware}
			cfg.HTTP.Routers[name+"-tls"] = traefikRouter{Rule: rule, EntryPoints: []string{"websecure"}, Service: name, Middlewares: []string{retryMiddleware, hstsMiddleware}, TLS: &traefikTLS{CertResolver: s.cfg.TLSCertResolver}}
		}
		cfg.HTTP.Routers[name] = httpRouter
	}
	if len(cfg.HTTP.Routers) > 0 {
		cfg.HTTP.Middlewares = map[string]traefikMiddleware{retryMiddleware: {Retry: &traefikRetry{Attempts: 3, InitialInterval: "100ms"}}}
	}
	if s.cfg.TLSCertResolver != "" && len(cfg.HTTP.Routers) > 0 {
		var mw traefikMiddleware
		mw.RedirectScheme = &struct {
			Scheme    string `json:"scheme"`
			Permanent bool   `json:"permanent"`
		}{Scheme: "https", Permanent: true}
		cfg.HTTP.Middlewares[redirectMiddleware] = mw
		cfg.HTTP.Middlewares[hstsMiddleware] = traefikMiddleware{Headers: &traefikHeaders{STSSeconds: 15552000}}
	}
	if len(cfg.HTTP.Routers) == 0 {
		cfg.HTTP = nil
	}
	if len(s.cfg.TraefikCertificates) > 0 {
		cfg.TLS = &traefikTLSSettings{Certificates: s.cfg.TraefikCertificates}
	}
	return cfg, nil
}

// syncRouting reescreve o ficheiro do Traefik se o conteúdo mudou. A
// escrita é atómica (temporário + rename) para o Traefik nunca ler meio
// ficheiro; a extensão .tmp é ignorada pelo file provider.
// errNodesPending: há nós remotos de que ainda não se sabe nada.
var errNodesPending = errors.New("à espera dos nós remotos")

func (s *Service) syncRouting(ctx context.Context) {
	if s.cfg.TraefikDynamicDir == "" {
		return
	}
	cfg, err := s.buildRouting(ctx)
	if errors.Is(err, errNodesPending) {
		// Fica o routing que estava até os nós remotos voltarem.
		return
	}
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
	routers := 0
	if cfg.HTTP != nil {
		routers = len(cfg.HTTP.Routers)
	}
	s.logger.Info("routing atualizado", "routers", routers)
}
