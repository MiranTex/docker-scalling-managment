package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrNodeNotFound = errors.New("store: nó não encontrado")

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
}

const nodeColumns = `name, address, os, arch, cpus, memory_mb, docker_version, schedulable, created_at, last_seen_at`

func scanNode(row interface{ Scan(...any) error }) (Node, error) {
	var n Node
	err := row.Scan(&n.Name, &n.Address, &n.OS, &n.Arch, &n.CPUs, &n.MemoryMB, &n.DockerVersion, &n.Schedulable, &n.CreatedAt, &n.LastSeenAt)
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
