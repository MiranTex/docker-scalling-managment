package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// config segue o mesmo padrão de services/auth/cmd/authd/config.go:
// tudo vem de variáveis de ambiente, com defaults sensatos para o caso
// comum (dentro do container services/database, ver entrypoint-wrapper.sh).
type config struct {
	listenAddr string

	// databaseURL liga ao Postgres LOCAL (mesmo container) como o
	// superuser da imagem oficial -- é assim que dbadmin lê pg_stat_activity
	// e pg_database_size sem precisar de credenciais próprias.
	databaseURL string

	provisionedDBHost    string
	provisionedDBPort    uint16
	provisionedDBSSLMode string

	pgbackrestStanza string

	// authServiceURL/authIssuer/authAudience têm de bater com o que o
	// services/auth usa para assinar tokens (AUTH_ISSUER/AUTH_AUDIENCE lá) --
	// senão todo token seria rejeitado por issuer/audience inesperados.
	authServiceURL string
	authIssuer     string
	authAudience   string
	jwksRefresh    time.Duration
}

func loadConfig() config {
	cfg := config{
		listenAddr:           envString("DBADMIN_LISTEN_ADDR", ":8091"),
		databaseURL:          os.Getenv("DBADMIN_DATABASE_URL"),
		provisionedDBHost:    envString("DBADMIN_PROVISIONED_DB_HOST", "localhost"),
		provisionedDBPort:    envUint16("DBADMIN_PROVISIONED_DB_PORT", 5432),
		provisionedDBSSLMode: envString("DBADMIN_PROVISIONED_DB_SSLMODE", "disable"),
		pgbackrestStanza:     envString("PGBACKREST_STANZA", "shared"),
		authServiceURL:       envString("AUTH_SERVICE_URL", "http://localhost:8081"),
		authIssuer:           envString("AUTH_ISSUER", "auth-service"),
		authAudience:         envString("AUTH_AUDIENCE", "base-stack"),
		jwksRefresh:          envSeconds("DBADMIN_JWKS_REFRESH_SECONDS", 300),
	}

	if cfg.databaseURL == "" {
		slog.Error("configuração inválida: DBADMIN_DATABASE_URL é obrigatória")
		os.Exit(1)
	}
	if cfg.provisionedDBHost == "" {
		slog.Error("configuração inválida: DBADMIN_PROVISIONED_DB_HOST é obrigatório")
		os.Exit(1)
	}
	validSSLModes := map[string]bool{"disable": true, "allow": true, "prefer": true, "require": true, "verify-ca": true, "verify-full": true}
	if !validSSLModes[cfg.provisionedDBSSLMode] {
		slog.Error("configuração inválida", "var", "DBADMIN_PROVISIONED_DB_SSLMODE", "value", cfg.provisionedDBSSLMode)
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

func envUint16(key string, def uint16) uint16 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 16)
	if err != nil || n == 0 {
		slog.Error("configuração inválida", "var", key, "value", v, "want", "porta entre 1 e 65535")
		os.Exit(1)
	}
	return uint16(n)
}

func (c config) logAttrs() []any {
	return []any{
		"listen_addr", c.listenAddr,
		"provisioned_db_host", c.provisionedDBHost,
		"provisioned_db_port", c.provisionedDBPort,
		"provisioned_db_sslmode", c.provisionedDBSSLMode,
		"pgbackrest_stanza", c.pgbackrestStanza,
		"auth_service_url", c.authServiceURL,
		"auth_issuer", c.authIssuer,
		"auth_audience", c.authAudience,
	}
}
