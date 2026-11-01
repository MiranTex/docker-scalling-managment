package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrAddonNotFound = errors.New("store: addon não encontrado")

const (
	AddonPostgres = "postgres"
	AddonRedis    = "redis"
	// AddonStorage: armazenamento de ficheiros com a API do S3 (SeaweedFS).
	AddonStorage = "storage"
)

type Addon struct {
	ID            string     `json:"id"`
	EnvironmentID string     `json:"environmentId"`
	Slug          string     `json:"slug"`
	Kind          string     `json:"kind"`
	Image         string     `json:"image"`
	EnvVar        string     `json:"envVar"`
	VCPU          float64    `json:"vcpu"`
	MemoryMB      int64      `json:"memoryMb"`
	BackupEnabled bool       `json:"backupEnabled"`
	LastBackupAt  *time.Time `json:"lastBackupAt,omitempty"`
	// PublicEndpoint: um storage com endereço público (ver
	// platform/storagepublic.go).
	PublicEndpoint bool `json:"publicEndpoint"`
	// DesiredState: running ou stopped (parado de propósito).
	DesiredState string    `json:"desiredState"`
	CreatedAt    time.Time `json:"createdAt"`
	CreatedBy    string    `json:"createdBy"`
}

const addonColumns = `id, environment_id, slug, kind, image, env_var, vcpu::float8, memory_mb,
	backup_enabled, last_backup_at, public_endpoint, desired_state, created_at, created_by`

func scanAddon(row interface{ Scan(...any) error }) (Addon, error) {
	var a Addon
	err := row.Scan(&a.ID, &a.EnvironmentID, &a.Slug, &a.Kind, &a.Image, &a.EnvVar, &a.VCPU, &a.MemoryMB,
		&a.BackupEnabled, &a.LastBackupAt, &a.PublicEndpoint, &a.DesiredState, &a.CreatedAt, &a.CreatedBy)
	return a, err
}

func (db *DB) queryAddons(ctx context.Context, query string, args ...any) ([]Addon, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listando addons: %w", err)
	}
	defer rows.Close()
	out := []Addon{}
	for rows.Next() {
		a, err := scanAddon(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo addon: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (db *DB) ListAddons(ctx context.Context, environmentID string) ([]Addon, error) {
	return db.queryAddons(ctx, `SELECT `+addonColumns+` FROM launcher.addons WHERE environment_id = $1 ORDER BY slug`, environmentID)
}

// ListAllAddons serve o reconciliador e o agendador de backups.
func (db *DB) ListAllAddons(ctx context.Context) ([]Addon, error) {
	return db.queryAddons(ctx, `SELECT `+addonColumns+` FROM launcher.addons ORDER BY created_at`)
}

func (db *DB) GetAddonBySlug(ctx context.Context, environmentID, slug string) (Addon, error) {
	a, err := scanAddon(db.sql.QueryRowContext(ctx, `SELECT `+addonColumns+` FROM launcher.addons WHERE environment_id = $1 AND slug = $2`, environmentID, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return Addon{}, ErrAddonNotFound
	}
	if err != nil {
		return Addon{}, fmt.Errorf("store: lendo addon %q: %w", slug, err)
	}
	return a, nil
}

func (db *DB) CreateAddon(ctx context.Context, a Addon) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.addons (id, environment_id, slug, kind, image, env_var, vcpu, memory_mb, backup_enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		a.ID, a.EnvironmentID, a.Slug, a.Kind, a.Image, a.EnvVar, a.VCPU, a.MemoryMB, a.BackupEnabled, a.CreatedBy)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: gravando addon %q: %w", a.Slug, err)
	}
	return nil
}

func (db *DB) DeleteAddon(ctx context.Context, id string) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.addons WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: apagando addon %q: %w", id, err)
	}
	return nil
}

func (db *DB) SetAddonBackupEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.addons SET backup_enabled = $2 WHERE id = $1`, id, enabled)
	if err != nil {
		return fmt.Errorf("store: atualizando addon %q: %w", id, err)
	}
	return nil
}

func (db *DB) SetAddonDesiredState(ctx context.Context, id, state string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.addons SET desired_state = $2 WHERE id = $1`, id, state)
	if err != nil {
		return fmt.Errorf("store: atualizando estado do addon %q: %w", id, err)
	}
	return nil
}

func (db *DB) MarkAddonBackedUp(ctx context.Context, id string, at time.Time) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.addons SET last_backup_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("store: marcando backup do addon %q: %w", id, err)
	}
	return nil
}
