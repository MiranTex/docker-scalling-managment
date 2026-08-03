package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// config reúne tudo que este processo precisa, vindo de variáveis de
// ambiente -- mesmo padrão do services/autoscaler/cmd/group/config.go.
type config struct {
	databaseURL string
	listenAddr  string

	// signingKeyFile é onde a chave RSA de assinatura dos JWT é
	// persistida entre restarts (ver internal/keystore). Precisa de um
	// volume que sobreviva ao container.
	signingKeyFile string

	// issuer/audience vão em todo JWT emitido (claims iss/aud) e são
	// conferidos na validação -- qualquer serviço que valide tokens
	// precisa saber estes dois valores.
	issuer   string
	audience string

	accessTokenTTL       time.Duration
	refreshTokenTTL      time.Duration
	emailVerificationTTL time.Duration

	// Credenciais de login social, por provider -- um provider só é
	// registado (ver main.go) se as três variáveis dele estiverem
	// definidas. Sem nenhuma configurada, o serviço funciona normalmente,
	// só sem login social disponível (os endpoints /v1/oauth/* devolvem
	// "provider desconhecido" pra qualquer provider pedido).
	googleClientID     string
	googleClientSecret string
	googleRedirectURL  string

	githubClientID     string
	githubClientSecret string
	githubRedirectURL  string
}

func loadConfig() config {
	cfg := config{
		databaseURL:          os.Getenv("AUTH_DATABASE_URL"),
		listenAddr:           envString("AUTH_LISTEN_ADDR", ":8080"),
		signingKeyFile:       envString("AUTH_SIGNING_KEY_FILE", "/var/lib/auth/signing-key.json"),
		issuer:               envString("AUTH_ISSUER", "auth-service"),
		audience:             envString("AUTH_AUDIENCE", "base-stack"),
		accessTokenTTL:       envSeconds("AUTH_ACCESS_TOKEN_TTL_SECONDS", 900),         // 15 min
		refreshTokenTTL:      envSeconds("AUTH_REFRESH_TOKEN_TTL_SECONDS", 1209600),    // 14 dias
		emailVerificationTTL: envSeconds("AUTH_EMAIL_VERIFICATION_TTL_SECONDS", 86400), // 24h

		googleClientID:     os.Getenv("AUTH_GOOGLE_CLIENT_ID"),
		googleClientSecret: os.Getenv("AUTH_GOOGLE_CLIENT_SECRET"),
		googleRedirectURL:  os.Getenv("AUTH_GOOGLE_REDIRECT_URL"),

		githubClientID:     os.Getenv("AUTH_GITHUB_CLIENT_ID"),
		githubClientSecret: os.Getenv("AUTH_GITHUB_CLIENT_SECRET"),
		githubRedirectURL:  os.Getenv("AUTH_GITHUB_REDIRECT_URL"),
	}

	if cfg.databaseURL == "" {
		slog.Error("configuração inválida: AUTH_DATABASE_URL é obrigatória")
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
		"issuer", c.issuer,
		"audience", c.audience,
		"access_token_ttl", c.accessTokenTTL.String(),
		"refresh_token_ttl", c.refreshTokenTTL.String(),
		"email_verification_ttl", c.emailVerificationTTL.String(),
		"signing_key_file", c.signingKeyFile,
	}
}
