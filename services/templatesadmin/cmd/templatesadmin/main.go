// cmd/templatesadmin é a API de administração de modelos de serviço:
// guarda, por nome, o launch template (imagem, cmd, env, labels, binds,
// network, extraHosts) e a config de grupo (target service, réplicas,
// portas) de um serviço autoscalado, geridos por humanos (role
// infra-admin, via portal). Não sobe nem gere nenhum container por si
// só -- ver services/templatesadmin/README.md, "Fora de escopo".
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

	"templatesadmin/internal/audit"
	"templatesadmin/internal/dockerclient"
	"templatesadmin/internal/httpapi"
	"templatesadmin/internal/jwtverify"
	"templatesadmin/internal/store"
)

func main() {
	_ = godotenv.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db := connectWithRetry(ctx, cfg.databaseURL, logger)
	defer db.Close()

	tokens := jwtverify.NewVerifier(cfg.authServiceURL, cfg.authIssuer, cfg.authAudience, cfg.jwksRefresh)
	docker := dockerclient.New(cfg.dockerSocket)
	handler := httpapi.NewHandler(tokens, db, docker, audit.NewLogger(logger))

	srv := &http.Server{
		Addr:    cfg.listenAddr,
		Handler: handler.Routes(),
	}

	go func() {
		logger.Info("templatesadmin iniciado", cfg.logAttrs()...)
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
	logger.Info("templatesadmin encerrado")
}

// connectWithRetry tenta ligar ao Postgres algumas vezes antes de
// desistir -- mesma razão de services/secretsadmin/cmd/secretsadmin/main.go:
// o compose sobe este serviço e o Postgres ao mesmo tempo.
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
