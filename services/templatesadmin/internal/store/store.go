// Package store é a única parte do templatesadmin que fala SQL --
// persiste modelos de serviço (launch template + config de grupo) em
// Postgres (schema "service_templates", na mesma base partilhada que
// services/database serve a outros consumidores, ver
// services/database/README.md, "schema-per-service").
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" pro database/sql
)

var ErrNotFound = errors.New("store: modelo não encontrado")

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

// Template é um modelo de serviço tal como persistido e exposto pela API
// -- não há nada cifrado nem oculto aqui (ao contrário de secretsadmin):
// qualquer infra-admin que consiga listar também consegue ver o
// conteúdo inteiro.
type Template struct {
	Name  string   `json:"name"`
	Image string   `json:"image"`
	Cmd   []string `json:"cmd"`
	// Env pode conter referências "${secret:NOME}" -- resolvidas pelo
	// autoscaler no arranque de cada réplica, nunca por este serviço
	// (ver services/autoscaler/internal/secretsclient).
	Env        []string          `json:"env"`
	Labels     map[string]string `json:"labels"`
	Binds      []string          `json:"binds"`
	Network    string            `json:"network"`
	ExtraHosts []string          `json:"extraHosts"`

	TargetService       string  `json:"targetService"`
	BackendPort         int     `json:"backendPort"`
	MinReplicas         int     `json:"minReplicas"`
	MaxReplicas         int     `json:"maxReplicas"`
	CPUScaleUpPercent   float64 `json:"cpuScaleUpPercent"`
	CPUScaleDownPercent float64 `json:"cpuScaleDownPercent"`
	HostProxyPort       *int    `json:"hostProxyPort,omitempty"`
	HostMetricsPort     *int    `json:"hostMetricsPort,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy string    `json:"updatedBy"`
}

const listColumns = `name, image, cmd, env, labels, binds, network, extra_hosts,
	target_service, backend_port, min_replicas, max_replicas,
	cpu_scale_up_percent, cpu_scale_down_percent, host_proxy_port, host_metrics_port,
	created_at, updated_at, updated_by`

func scanTemplate(row interface{ Scan(...any) error }) (Template, error) {
	var (
		t                   Template
		cmdRaw, envRaw      []byte
		labelsRaw, bindsRaw []byte
		extraHostsRaw       []byte
	)
	var hostProxyPort, hostMetricsPort sql.NullInt64
	err := row.Scan(
		&t.Name, &t.Image, &cmdRaw, &envRaw, &labelsRaw, &bindsRaw, &t.Network, &extraHostsRaw,
		&t.TargetService, &t.BackendPort, &t.MinReplicas, &t.MaxReplicas,
		&t.CPUScaleUpPercent, &t.CPUScaleDownPercent, &hostProxyPort, &hostMetricsPort,
		&t.CreatedAt, &t.UpdatedAt, &t.UpdatedBy,
	)
	if err != nil {
		return Template{}, err
	}
	if hostProxyPort.Valid {
		v := int(hostProxyPort.Int64)
		t.HostProxyPort = &v
	}
	if hostMetricsPort.Valid {
		v := int(hostMetricsPort.Int64)
		t.HostMetricsPort = &v
	}
	if err := json.Unmarshal(cmdRaw, &t.Cmd); err != nil {
		return Template{}, fmt.Errorf("store: decodificando cmd: %w", err)
	}
	if err := json.Unmarshal(envRaw, &t.Env); err != nil {
		return Template{}, fmt.Errorf("store: decodificando env: %w", err)
	}
	if err := json.Unmarshal(labelsRaw, &t.Labels); err != nil {
		return Template{}, fmt.Errorf("store: decodificando labels: %w", err)
	}
	if err := json.Unmarshal(bindsRaw, &t.Binds); err != nil {
		return Template{}, fmt.Errorf("store: decodificando binds: %w", err)
	}
	if err := json.Unmarshal(extraHostsRaw, &t.ExtraHosts); err != nil {
		return Template{}, fmt.Errorf("store: decodificando extraHosts: %w", err)
	}
	return t, nil
}

// List devolve todos os modelos, ordenados por nome.
func (db *DB) List(ctx context.Context) ([]Template, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+listColumns+` FROM service_templates.templates ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: listando modelos: %w", err)
	}
	defer rows.Close()

	// Inicializado como slice vazio, não nil -- um nil aqui serializa
	// como JSON "null", e o portal (que usa null == "ainda a carregar")
	// nunca sairia do estado de loading quando não existe nenhum modelo.
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo modelo: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get devolve um modelo pelo nome.
func (db *DB) Get(ctx context.Context, name string) (Template, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+listColumns+` FROM service_templates.templates WHERE name = $1`, name)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("store: lendo modelo %q: %w", name, err)
	}
	return t, nil
}

// Upsert cria ou substitui um modelo -- não distingue "criar" de
// "atualizar" (mesma operação, mesma auditoria do lado do chamador).
func (db *DB) Upsert(ctx context.Context, t Template, updatedBy string) error {
	cmd, err := json.Marshal(nonNilSlice(t.Cmd))
	if err != nil {
		return fmt.Errorf("store: codificando cmd: %w", err)
	}
	env, err := json.Marshal(nonNilSlice(t.Env))
	if err != nil {
		return fmt.Errorf("store: codificando env: %w", err)
	}
	labels := t.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("store: codificando labels: %w", err)
	}
	binds, err := json.Marshal(nonNilSlice(t.Binds))
	if err != nil {
		return fmt.Errorf("store: codificando binds: %w", err)
	}
	extraHosts, err := json.Marshal(nonNilSlice(t.ExtraHosts))
	if err != nil {
		return fmt.Errorf("store: codificando extraHosts: %w", err)
	}

	_, err = db.sql.ExecContext(ctx, `
		INSERT INTO service_templates.templates (
			name, image, cmd, env, labels, binds, network, extra_hosts,
			target_service, backend_port, min_replicas, max_replicas,
			cpu_scale_up_percent, cpu_scale_down_percent, host_proxy_port, host_metrics_port,
			updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (name) DO UPDATE SET
			image = EXCLUDED.image,
			cmd = EXCLUDED.cmd,
			env = EXCLUDED.env,
			labels = EXCLUDED.labels,
			binds = EXCLUDED.binds,
			network = EXCLUDED.network,
			extra_hosts = EXCLUDED.extra_hosts,
			target_service = EXCLUDED.target_service,
			backend_port = EXCLUDED.backend_port,
			min_replicas = EXCLUDED.min_replicas,
			max_replicas = EXCLUDED.max_replicas,
			cpu_scale_up_percent = EXCLUDED.cpu_scale_up_percent,
			cpu_scale_down_percent = EXCLUDED.cpu_scale_down_percent,
			host_proxy_port = EXCLUDED.host_proxy_port,
			host_metrics_port = EXCLUDED.host_metrics_port,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
	`,
		t.Name, t.Image, cmd, env, labelsJSON, binds, t.Network, extraHosts,
		t.TargetService, t.BackendPort, t.MinReplicas, t.MaxReplicas,
		t.CPUScaleUpPercent, t.CPUScaleDownPercent, t.HostProxyPort, t.HostMetricsPort,
		updatedBy,
	)
	if err != nil {
		return fmt.Errorf("store: gravando modelo %q: %w", t.Name, err)
	}
	return nil
}

// Delete remove um modelo. Devolve ErrNotFound se não existia.
func (db *DB) Delete(ctx context.Context, name string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM service_templates.templates WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("store: apagando modelo %q: %w", name, err)
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

// nonNilSlice evita serializar "null" em vez de "[]" quando o campo veio
// vazio do JSON de entrada -- json.Unmarshal em []string deixa nil se a
// chave estava ausente, e "[]" é o valor que o resto do sistema (a UI, o
// autoscaler ao ler o launch template gerado) espera ver.
func nonNilSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
