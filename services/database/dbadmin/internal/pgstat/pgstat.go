// Package pgstat lê estado operacional do Postgres local (versão,
// tamanho, número de ligações) via SQL -- distinto de pgbackrest, que
// fala com o pgBackRest, não com o Postgres.
package pgstat

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Status struct {
	Version           string    `json:"version"`
	DatabaseName      string    `json:"database_name"`
	DatabaseSizeBytes int64     `json:"database_size_bytes"`
	ConnectionCount   int       `json:"connection_count"`
	StartTime         time.Time `json:"start_time"`
}

type Client struct {
	pool *pgxpool.Pool
}

func Connect(ctx context.Context, databaseURL string) (*Client, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return &Client{pool: pool}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.pool.Ping(ctx)
}

func (c *Client) Close() {
	c.pool.Close()
}

func (c *Client) Pool() *pgxpool.Pool {
	return c.pool
}

// ResumeReplay confirma um PITR pausado -- ver
// services/database/README.md, "Restaurar para um instante específico".
// Nunca bloqueia por si só (não toca em nenhuma tabela), mas se houver
// uma query pendurada à espera do lock de uma transação DDL não
// confirmada no instante do restore, chamar isto é o que a destranca (ver
// o mesmo README para o porquê).
func (c *Client) ResumeReplay(ctx context.Context) error {
	_, err := c.pool.Exec(ctx, "select pg_wal_replay_resume()")
	return err
}

// IsInRecovery reporta se este Postgres ainda está em modo de recovery
// (standby ou a meio de um PITR) -- usado depois de ResumeReplay para
// esperar a promoção terminar de verdade antes de qualquer operação que
// exija um cluster primário (ex. um backup pgBackRest). pg_wal_replay_resume
// devolve quase imediatamente, mas o checkpoint de fim de recovery que
// transforma o cluster em primário acontece a seguir, em background --
// tentar um backup antes disso falha com "unable to find primary
// cluster" (visto na prática).
func (c *Client) IsInRecovery(ctx context.Context) (bool, error) {
	var inRecovery bool
	err := c.pool.QueryRow(ctx, "select pg_is_in_recovery()").Scan(&inRecovery)
	return inRecovery, err
}

// Status agrega num só round-trip lógico (várias queries pequenas, mas
// todas rápidas contra catálogos/funções do sistema) o que a UI mostra
// no topo da página -- versão, tamanho da BD atual, ligações abertas e
// há quanto tempo este Postgres está de pé.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	if err := c.pool.QueryRow(ctx, "select version()").Scan(&s.Version); err != nil {
		return Status{}, err
	}
	if err := c.pool.QueryRow(ctx, "select current_database(), pg_database_size(current_database())").
		Scan(&s.DatabaseName, &s.DatabaseSizeBytes); err != nil {
		return Status{}, err
	}
	if err := c.pool.QueryRow(ctx, "select count(*) from pg_stat_activity").Scan(&s.ConnectionCount); err != nil {
		return Status{}, err
	}
	if err := c.pool.QueryRow(ctx, "select pg_postmaster_start_time()").Scan(&s.StartTime); err != nil {
		return Status{}, err
	}
	return s, nil
}
