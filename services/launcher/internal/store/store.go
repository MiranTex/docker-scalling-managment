// Package store é a única parte do launcher que fala SQL -- persiste,
// por instância criada, a que modelo ela corresponde e o estado
// observado, em Postgres (schema "launcher", na mesma base partilhada
// que services/database serve a outros consumidores, ver
// services/database/README.md, "schema-per-service").
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" pro database/sql
)

var ErrNotFound = errors.New("store: instância não encontrada")

const (
	KindSolo  = "solo"
	KindGroup = "group"

	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusFailed  = "failed"
)

// DB encapsula a conexão com o Postgres.
type DB struct {
	sql *sql.DB
}

// Open conecta ao Postgres em dsn e aplica as migrations pendentes antes
// de devolver.
func Open(ctx context.Context, dsn string) (*DB, error) {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: abrindo conexão: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: conectando ao Postgres: %w", err)
	}
	if err := Migrate(ctx, sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return &DB{sql: sqlDB}, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func (db *DB) Ping(ctx context.Context) error {
	return db.sql.PingContext(ctx)
}

// Instance é uma instância (solo ou group) que o launcher criou.
type Instance struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	TemplateName string     `json:"templateName"`
	ContainerID  string     `json:"containerId"`
	Status       string     `json:"status"`
	Error        string     `json:"error,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	UpdatedBy    string     `json:"updatedBy"`
	StoppedAt    *time.Time `json:"stoppedAt,omitempty"`
}

const listColumns = `id, kind, template_name, container_id, status, error, created_at, updated_at, updated_by, stopped_at`

func scanInstance(row interface{ Scan(...any) error }) (Instance, error) {
	var i Instance
	err := row.Scan(&i.ID, &i.Kind, &i.TemplateName, &i.ContainerID, &i.Status, &i.Error, &i.CreatedAt, &i.UpdatedAt, &i.UpdatedBy, &i.StoppedAt)
	if err != nil {
		return Instance{}, err
	}
	return i, nil
}

// List devolve todas as instâncias, mais recentes primeiro.
func (db *DB) List(ctx context.Context) ([]Instance, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+listColumns+` FROM launcher.instances ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: listando instâncias: %w", err)
	}
	defer rows.Close()

	out := []Instance{}
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo instância: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Get devolve uma instância pelo id.
func (db *DB) Get(ctx context.Context, id string) (Instance, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+listColumns+` FROM launcher.instances WHERE id = $1`, id)
	i, err := scanInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Instance{}, ErrNotFound
	}
	if err != nil {
		return Instance{}, fmt.Errorf("store: lendo instância %q: %w", id, err)
	}
	return i, nil
}

// Create insere uma instância nova.
func (db *DB) Create(ctx context.Context, i Instance) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.instances (id, kind, template_name, container_id, status, error, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, i.ID, i.Kind, i.TemplateName, i.ContainerID, i.Status, i.Error, i.UpdatedBy)
	if err != nil {
		return fmt.Errorf("store: gravando instância %q: %w", i.ID, err)
	}
	return nil
}

// UpdateStatus atualiza o estado observado de uma instância (ex: depois
// de um DELETE efetivamente parar o container). stoppedAt fica preenchido
// quando status = StatusStopped.
func (db *DB) UpdateStatus(ctx context.Context, id, status, errMsg, updatedBy string) error {
	res, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.instances
		SET status = $2, error = $3, updated_by = $4, updated_at = now(),
			stopped_at = CASE WHEN $2 = 'stopped' THEN now() ELSE stopped_at END
		WHERE id = $1
	`, id, status, errMsg, updatedBy)
	if err != nil {
		return fmt.Errorf("store: atualizando instância %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: confirmando atualização de %q: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
