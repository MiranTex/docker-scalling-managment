// cmd/launcher é o serviço responsável por, de facto, criar containers:
// tanto instâncias "solo" (uma app qualquer, lançada sem nenhum
// autoscaler à sua volta) quanto instâncias "group" (um autoscaler-group
// inteiro, que a partir daí passa a gerir as suas próprias réplicas).
//
// É este serviço, e só ele, que resolve ${secret:NOME} contra o
// secretsadmin -- antes disto, essa lógica vivia dentro de cada cmd/group,
// o que significava que a ÚNICA forma de uma aplicação ter acesso a
// segredos era estar sob autoscaling. Ver services/launcher/README.md (se
// existir) ou o histórico da conversa que motivou esta feature.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"launcher/internal/apikeyclient"
	"launcher/internal/authusers"
	"launcher/internal/dockerclient"
	"launcher/internal/grafanasync"
	"launcher/internal/httpapi"
	"launcher/internal/jwtverify"
	"launcher/internal/platform"
	"launcher/internal/platformapi"
	"launcher/internal/secretbox"
	"launcher/internal/secretsclient"
	"launcher/internal/store"
	"launcher/internal/templatesclient"
)

func main() {
	_ = godotenv.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := loadConfig()
	if len(os.Args) > 1 && os.Args[1] == "join-token" {
		os.Exit(joinToken(cfg))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db := connectWithRetry(ctx, cfg.databaseURL, logger)
	defer db.Close()

	tokens := jwtverify.NewVerifier(cfg.authServiceURL, cfg.authIssuer, cfg.authAudience, cfg.jwksRefresh)
	docker := dockerclient.New(cfg.dockerSocket)
	secrets := secretsclient.New(cfg.secretsAdminServiceURL, cfg.authServiceURL, cfg.secretsRefreshToken)
	templates := templatesclient.New(cfg.templatesAdminServiceURL)
	handler := httpapi.NewHandler(tokens, db, docker, secrets, templates, cfg.groupConfig(), cfg.publicBaseDomain, cfg.tlsCertResolver, cfg.capacityConfig(), cfg.specPolicy(), logger)

	var box *secretbox.Box
	if cfg.secretsKey != "" {
		var err error
		if box, err = secretbox.New(cfg.secretsKey); err != nil {
			logger.Error("configuração inválida", "err", err)
			os.Exit(1)
		}
	}
	platformSvc := platform.New(db, docker, box, cfg.platformConfig(), logger)
	go platformSvc.Run(ctx)

	mux := handler.Routes()
	api := platformapi.New(platformSvc, tokens, apikeyclient.New(cfg.authServiceURL), authusers.New(cfg.authServiceURL), logger).
		WithTerminalOrigins(cfg.terminalOrigins)
	if cfg.grafanaURL != "" {
		syncer := grafanasync.New(grafanasync.Config{
			URL: cfg.grafanaURL, AdminUser: cfg.grafanaUser, AdminPassword: cfg.grafanaPassword,
			DashboardsDir: cfg.grafanaDashboards,
			// Como o Grafana alcança cada fonte de dados (rede observability-net).
			PrometheusURL: "http://prometheus:9090",
			PromProxyURL:  "http://prom-label-proxy:8080",
			LokiURL:       "http://loki:3100",
		}, db, logger)
		go syncer.Run(ctx)
		api.WithGrafana(syncer)
	}
	if cfg.registryHost != "" {
		if err := api.WithRegistry(platformapi.RegistryConfig{
			Service: cfg.registryHost, Issuer: "base-stack", KeyFile: cfg.registryTokenKey, CertFile: cfg.registryTokenCert,
			PullPassword: cfg.registryPullPassword, URL: cfg.registryURL,
		}); err != nil {
			logger.Error("configuração inválida do registry", "err", err)
			os.Exit(1)
		}
	}
	if cfg.caCert != "" {
		if err := api.WithAgents(platformapi.AgentConfig{
			PublicURL: cfg.publicAPIURL, CACertFile: cfg.caCert, RegistryHost: cfg.registryHost, AgentImage: cfg.agentImage,
		}); err != nil {
			logger.Error("configuração inválida dos nós remotos", "err", err)
			os.Exit(1)
		}
	}
	api.Register(mux)

	srv := &http.Server{
		Addr:    cfg.listenAddr,
		Handler: mux,
	}

	go func() {
		logger.Info("launcher iniciado", cfg.logAttrs()...)
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

	// Groups e instâncias continuam a correr: o launcher é plano de
	// controlo, e a sua ausência (restart, upgrade, reboot do host) não
	// deve derrubar as aplicações. Ver "make demo-down" para limpar tudo
	// de propósito.

	logger.Info("launcher encerrado")
}

// connectWithRetry tenta ligar ao Postgres algumas vezes antes de
// desistir -- mesma razão de services/templatesadmin/cmd/templatesadmin/main.go:
// o compose sobe este serviço e o Postgres ao mesmo tempo.
// joinToken ("launcher join-token", via "basestack node token" na máquina
// da plataforma): cria um token de entrada sem passar pelo portal e
// imprime o comando para o nó novo.
func joinToken(cfg config) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if cfg.caCert == "" {
		fmt.Fprintln(os.Stderr, "os nós remotos não estão ligados nesta instalação (falta LAUNCHER_CA_CERT)")
		return 1
	}
	db, err := store.Open(ctx, cfg.databaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	t, err := platform.New(db, dockerclient.New(cfg.dockerSocket), nil, cfg.platformConfig(), slog.New(slog.NewTextHandler(io.Discard, nil))).
		CreateJoinToken(ctx, "basestack-cli")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cmd, err := platformapi.JoinCommand(platformapi.AgentConfig{PublicURL: cfg.publicAPIURL, CACertFile: cfg.caCert}, t.Token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Corre no nó novo (o token serve uma vez e expira às %s UTC):\n\n  %s\n", t.ExpiresAt.Format("15:04"), cmd)
	return 0
}

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
