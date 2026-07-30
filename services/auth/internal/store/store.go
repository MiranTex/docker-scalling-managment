// Package store é a única parte do serviço que fala SQL — persiste
// utilizadores, credenciais de senha e refresh tokens em Postgres (schema
// "auth", isolado de qualquer outro schema que o mesmo servidor Postgres
// venha a hospedar para outros serviços).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" pro database/sql

	"auth/internal/idgen"
	"auth/internal/refresh"
)

// DB encapsula a conexão com o Postgres. Satisfaz refresh.Store (ver
// métodos Create/FindByHash/Revoke/RevokeFamily abaixo) — o pacote refresh
// não sabe nem precisa saber que quem o implementa é Postgres.
type DB struct {
	sql *sql.DB
}

// Open conecta ao Postgres em dsn (ex: "postgres://user:pass@host:5432/db?sslmode=disable")
// e aplica as migrations pendentes antes de devolver.
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

// Close fecha a pool de conexões.
func (db *DB) Close() error {
	return db.sql.Close()
}

// Ping confirma que o Postgres está alcançável -- usado pelo endpoint
// /readyz: o processo pode estar de pé (liveness) mas incapaz de servir
// nada de útil se a base de dados caiu.
func (db *DB) Ping(ctx context.Context) error {
	return db.sql.PingContext(ctx)
}

// User é uma conta, tal como persistida.
type User struct {
	ID        string
	Email     string
	CreatedAt time.Time
}

// ErrEmailTaken é devolvido por CreateUserWithPassword quando o e-mail já
// está registado.
var ErrEmailTaken = errors.New("store: e-mail já registado")

// ErrUserNotFound é devolvido quando nenhum utilizador bate com o critério
// de busca.
var ErrUserNotFound = errors.New("store: utilizador não encontrado")

// CreateUserWithPassword cria um utilizador novo já com uma credencial de
// senha, atomicamente — nunca fica um user sem credencial nenhuma nem uma
// credencial órfã se algo falhar a meio.
func (db *DB) CreateUserWithPassword(ctx context.Context, email, passwordHash string) (User, error) {
	id, err := idgen.New()
	if err != nil {
		return User{}, fmt.Errorf("store: gerando id de utilizador: %w", err)
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("store: iniciando transação: %w", err)
	}
	defer tx.Rollback()

	var createdAt time.Time
	err = tx.QueryRowContext(ctx,
		`INSERT INTO auth.users (id, email) VALUES ($1, $2) RETURNING created_at`,
		id, email,
	).Scan(&createdAt)
	if isUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("store: criando utilizador: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO auth.password_credentials (user_id, password_hash) VALUES ($1, $2)`,
		id, passwordHash,
	); err != nil {
		return User{}, fmt.Errorf("store: criando credencial de senha: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("store: confirmando criação de utilizador: %w", err)
	}

	return User{ID: id, Email: email, CreatedAt: createdAt}, nil
}

// FindUserByEmailWithPassword devolve o utilizador e o hash da senha
// guardada, usado no login. ErrUserNotFound se não existir conta com esse
// e-mail, ou se existir mas não tiver credencial de senha (ex: só OAuth).
func (db *DB) FindUserByEmailWithPassword(ctx context.Context, email string) (User, string, error) {
	var u User
	var passwordHash string
	err := db.sql.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.created_at, p.password_hash
		FROM auth.users u
		JOIN auth.password_credentials p ON p.user_id = u.id
		WHERE u.email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.CreatedAt, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrUserNotFound
	}
	if err != nil {
		return User{}, "", fmt.Errorf("store: buscando utilizador por e-mail: %w", err)
	}
	return u, passwordHash, nil
}

// Create implementa refresh.Store.
func (db *DB) Create(ctx context.Context, t refresh.Token) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO auth.refresh_tokens (id, user_id, family_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		t.ID, t.UserID, t.FamilyID, t.TokenHash, t.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: criando refresh token: %w", err)
	}
	return nil
}

// FindByHash implementa refresh.Store.
func (db *DB) FindByHash(ctx context.Context, tokenHash string) (refresh.Token, bool, error) {
	var t refresh.Token
	var revokedAt sql.NullTime
	err := db.sql.QueryRowContext(ctx, `
		SELECT id, user_id, family_id, token_hash, expires_at, revoked_at
		FROM auth.refresh_tokens
		WHERE token_hash = $1`,
		tokenHash,
	).Scan(&t.ID, &t.UserID, &t.FamilyID, &t.TokenHash, &t.ExpiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return refresh.Token{}, false, nil
	}
	if err != nil {
		return refresh.Token{}, false, fmt.Errorf("store: buscando refresh token: %w", err)
	}
	if revokedAt.Valid {
		t.RevokedAt = &revokedAt.Time
	}
	return t, true, nil
}

// Revoke implementa refresh.Store.
func (db *DB) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE auth.refresh_tokens SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`,
		id, revokedAt,
	)
	if err != nil {
		return fmt.Errorf("store: revogando refresh token: %w", err)
	}
	return nil
}

// RevokeFamily implementa refresh.Store.
func (db *DB) RevokeFamily(ctx context.Context, familyID string, revokedAt time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE auth.refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID, revokedAt,
	)
	if err != nil {
		return fmt.Errorf("store: revogando família de refresh tokens: %w", err)
	}
	return nil
}

// isUniqueViolation reconhece o erro de constraint UNIQUE do Postgres
// (SQLSTATE 23505) sem depender de importar os tipos internos do driver
// pgx — checar o texto é frágil em geral, mas o código de erro do Postgres
// em si (23505) é estável e documentado, é só o texto ao redor que muda
// entre drivers.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	type sqlStater interface{ SQLState() string }
	var pgErr sqlStater
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
