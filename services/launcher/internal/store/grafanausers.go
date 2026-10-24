package store

import (
	"context"
	"fmt"
	"time"
)

type GrafanaUser struct {
	UserID        string
	Email         string
	PlatformAdmin bool
	SeenAt        time.Time
}

func (db *DB) PutGrafanaUser(ctx context.Context, u GrafanaUser) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.grafana_users (user_id, email, platform_admin, seen_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id) DO UPDATE SET email = EXCLUDED.email, platform_admin = EXCLUDED.platform_admin, seen_at = now()`,
		u.UserID, u.Email, u.PlatformAdmin)
	if err != nil {
		return fmt.Errorf("store: gravando utilizador do Grafana: %w", err)
	}
	return nil
}

func (db *DB) ListGrafanaUsers(ctx context.Context) ([]GrafanaUser, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT user_id, email, platform_admin, seen_at FROM launcher.grafana_users`)
	if err != nil {
		return nil, fmt.Errorf("store: listando utilizadores do Grafana: %w", err)
	}
	defer rows.Close()
	var out []GrafanaUser
	for rows.Next() {
		var u GrafanaUser
		if err := rows.Scan(&u.UserID, &u.Email, &u.PlatformAdmin, &u.SeenAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListAllMembers devolve os membros de todos os projetos, para a
// sincronização com o Grafana.
func (db *DB) ListAllMembers(ctx context.Context) ([]Member, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT project_id, user_id, email, role, added_at, added_by FROM launcher.project_members`)
	if err != nil {
		return nil, fmt.Errorf("store: listando membros: %w", err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Email, &m.Role, &m.AddedAt, &m.AddedBy); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
