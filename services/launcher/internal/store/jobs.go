package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrJobNotFound = errors.New("store: job não encontrado")

const (
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

type Job struct {
	ID            string     `json:"id"`
	EnvironmentID string     `json:"environmentId"`
	AddonID       *string    `json:"addonId,omitempty"`
	ServiceID     *string    `json:"serviceId,omitempty"`
	RevisionID    *string    `json:"revisionId,omitempty"`
	Kind          string     `json:"kind"`
	Image         string     `json:"image"`
	Cmd           []string   `json:"cmd"`
	Status        string     `json:"status"`
	ExitCode      *int       `json:"exitCode,omitempty"`
	Output        string     `json:"output"`
	CreatedAt     time.Time  `json:"createdAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
	CreatedBy     string     `json:"createdBy"`
}

const jobColumns = `id, environment_id, addon_id, service_id, revision_id, kind, image, cmd, status, exit_code, output, created_at, finished_at, created_by`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var cmd []byte
	err := row.Scan(&j.ID, &j.EnvironmentID, &j.AddonID, &j.ServiceID, &j.RevisionID, &j.Kind, &j.Image, &cmd,
		&j.Status, &j.ExitCode, &j.Output, &j.CreatedAt, &j.FinishedAt, &j.CreatedBy)
	if err != nil {
		return Job{}, err
	}
	if err := json.Unmarshal(cmd, &j.Cmd); err != nil {
		return Job{}, fmt.Errorf("store: cmd do job %s: %w", j.ID, err)
	}
	return j, nil
}

// ListJobs devolve os jobs mais recentes de um ambiente (sem o output,
// que pode ser grande -- para isso há GetJob).
func (db *DB) ListJobs(ctx context.Context, environmentID string, limit int) ([]Job, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+jobColumns+` FROM launcher.jobs WHERE environment_id = $1 ORDER BY created_at DESC LIMIT $2`, environmentID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listando jobs: %w", err)
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo job: %w", err)
		}
		j.Output = ""
		out = append(out, j)
	}
	return out, rows.Err()
}

func (db *DB) GetJob(ctx context.Context, environmentID, id string) (Job, error) {
	j, err := scanJob(db.sql.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM launcher.jobs WHERE environment_id = $1 AND id = $2`, environmentID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("store: lendo job %q: %w", id, err)
	}
	return j, nil
}

func (db *DB) CreateJob(ctx context.Context, j Job) error {
	cmd, err := json.Marshal(j.Cmd)
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx, `
		INSERT INTO launcher.jobs (id, environment_id, addon_id, service_id, revision_id, kind, image, cmd, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		j.ID, j.EnvironmentID, j.AddonID, j.ServiceID, j.RevisionID, j.Kind, j.Image, cmd, j.Status, j.CreatedBy)
	if err != nil {
		return fmt.Errorf("store: gravando job: %w", err)
	}
	return nil
}

func (db *DB) FinishJob(ctx context.Context, id, status string, exitCode *int, output string) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.jobs SET status = $2, exit_code = $3, output = $4, finished_at = now() WHERE id = $1`,
		id, status, exitCode, output)
	if err != nil {
		return fmt.Errorf("store: terminando job %q: %w", id, err)
	}
	return nil
}

// FailRunningJobs marca como falhados os jobs que ficaram "running" de um
// arranque anterior do launcher -- o processo que os esperava morreu.
func (db *DB) FailRunningJobs(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.jobs SET status = 'failed', output = output || E'\n[interrompido: o launcher reiniciou]', finished_at = now()
		WHERE status = 'running'`)
	return err
}
