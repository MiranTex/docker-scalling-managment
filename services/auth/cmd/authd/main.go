// cmd/authd é o serviço de autenticação do base-stack: regista/autentica
// utilizadores por email+senha, emite e roda tokens (access token JWT
// RS256 + refresh token opaco), e expõe a chave pública de verificação via
// JWKS -- qualquer outro serviço, em qualquer stack, valida tokens sem
// precisar de mais nada além do endpoint /.well-known/jwks.json.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"auth/internal/apikey"
	"auth/internal/httpapi"
	"auth/internal/keystore"
	"auth/internal/oauth"
	"auth/internal/refresh"
	"auth/internal/store"
	"auth/internal/token"
	"auth/internal/verification"
)

func main() {
	_ = godotenv.Load()
	logger := setupLogger()
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	signingKey, err := keystore.LoadOrGenerate(cfg.signingKeyFile)
	if err != nil {
		logger.Error("erro carregando chave de assinatura", "err", err)
		os.Exit(1)
	}

	db := connectWithRetry(ctx, cfg.databaseURL, logger)
	defer db.Close()

	promoteBootstrapSuperAdmin(ctx, db, cfg, logger)

	tokens := token.NewManager(cfg.issuer, cfg.audience, cfg.accessTokenTTL, signingKey)
	refreshTokens := refresh.NewManager(db, cfg.refreshTokenTTL)
	apiKeys := apikey.NewManager(db.APIKeyStore())
	oauthLogin := oauth.NewManager(db, oauthProviders(cfg, logger)...)
	emailVerifier := verification.NewManager(db.EmailVerificationStore(), cfg.emailVerificationTTL)
	handler := httpapi.NewHandler(db, db, tokens, refreshTokens, apiKeys, oauthLogin, emailVerifier, db, cfg.accessTokenTTL, logger)

	srv := &http.Server{
		Addr:    cfg.listenAddr,
		Handler: handler.Routes(),
	}

	go func() {
		logger.Info("authd iniciado", cfg.logAttrs()...)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("erro no servidor HTTP", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("sinal de encerramento recebido, drenando requisições em andamento")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown do servidor HTTP não terminou a tempo", "err", err)
	}
	logger.Info("authd encerrado")
}

// promoteBootstrapSuperAdmin resolve o problema do "primeiro admin": os
// endpoints /v1/admin/* só respondem a quem já é super-admin, então sem
// nenhum a chave nunca vira. Se AUTH_BOOTSTRAP_SUPERADMIN_EMAIL estiver
// definida e ainda não existir nenhum super-admin, promove a conta com
// esse e-mail (que precisa já existir -- registada normalmente via
// POST /v1/register). Se a conta ainda não existir, não faz nada e
// tenta de novo no próximo arranque -- não cria contas "fantasma" a
// partir de uma env var.
func promoteBootstrapSuperAdmin(ctx context.Context, db *store.DB, cfg config, logger *slog.Logger) {
	if cfg.bootstrapSuperAdminEmail == "" {
		return
	}

	n, err := db.CountUsersByRole(ctx, store.RoleSuperAdmin)
	if err != nil {
		logger.Error("erro verificando super-admins existentes", "err", err)
		return
	}
	if n > 0 {
		logger.Debug("AUTH_BOOTSTRAP_SUPERADMIN_EMAIL definida mas já existe super-admin, ignorando", "existing_super_admins", n)
		return
	}

	found, err := db.PromoteUserByEmail(ctx, cfg.bootstrapSuperAdminEmail, store.RoleSuperAdmin)
	if err != nil {
		logger.Error("erro promovendo super-admin de bootstrap", "email", cfg.bootstrapSuperAdminEmail, "err", err)
		return
	}
	if !found {
		logger.Warn("AUTH_BOOTSTRAP_SUPERADMIN_EMAIL definida mas conta ainda não existe -- registe-a primeiro (POST /v1/register) e reinicie",
			"email", cfg.bootstrapSuperAdminEmail)
		return
	}
	logger.Info("conta promovida a super-admin (bootstrap)", "email", cfg.bootstrapSuperAdminEmail)
}

// oauthProviders monta os providers de login social a partir da config --
// só entra um provider se as suas três variáveis (client ID, secret,
// redirect URL) estiverem todas definidas. Sem nenhuma, o serviço sobe
// normalmente, só sem login social disponível.
func oauthProviders(cfg config, logger *slog.Logger) []oauth.Provider {
	var providers []oauth.Provider

	if cfg.googleClientID != "" && cfg.googleClientSecret != "" && cfg.googleRedirectURL != "" {
		providers = append(providers, oauth.NewGoogleProvider(cfg.googleClientID, cfg.googleClientSecret, cfg.googleRedirectURL))
		logger.Info("login social habilitado", "provider", "google")
	}
	if cfg.githubClientID != "" && cfg.githubClientSecret != "" && cfg.githubRedirectURL != "" {
		providers = append(providers, oauth.NewGitHubProvider(cfg.githubClientID, cfg.githubClientSecret, cfg.githubRedirectURL))
		logger.Info("login social habilitado", "provider", "github")
	}

	return providers
}

// connectWithRetry tenta ligar ao Postgres algumas vezes antes de desistir
// -- o compose sobe authd e o Postgres ao mesmo tempo, e mesmo com um
// healthcheck no Postgres, uma pequena janela de corrida entre "container
// de pé" e "aceitando conexões" é normal.
func connectWithRetry(ctx context.Context, dsn string, logger *slog.Logger) *store.DB {
	const maxAttempts = 10
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		db, err := store.Open(ctx, dsn)
		if err == nil {
			return db
		}
		lastErr = err
		logger.Warn("erro conectando à base de dados, tentando de novo", "attempt", attempt, "max_attempts", maxAttempts, "err", err)

		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			logger.Error("encerrado enquanto esperava a base de dados", "err", ctx.Err())
			os.Exit(1)
		}
	}

	logger.Error("não foi possível conectar à base de dados", "attempts", maxAttempts, "err", lastErr)
	os.Exit(1)
	return nil
}
