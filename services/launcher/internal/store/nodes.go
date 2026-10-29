package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNodeNotFound = errors.New("store: nó não encontrado")
	// ErrJoinTokenInvalid: o token de entrada não existe, expirou ou já
	// foi usado.
	ErrJoinTokenInvalid = errors.New("store: token de entrada inválido, expirado ou já usado")
)

const (
	NodeLocal = "local"
	NodeAgent = "agent"
)

// Node é uma máquina onde correm containers dos projetos.
type Node struct {
	Name          string    `json:"name"`
	Address       string    `json:"address"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"` // amd64, arm64
	CPUs          int       `json:"cpus"`
	MemoryMB      int64     `json:"memoryMb"`
	DockerVersion string    `json:"dockerVersion"`
	Schedulable   bool      `json:"schedulable"`
	CreatedAt     time.Time `json:"createdAt"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
	Kind          string    `json:"kind"` // NodeLocal ou NodeAgent
	AgentVersion  string    `json:"agentVersion,omitempty"`
}

const nodeColumns = `name, address, os, arch, cpus, memory_mb, docker_version, schedulable, created_at, last_seen_at, kind, agent_version`

func scanNode(row interface{ Scan(...any) error }) (Node, error) {
	var n Node
	err := row.Scan(&n.Name, &n.Address, &n.OS, &n.Arch, &n.CPUs, &n.MemoryMB, &n.DockerVersion, &n.Schedulable, &n.CreatedAt, &n.LastSeenAt, &n.Kind, &n.AgentVersion)
	return n, err
}

// PutNodeFacts regista um nó ou atualiza o que ele reporta (recursos,
// versão) e o last_seen_at. schedulable é decisão de um admin: um nó novo
// entra a aceitar réplicas e um que já existe mantém o que tinha.
func (db *DB) PutNodeFacts(ctx context.Context, n Node) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.nodes (name, address, os, arch, cpus, memory_mb, docker_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (name) DO UPDATE SET address = EXCLUDED.address, os = EXCLUDED.os, arch = EXCLUDED.arch,
			cpus = EXCLUDED.cpus, memory_mb = EXCLUDED.memory_mb, docker_version = EXCLUDED.docker_version, last_seen_at = now()`,
		n.Name, n.Address, n.OS, n.Arch, n.CPUs, n.MemoryMB, n.DockerVersion)
	if err != nil {
		return fmt.Errorf("store: gravando nó %s: %w", n.Name, err)
	}
	return nil
}

func (db *DB) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+nodeColumns+` FROM launcher.nodes ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: listando nós: %w", err)
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (db *DB) GetNode(ctx context.Context, name string) (Node, error) {
	n, err := scanNode(db.sql.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM launcher.nodes WHERE name = $1`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNodeNotFound
	}
	return n, err
}

func (db *DB) SetNodeSchedulable(ctx context.Context, name string, schedulable bool) error {
	res, err := db.sql.ExecContext(ctx, `UPDATE launcher.nodes SET schedulable = $2 WHERE name = $1`, name, schedulable)
	if err != nil {
		return fmt.Errorf("store: alterando nó %s: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNodeNotFound
	}
	return nil
}

// UpdateNodeFacts atualiza o que um nó que já existe reporta, e o
// last_seen_at.
func (db *DB) UpdateNodeFacts(ctx context.Context, n Node) error {
	res, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.nodes SET address = $2, os = $3, arch = $4, cpus = $5, memory_mb = $6, docker_version = $7,
			agent_version = $8, last_seen_at = now() WHERE name = $1`,
		n.Name, n.Address, n.OS, n.Arch, n.CPUs, n.MemoryMB, n.DockerVersion, n.AgentVersion)
	if err != nil {
		return fmt.Errorf("store: atualizando nó %s: %w", n.Name, err)
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (db *DB) TouchNode(ctx context.Context, name string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.nodes SET last_seen_at = now() WHERE name = $1`, name)
	return err
}

func (db *DB) CreateNodeJoinToken(ctx context.Context, tokenHash, by string, expires time.Time) error {
	_, err := db.sql.ExecContext(ctx, `INSERT INTO launcher.node_join_tokens (token_hash, created_by, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, by, expires)
	if err != nil {
		return fmt.Errorf("store: criando token de entrada: %w", err)
	}
	return nil
}

// JoinNode gasta o token de entrada e regista o nó com a sua credencial,
// numa só transação: um token só serve uma vez, e um nome que já existe
// não é tomado (ErrConflict, e o token não se gasta).
func (db *DB) JoinNode(ctx context.Context, joinHash string, n Node, nodeTokenHash string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE launcher.node_join_tokens SET used_at = now(), used_by_node = $2
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`, joinHash, n.Name)
	if err != nil {
		return fmt.Errorf("store: usando token de entrada: %w", err)
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrJoinTokenInvalid
	}
	res, err = tx.ExecContext(ctx, `
		INSERT INTO launcher.nodes (name, address, os, arch, cpus, memory_mb, kind, token_hash)
		VALUES ($1, $2, '', '', 0, 0, 'agent', $3) ON CONFLICT (name) DO NOTHING`, n.Name, n.Address, nodeTokenHash)
	if err != nil {
		return fmt.Errorf("store: registando nó %s: %w", n.Name, err)
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return fmt.Errorf("%w: já existe um nó %q", ErrConflict, n.Name)
	}
	return tx.Commit()
}

// NodeByToken encontra o nó remoto da credencial do agente.
func (db *DB) NodeByToken(ctx context.Context, tokenHash string) (Node, error) {
	n, err := scanNode(db.sql.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM launcher.nodes WHERE kind = 'agent' AND token_hash = $1`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNodeNotFound
	}
	return n, err
}

func (db *DB) DeleteNode(ctx context.Context, name string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.nodes WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("store: apagando nó %s: %w", name, err)
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNodeNotFound
	}
	return nil
}
