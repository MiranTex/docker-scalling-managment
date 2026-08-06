package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// config reúne tudo que este processo precisa, vindo de variáveis de
// ambiente -- mesmo padrão de services/secretsadmin/cmd/secretsadmin/config.go.
type config struct {
	databaseURL string
	listenAddr  string

	// dockerSocket é o caminho do unix socket do Docker montado neste
	// container -- só usado para listar imagens (GET /v1/images), nunca
	// para criar/remover containers (ver internal/dockerclient).
	dockerSocket string

	authServiceURL string
	authIssuer     string
	authAudience   string
	jwksRefresh    time.Duration
}

func loadConfig() config {
	cfg := config{
		databaseURL:  os.Getenv("TEMPLATESADMIN_DATABASE_URL"),
		listenAddr:   envString("TEMPLATESADMIN_LISTEN_ADDR", ":8093"),
		dockerSocket: envString("DOCKER_SOCKET", "/var/run/docker.sock"),

		authServiceURL: envString("AUTH_SERVICE_URL", "http://localhost:8081"),
		authIssuer:     envString("AUTH_ISSUER", "auth-service"),
		authAudience:   envString("AUTH_AUDIENCE", "base-stack"),
		jwksRefresh:    envSeconds("TEMPLATESADMIN_JWKS_REFRESH_SECONDS", 300),
	}

	if cfg.databaseURL == "" {
		slog.Error("configuração inválida: TEMPLATESADMIN_DATABASE_URL é obrigatória")
		os.Exit(1)
	}

	return cfg
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
		"auth_issuer", c.authIssuer,
		"auth_audience", c.authAudience,
	}
}
