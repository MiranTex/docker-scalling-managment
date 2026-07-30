package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate aplica, em ordem e uma única vez cada, todos os ficheiros .sql
// embutidos em migrations/ -- idempotente, seguro de chamar em todo
// arranque do serviço. O schema "auth" e a tabela de controlo
// (schema_migrations) são criados aqui em vez de num ficheiro de migration,
// porque a primeira migration real já precisa que o schema exista.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS auth`); err != nil {
		return fmt.Errorf("store: criando schema auth: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS auth.schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("store: criando tabela de controlo de migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: lendo migrations embutidas: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names) // "0001_..." < "0002_..." só funciona ordenado como string mesmo

	for _, name := range names {
		applied, err := isApplied(ctx, db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := applyMigration(ctx, db, name); err != nil {
			return err
		}
	}
	return nil
}

func isApplied(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM auth.schema_migrations WHERE version = $1)`, version).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: verificando migration %s: %w", version, err)
	}
	return exists, nil
}

func applyMigration(ctx context.Context, db *sql.DB, version string) error {
	content, err := migrationsFS.ReadFile("migrations/" + version)
	if err != nil {
		return fmt.Errorf("store: lendo migration %s: %w", version, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: iniciando transação pra migration %s: %w", version, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("store: aplicando migration %s: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth.schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("store: registando migration %s: %w", version, err)
	}
	return tx.Commit()
}
