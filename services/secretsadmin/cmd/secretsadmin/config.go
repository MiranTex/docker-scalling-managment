package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// config reúne tudo que este processo precisa, vindo de variáveis de
// ambiente -- mesmo padrão de services/auth/cmd/authd/config.go e
// services/autoscaler/cmd/group/config.go.
type config struct {
	databaseURL string
	listenAddr  string

	// masterKeyBase64 cifra/decifra todos os valores (ver internal/crypto)
	// -- 32 bytes em base64 (AES-256). Perdê-la torna os segredos já
	// gravados irrecuperáveis; não há rotação nem KMS nesta iteração.
	masterKeyBase64 string

	authServiceURL string
	authIssuer     string
	authAudience   string
	jwksRefresh    time.Duration
}

func loadConfig() config {
	cfg := config{
		databaseURL:     os.Getenv("SECRETSADMIN_DATABASE_URL"),
		listenAddr:      envString("SECRETSADMIN_LISTEN_ADDR", ":8092"),
		masterKeyBase64: os.Getenv("SECRETS_MASTER_KEY"),

		authServiceURL: envString("AUTH_SERVICE_URL", "http://localhost:8081"),
		authIssuer:     envString("AUTH_ISSUER", "auth-service"),
		authAudience:   envString("AUTH_AUDIENCE", "base-stack"),
		jwksRefresh:    envSeconds("SECRETSADMIN_JWKS_REFRESH_SECONDS", 300),
	}

	if cfg.databaseURL == "" {
		slog.Error("configuração inválida: SECRETSADMIN_DATABASE_URL é obrigatória")
		os.Exit(1)
	}
	if cfg.masterKeyBase64 == "" {
		slog.Error("configuração inválida: SECRETS_MASTER_KEY é obrigatória (32 bytes em base64, AES-256)")
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
		"auth_service_url", c.authServiceURL,
		"auth_issuer", c.authIssuer,
		"auth_audience", c.authAudience,
	}
}
