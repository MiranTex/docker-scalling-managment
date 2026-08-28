package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"launcher/internal/httpapi"
)

// config reúne tudo que este processo precisa, vindo de variáveis de
// ambiente -- mesmo padrão de services/templatesadmin/cmd/templatesadmin/config.go.
type config struct {
	databaseURL string
	listenAddr  string

	// dockerSocket é o caminho do unix socket do Docker montado neste
	// container -- este serviço USA-o para criar/iniciar/parar containers
	// de verdade (ao contrário do templatesadmin, que só lista imagens).
	dockerSocket string

	authServiceURL string
	authIssuer     string
	authAudience   string
	jwksRefresh    time.Duration

	templatesAdminServiceURL string

	// secretsAdminServiceURL/secretsRefreshToken configuram a resolução de
	// ${secret:NOME} contra o secretsadmin (ver internal/secretsclient) --
	// a MESMA credencial de máquina que antes vivia em cada cmd/group
	// (SECRETS_REFRESH_TOKEN), agora centralizada aqui.
	secretsAdminServiceURL string
	secretsRefreshToken    string

	// selfURL é o endereço deste launcher alcançável por um group que ele
	// próprio lançou (ver httpapi.GroupConfig.SelfURL).
	selfURL string
	// groupImage é a imagem usada para lançar uma instância "kind=group".
	groupImage string

	// publicBaseDomain é o domínio base para expor uma instância/group
	// publicamente através do Traefik (ver httpapi.resolveExposeLabels) --
	// "" (default) desliga a feature: um pedido com "exposeAs" falha com
	// erro claro em vez de não expor nada silenciosamente.
	publicBaseDomain string

	// tlsCertResolver é o certResolver Traefik (ver demo/docker-compose.yml,
	// serviço traefik, certificatesresolvers) a usar em toda instância
	// exposta a partir de agora -- "" (default) mantém toda instância só em
	// HTTP, como sempre até esta feature existir. Preencher com "le" (o
	// nome que demo/docker-compose.yml já configura) liga HTTPS/ACME para
	// instâncias novas; ver demo/README.md, "HTTPS/TLS (ACME)".
	tlsCertResolver string

	// defaultInstanceType/groupInstanceType são nomes do catálogo em
	// services/templatesadmin (service_templates.instance_types) -- ver
	// httpapi.CapacityConfig.
	defaultInstanceType string
	groupInstanceType   string
	// hostVCPU/hostMemoryMB descrevem a máquina onde este launcher cria
	// containers. Zero (default) desliga a contabilidade de capacidade e o
	// bloqueio por falta de memória -- os limites por container continuam
	// a ser aplicados na mesma.
	hostVCPU              float64
	hostMemoryMB          int64
	memoryOvercommitRatio float64
}

func loadConfig() config {
	cfg := config{
		databaseURL:  os.Getenv("LAUNCHER_DATABASE_URL"),
		listenAddr:   envString("LAUNCHER_LISTEN_ADDR", ":8094"),
		dockerSocket: envString("DOCKER_SOCKET", "/var/run/docker.sock"),

		authServiceURL: envString("AUTH_SERVICE_URL", "http://localhost:8081"),
		authIssuer:     envString("AUTH_ISSUER", "auth-service"),
		authAudience:   envString("AUTH_AUDIENCE", "base-stack"),
		jwksRefresh:    envSeconds("LAUNCHER_JWKS_REFRESH_SECONDS", 300),

		templatesAdminServiceURL: os.Getenv("TEMPLATESADMIN_SERVICE_URL"),

		secretsAdminServiceURL: os.Getenv("SECRETSADMIN_SERVICE_URL"),
		secretsRefreshToken:    os.Getenv("LAUNCHER_SECRETS_REFRESH_TOKEN"),

		selfURL:    envString("LAUNCHER_SELF_URL", "http://launcher:8094"),
		groupImage: envString("AUTOSCALER_GROUP_IMAGE", "autoscaler-group:latest"),

		publicBaseDomain: os.Getenv("PUBLIC_BASE_DOMAIN"),
		tlsCertResolver:  os.Getenv("PUBLIC_TLS_CERT_RESOLVER"),

		defaultInstanceType: envString("LAUNCHER_DEFAULT_INSTANCE_TYPE", "t1.small"),
		groupInstanceType:   envString("LAUNCHER_GROUP_INSTANCE_TYPE", "t1.micro"),

		hostVCPU:              envFloat("LAUNCHER_HOST_VCPU", 0),
		hostMemoryMB:          int64(envFloat("LAUNCHER_HOST_MEMORY_MB", 0)),
		memoryOvercommitRatio: envFloat("LAUNCHER_MEMORY_OVERCOMMIT_RATIO", 1),
	}

	if cfg.databaseURL == "" {
		slog.Error("configuração inválida: LAUNCHER_DATABASE_URL é obrigatória")
		os.Exit(1)
	}
	if cfg.templatesAdminServiceURL == "" {
		slog.Error("configuração inválida: TEMPLATESADMIN_SERVICE_URL é obrigatória")
		os.Exit(1)
	}

	return cfg
}

func (c config) groupConfig() httpapi.GroupConfig {
	return httpapi.GroupConfig{
		Image:          c.groupImage,
		AuthServiceURL: c.authServiceURL,
		AuthIssuer:     c.authIssuer,
		AuthAudience:   c.authAudience,
		SelfURL:        c.selfURL,
		DockerSocket:   c.dockerSocket,
	}
}

func (c config) capacityConfig() httpapi.CapacityConfig {
	return httpapi.CapacityConfig{
		DefaultInstanceType:   c.defaultInstanceType,
		GroupInstanceType:     c.groupInstanceType,
		HostVCPU:              c.hostVCPU,
		HostMemoryMB:          c.hostMemoryMB,
		MemoryOvercommitRatio: c.memoryOvercommitRatio,
	}
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		slog.Error("configuração inválida", "var", key, "value", v, "want", "número", "err", err)
		os.Exit(1)
	}
	return n
}

func envSeconds(key string, defSeconds int) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return time.Duration(defSeconds) * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Error("configuração inválida", "var", key, "value", v, "want", "inteiro (segundos)", "err", err)
		os.Exit(1)
	}
	return time.Duration(n) * time.Second
}

func (c config) logAttrs() []any {
	return []any{
		"listen_addr", c.listenAddr,
		"docker_socket", c.dockerSocket,
		"auth_service_url", c.authServiceURL,
		"templatesadmin_service_url", c.templatesAdminServiceURL,
		"secretsadmin_service_url", c.secretsAdminServiceURL,
		"self_url", c.selfURL,
		"group_image", c.groupImage,
		"public_base_domain", c.publicBaseDomain,
		"tls_cert_resolver", c.tlsCertResolver,
		"default_instance_type", c.defaultInstanceType,
		"host_vcpu", c.hostVCPU,
		"host_memory_mb", c.hostMemoryMB,
	}
}
