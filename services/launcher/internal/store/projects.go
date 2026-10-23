package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// DefaultAccountID é a única conta que existe por agora (ver
// migrations/0003_projects.sql).
const DefaultAccountID = "default"

var (
	ErrProjectNotFound     = errors.New("store: projeto não encontrado")
	ErrEnvironmentNotFound = errors.New("store: ambiente não encontrado")
	ErrSecretNotFound      = errors.New("store: segredo não encontrado")
	// ErrConflict é devolvido quando um slug/nome já existe no mesmo âmbito.
	ErrConflict = errors.New("store: já existe")
)

// isUniqueViolation reconhece o erro 23505 do Postgres, para devolver
// ErrConflict em vez de um erro genérico de SQL.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type Project struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"accountId"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	QuotaVCPU     float64   `json:"quotaVcpu"`
	QuotaMemoryMB int64     `json:"quotaMemoryMb"`
	CreatedAt     time.Time `json:"createdAt"`
	CreatedBy     string    `json:"createdBy"`
}

const projectColumns = `id, account_id, slug, name, quota_vcpu::float8, quota_memory_mb, created_at, created_by`

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.AccountID, &p.Slug, &p.Name, &p.QuotaVCPU, &p.QuotaMemoryMB, &p.CreatedAt, &p.CreatedBy)
	return p, err
}

func (db *DB) ListProjects(ctx context.Context, accountID string) ([]Project, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+projectColumns+` FROM launcher.projects WHERE account_id = $1 ORDER BY slug`, accountID)
	if err != nil {
		return nil, fmt.Errorf("store: listando projetos: %w", err)
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo projeto: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) GetProjectBySlug(ctx context.Context, accountID, slug string) (Project, error) {
	p, err := scanProject(db.sql.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM launcher.projects WHERE account_id = $1 AND slug = $2`, accountID, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrProjectNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("store: lendo projeto %q: %w", slug, err)
	}
	return p, nil
}

func (db *DB) GetProject(ctx context.Context, id string) (Project, error) {
	p, err := scanProject(db.sql.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM launcher.projects WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrProjectNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("store: lendo projeto %q: %w", id, err)
	}
	return p, nil
}

func (db *DB) CreateProject(ctx context.Context, p Project) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.projects (id, account_id, slug, name, quota_vcpu, quota_memory_mb, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		p.ID, p.AccountID, p.Slug, p.Name, p.QuotaVCPU, p.QuotaMemoryMB, p.CreatedBy)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: gravando projeto %q: %w", p.Slug, err)
	}
	return nil
}

// UpdateProject grava nome e quotas (o slug é fixo: entra nos nomes de
// redes, containers e volumes).
func (db *DB) UpdateProject(ctx context.Context, p Project) error {
	res, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.projects SET name = $2, quota_vcpu = $3, quota_memory_mb = $4 WHERE id = $1`,
		p.ID, p.Name, p.QuotaVCPU, p.QuotaMemoryMB)
	if err != nil {
		return fmt.Errorf("store: atualizando projeto %q: %w", p.Slug, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func (db *DB) DeleteProject(ctx context.Context, id string) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.projects WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: apagando projeto %q: %w", id, err)
	}
	return nil
}

type Environment struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Slug      string    `json:"slug"`
	Network   string    `json:"network"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
}

const environmentColumns = `id, project_id, slug, network, created_at, created_by`

func scanEnvironment(row interface{ Scan(...any) error }) (Environment, error) {
	var e Environment
	err := row.Scan(&e.ID, &e.ProjectID, &e.Slug, &e.Network, &e.CreatedAt, &e.CreatedBy)
	return e, err
}

func (db *DB) ListEnvironments(ctx context.Context, projectID string) ([]Environment, error) {
	return db.queryEnvironments(ctx, `SELECT `+environmentColumns+` FROM launcher.environments WHERE project_id = $1 ORDER BY created_at`, projectID)
}

// ListAllEnvironments serve o reconciliador e o arranque (ligar o Traefik e
// o próprio launcher a todas as redes de ambiente).
func (db *DB) ListAllEnvironments(ctx context.Context) ([]Environment, error) {
	return db.queryEnvironments(ctx, `SELECT `+environmentColumns+` FROM launcher.environments ORDER BY created_at`)
}

func (db *DB) queryEnvironments(ctx context.Context, query string, args ...any) ([]Environment, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listando ambientes: %w", err)
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo ambiente: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (db *DB) GetEnvironmentBySlug(ctx context.Context, projectID, slug string) (Environment, error) {
	e, err := scanEnvironment(db.sql.QueryRowContext(ctx, `SELECT `+environmentColumns+` FROM launcher.environments WHERE project_id = $1 AND slug = $2`, projectID, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return Environment{}, ErrEnvironmentNotFound
	}
	if err != nil {
		return Environment{}, fmt.Errorf("store: lendo ambiente %q: %w", slug, err)
	}
	return e, nil
}

func (db *DB) GetEnvironment(ctx context.Context, id string) (Environment, error) {
	e, err := scanEnvironment(db.sql.QueryRowContext(ctx, `SELECT `+environmentColumns+` FROM launcher.environments WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Environment{}, ErrEnvironmentNotFound
	}
	if err != nil {
		return Environment{}, fmt.Errorf("store: lendo ambiente %q: %w", id, err)
	}
	return e, nil
}

func (db *DB) CreateEnvironment(ctx context.Context, e Environment) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.environments (id, project_id, slug, network, created_by)
		VALUES ($1, $2, $3, $4, $5)`, e.ID, e.ProjectID, e.Slug, e.Network, e.CreatedBy)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: gravando ambiente %q: %w", e.Slug, err)
	}
	return nil
}

func (db *DB) DeleteEnvironment(ctx context.Context, id string) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.environments WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: apagando ambiente %q: %w", id, err)
	}
	return nil
}

// SealedSecret é um segredo tal como está na base: só o cifrado. Quem o
// abre é internal/platform, com a chave que a store nunca vê.
type SealedSecret struct {
	EnvironmentID string    `json:"-"`
	Name          string    `json:"name"`
	Ciphertext    []byte    `json:"-"`
	Nonce         []byte    `json:"-"`
	ManagedBy     string    `json:"managedBy,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
	UpdatedBy     string    `json:"updatedBy"`
}

func (db *DB) ListSecrets(ctx context.Context, environmentID string) ([]SealedSecret, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT environment_id, name, ciphertext, nonce, managed_by, updated_at, updated_by
		FROM launcher.environment_secrets WHERE environment_id = $1 ORDER BY name`, environmentID)
	if err != nil {
		return nil, fmt.Errorf("store: listando segredos: %w", err)
	}
	defer rows.Close()
	out := []SealedSecret{}
	for rows.Next() {
		var s SealedSecret
		if err := rows.Scan(&s.EnvironmentID, &s.Name, &s.Ciphertext, &s.Nonce, &s.ManagedBy, &s.UpdatedAt, &s.UpdatedBy); err != nil {
			return nil, fmt.Errorf("store: lendo segredo: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) GetSecret(ctx context.Context, environmentID, name string) (SealedSecret, error) {
	var s SealedSecret
	err := db.sql.QueryRowContext(ctx, `
		SELECT environment_id, name, ciphertext, nonce, managed_by, updated_at, updated_by
		FROM launcher.environment_secrets WHERE environment_id = $1 AND name = $2`, environmentID, name).
		Scan(&s.EnvironmentID, &s.Name, &s.Ciphertext, &s.Nonce, &s.ManagedBy, &s.UpdatedAt, &s.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return SealedSecret{}, ErrSecretNotFound
	}
	if err != nil {
		return SealedSecret{}, fmt.Errorf("store: lendo segredo %q: %w", name, err)
	}
	return s, nil
}

func (db *DB) PutSecret(ctx context.Context, s SealedSecret) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.environment_secrets (environment_id, name, ciphertext, nonce, managed_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (environment_id, name) DO UPDATE
		SET ciphertext = EXCLUDED.ciphertext, nonce = EXCLUDED.nonce, managed_by = EXCLUDED.managed_by,
			updated_by = EXCLUDED.updated_by, updated_at = now()`,
		s.EnvironmentID, s.Name, s.Ciphertext, s.Nonce, s.ManagedBy, s.UpdatedBy)
	if err != nil {
		return fmt.Errorf("store: gravando segredo %q: %w", s.Name, err)
	}
	return nil
}

func (db *DB) DeleteSecret(ctx context.Context, environmentID, name string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.environment_secrets WHERE environment_id = $1 AND name = $2`, environmentID, name)
	if err != nil {
		return fmt.Errorf("store: apagando segredo %q: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSecretNotFound
	}
	return nil
}
