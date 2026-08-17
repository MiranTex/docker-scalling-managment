package provisioning

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidName  = errors.New("provisioning: nome inválido")
	ErrConflict     = errors.New("provisioning: schema ou utilizador já existe")
	ErrNotFound     = errors.New("provisioning: schema não gerido")
	ErrDependencies = errors.New("provisioning: utilizador tem dependências externas")
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]{2,62}$`)

type ConnectionConfig struct {
	Host    string
	Port    uint16
	SSLMode string
}

type Schema struct {
	SchemaName string    `json:"schema_name"`
	RoleName   string    `json:"role_name"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by"`
}

type Credentials struct {
	Schema
	DatabaseURL string `json:"database_url"`
	PGEnv       string `json:"pg_env"`
}

type Manager struct {
	pool       *pgxpool.Pool
	connection ConnectionConfig
}

func NewManager(pool *pgxpool.Pool, connection ConnectionConfig) *Manager {
	return &Manager{pool: pool, connection: connection}
}

func (m *Manager) Migrate(ctx context.Context) error {
	_, err := m.pool.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS dbadmin;
		CREATE TABLE IF NOT EXISTS dbadmin.provisioned_schemas (
			schema_name TEXT PRIMARY KEY,
			role_name TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_by TEXT NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("provisioning: preparando metadata: %w", err)
	}
	return nil
}

func (m *Manager) List(ctx context.Context) ([]Schema, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT schema_name, role_name, created_at, created_by
		FROM dbadmin.provisioned_schemas
		ORDER BY schema_name`)
	if err != nil {
		return nil, fmt.Errorf("provisioning: listando schemas: %w", err)
	}
	defer rows.Close()

	schemas := make([]Schema, 0)
	for rows.Next() {
		var schema Schema
		if err := rows.Scan(&schema.SchemaName, &schema.RoleName, &schema.CreatedAt, &schema.CreatedBy); err != nil {
			return nil, fmt.Errorf("provisioning: lendo schema: %w", err)
		}
		schemas = append(schemas, schema)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("provisioning: listando schemas: %w", err)
	}
	return schemas, nil
}

func (m *Manager) Create(ctx context.Context, schemaName, roleName, actor string) (Credentials, error) {
	if err := validateNames(schemaName, roleName); err != nil {
		return Credentials{}, err
	}
	password, err := generatePassword()
	if err != nil {
		return Credentials{}, err
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return Credentials{}, fmt.Errorf("provisioning: iniciando criação: %w", err)
	}
	defer tx.Rollback(ctx)

	var occupied bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname = $1)
			OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $2)`, schemaName, roleName).Scan(&occupied)
	if err != nil {
		return Credentials{}, fmt.Errorf("provisioning: verificando nomes: %w", err)
	}
	if occupied {
		return Credentials{}, ErrConflict
	}

	roleID := pgx.Identifier{roleName}.Sanitize()
	schemaID := pgx.Identifier{schemaName}.Sanitize()
	createRoleSQL, err := formatSQL(ctx, tx, "CREATE ROLE %I WITH LOGIN PASSWORD %L", roleName, password)
	if err != nil {
		return Credentials{}, err
	}
	if _, err := tx.Exec(ctx, createRoleSQL); err != nil {
		return Credentials{}, classifyConflict(err)
	}
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schemaID+" AUTHORIZATION "+roleID); err != nil {
		return Credentials{}, classifyConflict(err)
	}
	var databaseName string
	if err := tx.QueryRow(ctx, "SELECT current_database()").Scan(&databaseName); err != nil {
		return Credentials{}, fmt.Errorf("provisioning: lendo nome da base: %w", err)
	}
	databaseID := pgx.Identifier{databaseName}.Sanitize()
	if _, err := tx.Exec(ctx, "ALTER ROLE "+roleID+" IN DATABASE "+databaseID+" SET search_path TO "+schemaID); err != nil {
		return Credentials{}, fmt.Errorf("provisioning: configurando search_path: %w", err)
	}

	var schema Schema
	err = tx.QueryRow(ctx, `
		INSERT INTO dbadmin.provisioned_schemas (schema_name, role_name, created_by)
		VALUES ($1, $2, $3)
		RETURNING schema_name, role_name, created_at, created_by`, schemaName, roleName, actor).
		Scan(&schema.SchemaName, &schema.RoleName, &schema.CreatedAt, &schema.CreatedBy)
	if err != nil {
		return Credentials{}, classifyConflict(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Credentials{}, fmt.Errorf("provisioning: confirmando criação: %w", err)
	}
	return m.credentials(schema, databaseName, password), nil
}

func (m *Manager) ResetPassword(ctx context.Context, schemaName string) (Credentials, error) {
	if err := validateName(schemaName); err != nil {
		return Credentials{}, err
	}
	password, err := generatePassword()
	if err != nil {
		return Credentials{}, err
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return Credentials{}, fmt.Errorf("provisioning: iniciando redefinição: %w", err)
	}
	defer tx.Rollback(ctx)

	schema, err := getManaged(ctx, tx, schemaName)
	if err != nil {
		return Credentials{}, err
	}
	resetPasswordSQL, err := formatSQL(ctx, tx, "ALTER ROLE %I PASSWORD %L", schema.RoleName, password)
	if err != nil {
		return Credentials{}, err
	}
	if _, err := tx.Exec(ctx, resetPasswordSQL); err != nil {
		return Credentials{}, fmt.Errorf("provisioning: redefinindo senha: %w", err)
	}
	var databaseName string
	if err := tx.QueryRow(ctx, "SELECT current_database()").Scan(&databaseName); err != nil {
		return Credentials{}, fmt.Errorf("provisioning: lendo nome da base: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Credentials{}, fmt.Errorf("provisioning: confirmando redefinição: %w", err)
	}
	return m.credentials(schema, databaseName, password), nil
}

func (m *Manager) Delete(ctx context.Context, schemaName string) error {
	if err := validateName(schemaName); err != nil {
		return err
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("provisioning: iniciando remoção: %w", err)
	}
	defer tx.Rollback(ctx)

	schema, err := getManaged(ctx, tx, schemaName)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema.SchemaName}.Sanitize()+" CASCADE"); err != nil {
		return fmt.Errorf("provisioning: removendo schema: %w", err)
	}
	if _, err := tx.Exec(ctx, "DROP ROLE "+pgx.Identifier{schema.RoleName}.Sanitize()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "2BP01" {
			return ErrDependencies
		}
		return fmt.Errorf("provisioning: removendo utilizador: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dbadmin.provisioned_schemas WHERE schema_name = $1`, schemaName); err != nil {
		return fmt.Errorf("provisioning: removendo metadata: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("provisioning: confirmando remoção: %w", err)
	}
	return nil
}

func getManaged(ctx context.Context, tx pgx.Tx, schemaName string) (Schema, error) {
	var schema Schema
	err := tx.QueryRow(ctx, `
		SELECT schema_name, role_name, created_at, created_by
		FROM dbadmin.provisioned_schemas WHERE schema_name = $1
		FOR UPDATE`, schemaName).
		Scan(&schema.SchemaName, &schema.RoleName, &schema.CreatedAt, &schema.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Schema{}, ErrNotFound
	}
	if err != nil {
		return Schema{}, fmt.Errorf("provisioning: lendo schema: %w", err)
	}
	return schema, nil
}

func formatSQL(ctx context.Context, tx pgx.Tx, format string, args ...any) (string, error) {
	query := "SELECT format($1"
	parameters := make([]any, 0, len(args)+1)
	parameters = append(parameters, format)
	for index, arg := range args {
		query += ", $" + strconv.Itoa(index+2) + "::text"
		parameters = append(parameters, arg)
	}
	query += ")"
	var statement string
	if err := tx.QueryRow(ctx, query, parameters...).Scan(&statement); err != nil {
		return "", fmt.Errorf("provisioning: preparando comando SQL: %w", err)
	}
	return statement, nil
}

func validateNames(schemaName, roleName string) error {
	if err := validateName(schemaName); err != nil {
		return err
	}
	return validateName(roleName)
}

func validateName(name string) error {
	if !validName.MatchString(name) || name == "public" || name == "dbadmin" || name == "information_schema" || strings.HasPrefix(name, "pg_") {
		return fmt.Errorf("%w: use 3 a 63 caracteres minúsculos, começando por letra e contendo apenas letras, números ou _", ErrInvalidName)
	}
	return nil
}

func generatePassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("provisioning: gerando senha: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func classifyConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "42710" || pgErr.Code == "42P06") {
		return ErrConflict
	}
	return fmt.Errorf("provisioning: criando recurso: %w", err)
}

func (m *Manager) credentials(schema Schema, databaseName, password string) Credentials {
	connectionURL := &url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(schema.RoleName, password),
		Host:   net.JoinHostPort(m.connection.Host, strconv.Itoa(int(m.connection.Port))),
		Path:   databaseName,
	}
	query := connectionURL.Query()
	query.Set("sslmode", m.connection.SSLMode)
	connectionURL.RawQuery = query.Encode()

	pgEnv := strings.Join([]string{
		"PGHOST=" + m.connection.Host,
		"PGPORT=" + strconv.Itoa(int(m.connection.Port)),
		"PGDATABASE=" + databaseName,
		"PGUSER=" + schema.RoleName,
		"PGPASSWORD=" + password,
		"PGSSLMODE=" + m.connection.SSLMode,
	}, "\n")
	return Credentials{Schema: schema, DatabaseURL: connectionURL.String(), PGEnv: pgEnv}
}
