// cmd/authd é o serviço de autenticação do base-stack: regista/autentica
// utilizadores por email+senha, emite e roda tokens (access token JWT
// RS256 + refresh token opaco), e expõe a chave pública de verificação via
// JWKS -- qualquer outro serviço, em qualquer stack, valida tokens sem
// precisar de mais nada além do endpoint /.well-known/jwks.json.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
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
	"auth/internal/mfa"
	"auth/internal/oauth"
	"auth/internal/password"
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

	// "authd reset-password <email>" (via "basestack reset-password" na
	// máquina da plataforma): recuperar o acesso sem registo nem e-mail.
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		os.Exit(resetPassword(ctx, db, os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "reset-2fa" {
		os.Exit(reset2FA(ctx, db, os.Args[2:]))
	}

	promoteBootstrapSuperAdmin(ctx, db, cfg, logger)

	tokens := token.NewManager(cfg.issuer, cfg.audience, cfg.accessTokenTTL, signingKey)
	refreshTokens := refresh.NewManager(db, cfg.refreshTokenTTL)
	apiKeys := apikey.NewManager(db.APIKeyStore())
	oauthLogin := oauth.NewManager(db, oauthProviders(cfg, logger)...)
	if !cfg.openRegistration {
		oauthLogin.CloseSignup()
	}
	emailVerifier := verification.NewManager(db.EmailVerificationStore(), cfg.emailVerificationTTL)
	handler := httpapi.NewHandler(db, db, tokens, refreshTokens, apiKeys, oauthLogin, emailVerifier, db, cfg.accessTokenTTL, logger).
		WithRegistration(cfg.openRegistration, cfg.bootstrapSuperAdminEmail)
	if cfg.mfaKey != "" {
		box, err := mfa.NewBox(cfg.mfaKey)
		if err != nil {
			logger.Error("configuração inválida: AUTH_MFA_KEY", "err", err)
			os.Exit(1)
		}
		handler.WithMFA(db, box, cfg.mfaIssuer)
		if !cfg.mfaRequired {
			handler.WithMFAOptional()
			logger.Warn("AUTH_MFA_REQUIRED=false: o 2FA é opcional, também para os administradores")
		}
	} else {
		logger.Warn("AUTH_MFA_KEY não definida: 2FA desligado (e não obrigatório para os admins)")
	}

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

// resetPassword dá uma password temporária a uma conta e revoga as sessões
// dela. Quem corre isto tem acesso à máquina da plataforma -- é esse o
// controlo.
func resetPassword(ctx context.Context, db *store.DB, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "uso: authd reset-password <email>")
		return 2
	}
	user, found, err := db.FindUserByEmail(ctx, args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !found {
		fmt.Fprintf(os.Stderr, "não há conta com o e-mail %s\n", args[0])
		return 1
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	temp := base64.RawURLEncoding.EncodeToString(raw)
	hash, err := password.Hash(temp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := db.SetUserPassword(ctx, user.ID, hash); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := db.RevokeUserSessions(ctx, user.ID, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Password temporária de %s: %s\nEntra no portal com ela e troca-a (menu da conta > Alterar password).\n", user.Email, temp)
	return 0
}

// reset2FA desliga o 2FA de uma conta (perdeu o telemóvel e os códigos de
// recuperação) e termina as sessões dela. Se for obrigatório (admins), a
// pessoa volta a ativá-lo no próximo login.
func reset2FA(ctx context.Context, db *store.DB, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "uso: authd reset-2fa <email>")
		return 2
	}
	user, found, err := db.FindUserByEmail(ctx, args[0])
	if err != nil || !found {
		fmt.Fprintf(os.Stderr, "não há conta com o e-mail %s\n", args[0])
		return 1
	}
	if err := db.DisableTOTP(ctx, user.ID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = db.RevokeUserSessions(ctx, user.ID, time.Now())
	fmt.Printf("2FA de %s desligado. No próximo login volta a configurá-lo (se for administrador, é obrigatório).\n", user.Email)
	return 0
}
