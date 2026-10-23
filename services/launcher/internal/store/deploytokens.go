package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrDeployTokenNotFound = errors.New("store: token de deploy não encontrado")

type DeployToken struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"projectId"`
	EnvironmentID *string    `json:"environmentId,omitempty"`
	Name          string     `json:"name"`
	TokenHash     string     `json:"-"`
	Prefix        string     `json:"prefix"`
	CreatedAt     time.Time  `json:"createdAt"`
	CreatedBy     string     `json:"createdBy"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
}

const deployTokenColumns = `id, project_id, environment_id, name, token_hash, prefix, created_at, created_by, last_used_at, revoked_at`

func scanDeployToken(row interface{ Scan(...any) error }) (DeployToken, error) {
	var t DeployToken
	err := row.Scan(&t.ID, &t.ProjectID, &t.EnvironmentID, &t.Name, &t.TokenHash, &t.Prefix, &t.CreatedAt, &t.CreatedBy, &t.LastUsedAt, &t.RevokedAt)
	return t, err
}

func (db *DB) CreateDeployToken(ctx context.Context, t DeployToken) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.deploy_tokens (id, project_id, environment_id, name, token_hash, prefix, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, t.ID, t.ProjectID, t.EnvironmentID, t.Name, t.TokenHash, t.Prefix, t.CreatedBy)
	if err != nil {
		return fmt.Errorf("store: gravando token de deploy: %w", err)
	}
	return nil
}

func (db *DB) ListDeployTokens(ctx context.Context, projectID string) ([]DeployToken, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+deployTokenColumns+` FROM launcher.deploy_tokens WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listando tokens de deploy: %w", err)
	}
	defer rows.Close()
	out := []DeployToken{}
	for rows.Next() {
		t, err := scanDeployToken(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo token de deploy: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindActiveDeployToken procura pelo hash e marca o uso numa só ida à base.
func (db *DB) FindActiveDeployToken(ctx context.Context, hash string) (DeployToken, error) {
	t, err := scanDeployToken(db.sql.QueryRowContext(ctx, `
		UPDATE launcher.deploy_tokens SET last_used_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL
		RETURNING `+deployTokenColumns, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return DeployToken{}, ErrDeployTokenNotFound
	}
	if err != nil {
		return DeployToken{}, fmt.Errorf("store: validando token de deploy: %w", err)
	}
	return t, nil
}

func (db *DB) RevokeDeployToken(ctx context.Context, projectID, id string) error {
	res, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.deploy_tokens SET revoked_at = now() WHERE project_id = $1 AND id = $2 AND revoked_at IS NULL`, projectID, id)
	if err != nil {
		return fmt.Errorf("store: revogando token de deploy: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrDeployTokenNotFound
	}
	return nil
}
