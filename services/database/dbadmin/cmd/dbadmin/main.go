// cmd/dbadmin é o ENTRYPOINT do container services/database (chamado por
// entrypoint-wrapper.sh via `exec dbadmin "$@"`, depois de gerar a
// config do pgBackRest e agendar os backups no cron) -- não um segundo
// processo em background, é o PID 1.
//
// Isto é deliberado por causa do PITR: dbadmin arranca o Postgres como
// seu processo FILHO (ver internal/pgsupervisor), em vez de dar-lhe
// exec e tornar-se ele o PID 1 (como a imagem oficial faz sozinha). Um
// restore exige parar o Postgres, reescrever o PGDATA via pgbackrest, e
// arrancá-lo de novo -- se o Postgres fosse o PID 1, pará-lo matava o
// container inteiro a meio da operação. Com o Postgres como filho, o
// dbadmin sobrevive ao ciclo parar/restaurar/arrancar inteiro e continua
// a servir a API HTTP o tempo todo (inclusive o pedido de confirmação
// do restore, feito só depois do Postgres já estar de volta em recovery
// pausado).
//
// Se o Postgres morrer sozinho (crash, não uma paragem pedida por nós),
// dbadmin sai também -- mesmo comportamento que existia quando o
// Postgres era o PID 1 (a morte dele derrubava o container inteiro);
// só queríamos deixar de acoplar isso à paragem CONTROLADA de um
// restore.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dbadmin/internal/audit"
	"dbadmin/internal/backupjob"
	"dbadmin/internal/httpapi"
	"dbadmin/internal/jwtverify"
	"dbadmin/internal/pgbackrest"
	"dbadmin/internal/pgstat"
	"dbadmin/internal/pgsupervisor"
	"dbadmin/internal/provisioning"
	"dbadmin/internal/restorejob"
	"dbadmin/internal/restorerunner"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sup := pgsupervisor.New("docker-entrypoint.sh", postgresArgs(cfg))
	if err := sup.Start(); err != nil {
		logger.Error("erro arrancando o Postgres", "err", err)
		os.Exit(1)
	}
	go sup.Monitor(ctx, func(err error) {
		logger.Error("Postgres saiu inesperadamente, encerrando o container", "err", err)
		os.Exit(1)
	})

	db := connectWithRetry(ctx, cfg.databaseURL, logger)
	defer db.Close()
	provisioner := provisioning.NewManager(db.Pool(), provisioning.ConnectionConfig{
		Host: cfg.provisionedDBHost, Port: cfg.provisionedDBPort, SSLMode: cfg.provisionedDBSSLMode,
	})
	if err := provisioner.Migrate(ctx); err != nil {
		logger.Error("erro preparando provisionamento de schemas", "err", err)
		os.Exit(1)
	}

	tokens := jwtverify.NewVerifier(cfg.authServiceURL, cfg.authIssuer, cfg.authAudience, cfg.jwksRefresh)
	backups := pgbackrest.Client{Stanza: cfg.pgbackrestStanza}
	jobs := backupjob.NewManager()
	runner := restorerunner.Runner{Supervisor: sup, Backups: backups, DB: db}
	restores := restorejob.NewManager(runner, jobs)
	auditLogger := audit.NewLogger(logger)
	handler := httpapi.NewHandler(tokens, db, backups, jobs, restores, provisioner, auditLogger)

	srv := &http.Server{
		Addr:    cfg.listenAddr,
		Handler: handler.Routes(),
	}

	go func() {
		logger.Info("dbadmin iniciado", cfg.logAttrs()...)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("erro no servidor HTTP", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("sinal de encerramento recebido, desligando")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown do servidor HTTP não terminou a tempo", "err", err)
	}
	if err := sup.Stop(shutdownCtx); err != nil {
		logger.Warn("Postgres não parou a tempo", "err", err)
	}
	logger.Info("dbadmin encerrado")
}

// postgresArgs monta os argumentos passados a docker-entrypoint.sh --
// os mesmos que antes iam diretamente no `exec docker-entrypoint.sh "$@"
// -c ...` no fim de entrypoint-wrapper.sh. os.Args[1:] é o "$@" que o
// Dockerfile/entrypoint-wrapper.sh repassam (hoje sempre ["postgres"],
// ver CMD no Dockerfile) -- lido de os.Args em vez de hardcoded para não
// duplicar essa decisão aqui.
func postgresArgs(cfg config) []string {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"postgres"}
	}
	return append(args,
		"-c", "archive_mode=on",
		"-c", fmt.Sprintf("archive_command=pgbackrest --stanza=%s archive-push %%p", cfg.pgbackrestStanza),
		"-c", "wal_level=replica",
		"-c", "max_wal_senders=3",
		"-c", "archive_timeout=60",
	)
}

// connectWithRetry espera o Postgres filho aceitar ligações -- mesmo
// padrão de services/auth/cmd/authd/main.go. A janela de corrida aqui é
// normal: pedimos para o Postgres arrancar (sup.Start) mas ele ainda
// está a inicializar (ou, na primeira vez com PGDATA vazio, a passar
// pela instância temporária do initdb) quando tentamos a primeira
// ligação.
func connectWithRetry(ctx context.Context, databaseURL string, logger *slog.Logger) *pgstat.Client {
	const maxAttempts = 30
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		db, err := pgstat.Connect(ctx, databaseURL)
		if err == nil {
			if err := db.Ping(ctx); err == nil {
				return db
			}
			db.Close()
		}
		lastErr = err
		logger.Warn("erro conectando à base de dados, tentando de novo", "attempt", attempt, "max_attempts", maxAttempts, "err", lastErr)

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
