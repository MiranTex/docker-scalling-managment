// Package store é a única parte do secretsadmin que fala SQL -- persiste
// segredos cifrados em Postgres (schema "secrets", na mesma base
// partilhada que services/database serve a outros consumidores, ver
// services/database/README.md, "schema-per-service"). Nunca guarda nem lê
// valores em claro: cifra/decifra é feita por quem chama (ver
// internal/httpapi), este pacote só move bytes.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" pro database/sql
)

var ErrNotFound = errors.New("store: segredo não encontrado")

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

// Secret é uma entrada tal como persistida -- Ciphertext/Nonce nunca saem
// deste pacote em direção a uma resposta HTTP sem passar primeiro por
// internal/crypto.Open (decifra) do lado de quem gere permissão pra isso.
type Secret struct {
	Name       string
	Ciphertext []byte
	Nonce      []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
	UpdatedBy  string
}

// SecretInfo é a forma exposta por GET /v1/secrets -- deliberadamente sem
// Ciphertext/Nonce, para que nem um bug num handler consiga vazar bytes
// cifrados por engano numa resposta que não devia tê-los.
type SecretInfo struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

// List devolve todos os segredos conhecidos, sem o valor cifrado.
func (db *DB) List(ctx context.Context) ([]SecretInfo, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT name, created_at, updated_at, updated_by FROM secrets.secrets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: listando segredos: %w", err)
	}
	defer rows.Close()

	var out []SecretInfo
	for rows.Next() {
		var s SecretInfo
		if err := rows.Scan(&s.Name, &s.CreatedAt, &s.UpdatedAt, &s.UpdatedBy); err != nil {
			return nil, fmt.Errorf("store: lendo segredo: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get devolve o valor cifrado + nonce de um segredo -- só para quem já
// tem a chave de cifra e vai decifrar antes de devolver a quem pediu.
func (db *DB) Get(ctx context.Context, name string) (Secret, error) {
	var s Secret
	s.Name = name
	err := db.sql.QueryRowContext(ctx,
		`SELECT ciphertext, nonce, created_at, updated_at, updated_by FROM secrets.secrets WHERE name = $1`, name,
	).Scan(&s.Ciphertext, &s.Nonce, &s.CreatedAt, &s.UpdatedAt, &s.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Secret{}, ErrNotFound
	}
	if err != nil {
		return Secret{}, fmt.Errorf("store: lendo segredo %q: %w", name, err)
	}
	return s, nil
}

// GetMany devolve os segredos cifrados cujo nome esteja em names, num mapa
// nome->Secret. Nomes que não existem simplesmente não aparecem no mapa
// (quem chama decide se falta ser erro).
func (db *DB) GetMany(ctx context.Context, names []string) (map[string]Secret, error) {
	if len(names) == 0 {
		return map[string]Secret{}, nil
	}

	placeholders := make([]any, len(names))
	query := `SELECT name, ciphertext, nonce, created_at, updated_at, updated_by FROM secrets.secrets WHERE name = ANY($1)`
	placeholders[0] = names
	rows, err := db.sql.QueryContext(ctx, query, names)
	if err != nil {
		return nil, fmt.Errorf("store: lendo segredos: %w", err)
	}
	defer rows.Close()

	out := make(map[string]Secret, len(names))
	for rows.Next() {
		var s Secret
		if err := rows.Scan(&s.Name, &s.Ciphertext, &s.Nonce, &s.CreatedAt, &s.UpdatedAt, &s.UpdatedBy); err != nil {
			return nil, fmt.Errorf("store: lendo segredo: %w", err)
		}
		out[s.Name] = s
	}
	return out, rows.Err()
}

// Upsert cria ou substitui o valor cifrado de name -- não distingue
// "criar" de "atualizar" (mesma operação, mesma auditoria do lado do
// chamador).
func (db *DB) Upsert(ctx context.Context, name string, ciphertext, nonce []byte, updatedBy string) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO secrets.secrets (name, ciphertext, nonce, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (name) DO UPDATE SET
			ciphertext = EXCLUDED.ciphertext,
			nonce = EXCLUDED.nonce,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
	`, name, ciphertext, nonce, updatedBy)
	if err != nil {
		return fmt.Errorf("store: gravando segredo %q: %w", name, err)
	}
	return nil
}

// Delete remove um segredo. Devolve ErrNotFound se não existia.
func (db *DB) Delete(ctx context.Context, name string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM secrets.secrets WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("store: apagando segredo %q: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: confirmando remoção de %q: %w", name, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
