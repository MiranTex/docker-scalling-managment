package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrServiceNotFound    = errors.New("store: serviço não encontrado")
	ErrRevisionNotFound   = errors.New("store: revisão não encontrada")
	ErrDeploymentNotFound = errors.New("store: deploy não encontrado")
	ErrRegistryNotFound   = errors.New("store: credencial de registry não encontrada")
)

const (
	ServiceWeb    = "web"
	ServiceWorker = "worker"

	ScalingFixed = "fixed"
	ScalingAuto  = "auto"

	DesiredRunning = "running"
	DesiredStopped = "stopped"

	DeployPending   = "pending"
	DeployPulling   = "pulling"
	DeployReleasing = "releasing"
	DeployStarting  = "starting"
	DeploySwitching = "switching"
	DeploySucceeded = "succeeded"
	DeployFailed    = "failed"
)

type Service struct {
	ID                   string    `json:"id"`
	EnvironmentID        string    `json:"environmentId"`
	Slug                 string    `json:"slug"`
	Kind                 string    `json:"kind"`
	Port                 int       `json:"port"`
	HealthPath           string    `json:"healthPath"`
	HealthTimeoutSeconds int       `json:"healthTimeoutSeconds"`
	Cmd                  []string  `json:"cmd"`
	Env                  []string  `json:"env"`
	ReleaseCmd           []string  `json:"releaseCmd"`
	VCPU                 float64   `json:"vcpu"`
	MemoryMB             int64     `json:"memoryMb"`
	ScalingMode          string    `json:"scalingMode"`
	Replicas             int       `json:"replicas"`
	MinReplicas          int       `json:"minReplicas"`
	MaxReplicas          int       `json:"maxReplicas"`
	CPUScaleUpPercent    float64   `json:"cpuScaleUpPercent"`
	CPUScaleDownPercent  float64   `json:"cpuScaleDownPercent"`
	DesiredState         string    `json:"desiredState"`
	CurrentRevisionID    *string   `json:"currentRevisionId,omitempty"`
	MetricsPort          int       `json:"metricsPort"`
	MetricsPath          string    `json:"metricsPath"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
	CreatedBy            string    `json:"createdBy"`
}

const serviceColumns = `id, environment_id, slug, kind, port, health_path, health_timeout_seconds, cmd, env, release_cmd,
	vcpu::float8, memory_mb, scaling_mode, replicas, min_replicas, max_replicas, cpu_scale_up_percent, cpu_scale_down_percent,
	desired_state, current_revision_id, metrics_port, metrics_path, created_at, updated_at, created_by`

func scanService(row interface{ Scan(...any) error }) (Service, error) {
	var s Service
	var cmd, env, release []byte
	err := row.Scan(&s.ID, &s.EnvironmentID, &s.Slug, &s.Kind, &s.Port, &s.HealthPath, &s.HealthTimeoutSeconds, &cmd, &env, &release,
		&s.VCPU, &s.MemoryMB, &s.ScalingMode, &s.Replicas, &s.MinReplicas, &s.MaxReplicas, &s.CPUScaleUpPercent, &s.CPUScaleDownPercent,
		&s.DesiredState, &s.CurrentRevisionID, &s.MetricsPort, &s.MetricsPath, &s.CreatedAt, &s.UpdatedAt, &s.CreatedBy)
	if err != nil {
		return Service{}, err
	}
	if err := unmarshalLists(map[*[]string][]byte{&s.Cmd: cmd, &s.Env: env, &s.ReleaseCmd: release}); err != nil {
		return Service{}, fmt.Errorf("store: serviço %s: %w", s.ID, err)
	}
	return s, nil
}

func unmarshalLists(fields map[*[]string][]byte) error {
	for dst, raw := range fields {
		if err := json.Unmarshal(raw, dst); err != nil {
			return err
		}
		if *dst == nil {
			*dst = []string{}
		}
	}
	return nil
}

func marshalList(l []string) []byte {
	if l == nil {
		l = []string{}
	}
	b, _ := json.Marshal(l)
	return b
}

func (db *DB) queryServices(ctx context.Context, query string, args ...any) ([]Service, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listando serviços: %w", err)
	}
	defer rows.Close()
	out := []Service{}
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo serviço: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) ListServices(ctx context.Context, environmentID string) ([]Service, error) {
	return db.queryServices(ctx, `SELECT `+serviceColumns+` FROM launcher.services WHERE environment_id = $1 ORDER BY slug`, environmentID)
}

func (db *DB) ListAllServices(ctx context.Context) ([]Service, error) {
	return db.queryServices(ctx, `SELECT `+serviceColumns+` FROM launcher.services ORDER BY created_at`)
}

func (db *DB) GetService(ctx context.Context, id string) (Service, error) {
	s, err := scanService(db.sql.QueryRowContext(ctx, `SELECT `+serviceColumns+` FROM launcher.services WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Service{}, ErrServiceNotFound
	}
	if err != nil {
		return Service{}, fmt.Errorf("store: lendo serviço %q: %w", id, err)
	}
	return s, nil
}

func (db *DB) GetServiceBySlug(ctx context.Context, environmentID, slug string) (Service, error) {
	s, err := scanService(db.sql.QueryRowContext(ctx, `SELECT `+serviceColumns+` FROM launcher.services WHERE environment_id = $1 AND slug = $2`, environmentID, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return Service{}, ErrServiceNotFound
	}
	if err != nil {
		return Service{}, fmt.Errorf("store: lendo serviço %q: %w", slug, err)
	}
	return s, nil
}

func (db *DB) CreateService(ctx context.Context, s Service) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.services (id, environment_id, slug, kind, port, health_path, health_timeout_seconds, cmd, env, release_cmd,
			vcpu, memory_mb, scaling_mode, replicas, min_replicas, max_replicas, cpu_scale_up_percent, cpu_scale_down_percent,
			desired_state, created_by, metrics_port, metrics_path)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`,
		s.ID, s.EnvironmentID, s.Slug, s.Kind, s.Port, s.HealthPath, s.HealthTimeoutSeconds,
		marshalList(s.Cmd), marshalList(s.Env), marshalList(s.ReleaseCmd),
		s.VCPU, s.MemoryMB, s.ScalingMode, s.Replicas, s.MinReplicas, s.MaxReplicas, s.CPUScaleUpPercent, s.CPUScaleDownPercent,
		s.DesiredState, s.CreatedBy, s.MetricsPort, s.MetricsPath)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: gravando serviço %q: %w", s.Slug, err)
	}
	return nil
}

// UpdateService grava toda a configuração editável (não mexe na revisão atual).
func (db *DB) UpdateService(ctx context.Context, s Service) error {
	res, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.services SET port = $2, health_path = $3, health_timeout_seconds = $4, cmd = $5, env = $6, release_cmd = $7,
			vcpu = $8, memory_mb = $9, scaling_mode = $10, replicas = $11, min_replicas = $12, max_replicas = $13,
			cpu_scale_up_percent = $14, cpu_scale_down_percent = $15, desired_state = $16,
			metrics_port = $17, metrics_path = $18, updated_at = now()
		WHERE id = $1`,
		s.ID, s.Port, s.HealthPath, s.HealthTimeoutSeconds, marshalList(s.Cmd), marshalList(s.Env), marshalList(s.ReleaseCmd),
		s.VCPU, s.MemoryMB, s.ScalingMode, s.Replicas, s.MinReplicas, s.MaxReplicas,
		s.CPUScaleUpPercent, s.CPUScaleDownPercent, s.DesiredState, s.MetricsPort, s.MetricsPath)
	if err != nil {
		return fmt.Errorf("store: atualizando serviço %q: %w", s.Slug, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrServiceNotFound
	}
	return nil
}

func (db *DB) SetCurrentRevision(ctx context.Context, serviceID, revisionID string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.services SET current_revision_id = $2, updated_at = now() WHERE id = $1`, serviceID, revisionID)
	if err != nil {
		return fmt.Errorf("store: trocando revisão do serviço %q: %w", serviceID, err)
	}
	return nil
}

func (db *DB) DeleteService(ctx context.Context, id string) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.services WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: apagando serviço %q: %w", id, err)
	}
	return nil
}

type Revision struct {
	ID                   string    `json:"id"`
	ServiceID            string    `json:"serviceId"`
	Number               int       `json:"number"`
	Image                string    `json:"image"`
	Cmd                  []string  `json:"cmd"`
	Env                  []string  `json:"env"`
	ReleaseCmd           []string  `json:"releaseCmd"`
	Port                 int       `json:"port"`
	HealthPath           string    `json:"healthPath"`
	HealthTimeoutSeconds int       `json:"healthTimeoutSeconds"`
	VCPU                 float64   `json:"vcpu"`
	MemoryMB             int64     `json:"memoryMb"`
	MetricsPort          int       `json:"metricsPort"`
	MetricsPath          string    `json:"metricsPath"`
	CreatedAt            time.Time `json:"createdAt"`
	CreatedBy            string    `json:"createdBy"`
}

const revisionColumns = `id, service_id, number, image, cmd, env, release_cmd, port, health_path, health_timeout_seconds,
	vcpu::float8, memory_mb, metrics_port, metrics_path, created_at, created_by`

func scanRevision(row interface{ Scan(...any) error }) (Revision, error) {
	var r Revision
	var cmd, env, release []byte
	err := row.Scan(&r.ID, &r.ServiceID, &r.Number, &r.Image, &cmd, &env, &release, &r.Port, &r.HealthPath, &r.HealthTimeoutSeconds,
		&r.VCPU, &r.MemoryMB, &r.MetricsPort, &r.MetricsPath, &r.CreatedAt, &r.CreatedBy)
	if err != nil {
		return Revision{}, err
	}
	if err := unmarshalLists(map[*[]string][]byte{&r.Cmd: cmd, &r.Env: env, &r.ReleaseCmd: release}); err != nil {
		return Revision{}, fmt.Errorf("store: revisão %s: %w", r.ID, err)
	}
	return r, nil
}

// CreateRevision grava uma revisão com o número seguinte do serviço. O
// UNIQUE (service_id, number) apanha dois deploys em simultâneo.
func (db *DB) CreateRevision(ctx context.Context, r Revision) (Revision, error) {
	row := db.sql.QueryRowContext(ctx, `
		INSERT INTO launcher.revisions (id, service_id, number, image, cmd, env, release_cmd, port, health_path, health_timeout_seconds,
			vcpu, memory_mb, created_by, metrics_port, metrics_path)
		VALUES ($1, $2, (SELECT COALESCE(MAX(number), 0) + 1 FROM launcher.revisions WHERE service_id = $2),
			$3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING `+revisionColumns,
		r.ID, r.ServiceID, r.Image, marshalList(r.Cmd), marshalList(r.Env), marshalList(r.ReleaseCmd),
		r.Port, r.HealthPath, r.HealthTimeoutSeconds, r.VCPU, r.MemoryMB, r.CreatedBy, r.MetricsPort, r.MetricsPath)
	out, err := scanRevision(row)
	if isUniqueViolation(err) {
		return Revision{}, ErrConflict
	}
	if err != nil {
		return Revision{}, fmt.Errorf("store: gravando revisão: %w", err)
	}
	return out, nil
}

func (db *DB) GetRevision(ctx context.Context, id string) (Revision, error) {
	r, err := scanRevision(db.sql.QueryRowContext(ctx, `SELECT `+revisionColumns+` FROM launcher.revisions WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrRevisionNotFound
	}
	if err != nil {
		return Revision{}, fmt.Errorf("store: lendo revisão %q: %w", id, err)
	}
	return r, nil
}

func (db *DB) GetRevisionByNumber(ctx context.Context, serviceID string, number int) (Revision, error) {
	r, err := scanRevision(db.sql.QueryRowContext(ctx, `SELECT `+revisionColumns+` FROM launcher.revisions WHERE service_id = $1 AND number = $2`, serviceID, number))
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrRevisionNotFound
	}
	if err != nil {
		return Revision{}, fmt.Errorf("store: lendo revisão %d: %w", number, err)
	}
	return r, nil
}

func (db *DB) ListRevisions(ctx context.Context, serviceID string, limit int) ([]Revision, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+revisionColumns+` FROM launcher.revisions WHERE service_id = $1 ORDER BY number DESC LIMIT $2`, serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listando revisões: %w", err)
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo revisão: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type Deployment struct {
	ID          string     `json:"id"`
	ServiceID   string     `json:"serviceId"`
	RevisionID  string     `json:"revisionId"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	Log         string     `json:"log"`
	TriggeredBy string     `json:"triggeredBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	// RevisionNumber/Image vêm da revisão (JOIN), para a lista de deploys
	// se ler sem uma chamada por linha.
	RevisionNumber int    `json:"revisionNumber"`
	Image          string `json:"image"`
}

const deploymentColumns = `d.id, d.service_id, d.revision_id, d.status, d.error, d.log, d.triggered_by, d.created_at, d.finished_at, r.number, r.image`

func scanDeployment(row interface{ Scan(...any) error }) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.ServiceID, &d.RevisionID, &d.Status, &d.Error, &d.Log, &d.TriggeredBy, &d.CreatedAt, &d.FinishedAt, &d.RevisionNumber, &d.Image)
	return d, err
}

func (db *DB) CreateDeployment(ctx context.Context, d Deployment) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.deployments (id, service_id, revision_id, status, triggered_by) VALUES ($1, $2, $3, $4, $5)`,
		d.ID, d.ServiceID, d.RevisionID, d.Status, d.TriggeredBy)
	if err != nil {
		return fmt.Errorf("store: gravando deploy: %w", err)
	}
	return nil
}

func (db *DB) GetDeployment(ctx context.Context, serviceID, id string) (Deployment, error) {
	d, err := scanDeployment(db.sql.QueryRowContext(ctx, `
		SELECT `+deploymentColumns+` FROM launcher.deployments d JOIN launcher.revisions r ON r.id = d.revision_id
		WHERE d.service_id = $1 AND d.id = $2`, serviceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrDeploymentNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("store: lendo deploy %q: %w", id, err)
	}
	return d, nil
}

func (db *DB) ListDeployments(ctx context.Context, serviceID string, limit int) ([]Deployment, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+deploymentColumns+` FROM launcher.deployments d JOIN launcher.revisions r ON r.id = d.revision_id
		WHERE d.service_id = $1 ORDER BY d.created_at DESC LIMIT $2`, serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listando deploys: %w", err)
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo deploy: %w", err)
		}
		d.Log = ""
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDeployment muda o estado e acrescenta uma linha ao log.
func (db *DB) UpdateDeployment(ctx context.Context, id, status, logLine string) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.deployments SET status = $2,
			log = log || to_char(now() AT TIME ZONE 'UTC', 'HH24:MI:SS') || ' ' || $3 || E'\n'
		WHERE id = $1`, id, status, logLine)
	if err != nil {
		return fmt.Errorf("store: atualizando deploy %q: %w", id, err)
	}
	return nil
}

// FinishDeployment fecha o deploy numa só escrita (estado, erro, última
// linha do log) -- quem lê nunca vê "failed" sem a causa.
func (db *DB) FinishDeployment(ctx context.Context, id, status, errMsg, logLine string) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.deployments SET status = $2, error = $3, finished_at = now(),
			log = log || to_char(now() AT TIME ZONE 'UTC', 'HH24:MI:SS') || ' ' || $4 || E'\n'
		WHERE id = $1`, id, status, errMsg, logLine)
	if err != nil {
		return fmt.Errorf("store: terminando deploy %q: %w", id, err)
	}
	return nil
}

// FailUnfinishedDeployments: deploys a meio quando o launcher reiniciou.
// A revisão atual de cada serviço só muda no fim de um deploy, por isso a
// anterior continua válida.
func (db *DB) FailUnfinishedDeployments(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.deployments SET status = 'failed', error = 'interrompido: o launcher reiniciou a meio', finished_at = now()
		WHERE status NOT IN ('succeeded', 'failed')`)
	return err
}

type RegistryCredential struct {
	ProjectID  string    `json:"-"`
	Registry   string    `json:"registry"`
	Username   string    `json:"username"`
	Ciphertext []byte    `json:"-"`
	Nonce      []byte    `json:"-"`
	UpdatedAt  time.Time `json:"updatedAt"`
	UpdatedBy  string    `json:"updatedBy"`
}

func (db *DB) ListRegistryCredentials(ctx context.Context, projectID string) ([]RegistryCredential, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT project_id, registry, username, ciphertext, nonce, updated_at, updated_by
		FROM launcher.registry_credentials WHERE project_id = $1 ORDER BY registry`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listando registries: %w", err)
	}
	defer rows.Close()
	out := []RegistryCredential{}
	for rows.Next() {
		var c RegistryCredential
		if err := rows.Scan(&c.ProjectID, &c.Registry, &c.Username, &c.Ciphertext, &c.Nonce, &c.UpdatedAt, &c.UpdatedBy); err != nil {
			return nil, fmt.Errorf("store: lendo registry: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *DB) PutRegistryCredential(ctx context.Context, c RegistryCredential) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.registry_credentials (project_id, registry, username, ciphertext, nonce, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (project_id, registry) DO UPDATE
		SET username = EXCLUDED.username, ciphertext = EXCLUDED.ciphertext, nonce = EXCLUDED.nonce,
			updated_by = EXCLUDED.updated_by, updated_at = now()`,
		c.ProjectID, c.Registry, c.Username, c.Ciphertext, c.Nonce, c.UpdatedBy)
	if err != nil {
		return fmt.Errorf("store: gravando registry %q: %w", c.Registry, err)
	}
	return nil
}

func (db *DB) DeleteRegistryCredential(ctx context.Context, projectID, registry string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.registry_credentials WHERE project_id = $1 AND registry = $2`, projectID, registry)
	if err != nil {
		return fmt.Errorf("store: apagando registry %q: %w", registry, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRegistryNotFound
	}
	return nil
}
