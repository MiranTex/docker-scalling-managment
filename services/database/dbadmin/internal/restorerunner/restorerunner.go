// Package restorerunner implementa restorejob.Runner: a orquestração
// física de um PITR (parar Postgres, pgbackrest restore, arrancar de
// novo, e mais tarde confirmar) -- separado de internal/restorejob
// (que só rastreia estado/concorrência) para aquele pacote poder ser
// testado com um Runner falso, sem precisar de um Postgres real.
package restorerunner

import (
	"context"
	"fmt"
	"time"

	"dbadmin/internal/pgbackrest"
)

// Supervisor é o subconjunto de pgsupervisor.Supervisor que este pacote
// precisa -- Stop/Start do Postgres como processo filho do dbadmin (ver
// pgsupervisor para o porquê disto ser necessário: o Postgres já não é
// o PID 1 do container, é isso que permite ao dbadmin sobreviver ao
// ciclo parar/restaurar/arrancar inteiro).
type Supervisor interface {
	Stop(ctx context.Context) error
	Start() error
}

// DB é o subconjunto de pgstat.Client necessário para confirmar um
// restore pausado.
type DB interface {
	Ping(ctx context.Context) error
	ResumeReplay(ctx context.Context) error
	IsInRecovery(ctx context.Context) (bool, error)
}

type Runner struct {
	Supervisor Supervisor
	Backups    pgbackrest.Client
	DB         DB

	// StopTimeout/ReadyTimeout controlam quanto tempo esperamos em cada
	// fase antes de desistir -- expostos como campos (não constantes)
	// para poderem ser encurtados nos testes.
	StopTimeout  time.Duration
	ReadyTimeout time.Duration
}

// Restore para o Postgres, corre "pgbackrest --delta restore" e arranca-o
// de novo (em recovery pausado, se restoreType/target vierem
// preenchidos) -- ver pgbackrest.Client.Restore para o porquê de
// --target-action=pause ser sempre explícito.
func (r Runner) Restore(ctx context.Context, restoreType, target string) error {
	stopCtx, cancel := context.WithTimeout(ctx, r.stopTimeout())
	defer cancel()
	if err := r.Supervisor.Stop(stopCtx); err != nil {
		return fmt.Errorf("parando Postgres antes do restore: %w", err)
	}

	if err := r.Backups.Restore(ctx, restoreType, target); err != nil {
		// Mesmo falhando, tentamos religar o Postgres -- deixar a BD
		// parada, sem nenhum processo, seria pior que um restore falhado
		// (nenhum outro pedido conseguiria sequer ver o erro através do
		// /v1/status, porque a query dependeria do próprio Postgres que
		// está de pé mas o pgstat.Client já teria falhado de qualquer
		// forma se estivesse parado).
		if startErr := r.Supervisor.Start(); startErr != nil {
			return fmt.Errorf("restore falhou (%v) e religar o Postgres também falhou: %w", err, startErr)
		}
		return fmt.Errorf("pgbackrest restore: %w", err)
	}

	if err := r.Supervisor.Start(); err != nil {
		return fmt.Errorf("arrancando Postgres depois do restore: %w", err)
	}

	return r.waitReady(ctx)
}

// waitReady espera o Postgres voltar a aceitar ligações -- em recovery
// pausado ele já aceita ligações de leitura (hot_standby é on por
// default), só não há forma de saber isso de fora além de tentar
// ligar.
func (r Runner) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(r.readyTimeout())
	var lastErr error
	for time.Now().Before(deadline) {
		if err := r.DB.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("Postgres não respondeu a tempo depois do restore: %w", lastErr)
}

// Confirm resolve o recovery pausado (pg_wal_replay_resume) e dispara um
// backup full imediatamente a seguir -- um PITR promovido sempre entra
// numa timeline nova (ver services/database/README.md), e um backup full
// reancora a cadeia de backups nessa timeline em vez de depender só da
// timeline anterior.
func (r Runner) Confirm(ctx context.Context) error {
	if err := r.DB.ResumeReplay(ctx); err != nil {
		return fmt.Errorf("confirmando restore (pg_wal_replay_resume): %w", err)
	}

	// pg_wal_replay_resume devolve quase de imediato, mas o checkpoint de
	// fim de recovery que torna o cluster num primário de verdade
	// acontece a seguir, em background -- visto na prática: disparar o
	// backup logo depois do resume falha com "unable to find primary
	// cluster" porque o pgBackRest ainda vê um standby.
	if err := r.waitPromoted(ctx); err != nil {
		return fmt.Errorf("à espera da promoção terminar: %w", err)
	}

	if err := pgbackrest.RunBackup(ctx, "full"); err != nil {
		return fmt.Errorf("backup de reancoragem pós-restore: %w", err)
	}
	return nil
}

func (r Runner) waitPromoted(ctx context.Context) error {
	deadline := time.Now().Add(r.readyTimeout())
	var lastErr error
	for time.Now().Before(deadline) {
		inRecovery, err := r.DB.IsInRecovery(ctx)
		if err == nil && !inRecovery {
			return nil
		}
		lastErr = err
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if lastErr != nil {
		return fmt.Errorf("ainda em recovery depois do timeout: %w", lastErr)
	}
	return fmt.Errorf("ainda em recovery depois do timeout")
}

func (r Runner) stopTimeout() time.Duration {
	if r.StopTimeout > 0 {
		return r.StopTimeout
	}
	return 30 * time.Second
}

func (r Runner) readyTimeout() time.Duration {
	if r.ReadyTimeout > 0 {
		return r.ReadyTimeout
	}
	return 60 * time.Second
}
