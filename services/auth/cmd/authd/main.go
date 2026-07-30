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

	"auth/internal/httpapi"
	"auth/internal/keystore"
	"auth/internal/refresh"
	"auth/internal/store"
	"auth/internal/token"
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

	tokens := token.NewManager(cfg.issuer, cfg.audience, cfg.accessTokenTTL, signingKey)
	refreshTokens := refresh.NewManager(db, cfg.refreshTokenTTL)
	handler := httpapi.NewHandler(db, tokens, refreshTokens, db, cfg.accessTokenTTL)

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
