package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrMemberNotFound = errors.New("store: membro não encontrado")

const (
	MemberViewer    = "viewer"
	MemberDeveloper = "developer"
	MemberAdmin     = "admin"
)

type Member struct {
	ProjectID string    `json:"-"`
	UserID    string    `json:"userId"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	AddedAt   time.Time `json:"addedAt"`
	AddedBy   string    `json:"addedBy"`
}

func (db *DB) ListMembers(ctx context.Context, projectID string) ([]Member, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT project_id, user_id, email, role, added_at, added_by
		FROM launcher.project_members WHERE project_id = $1 ORDER BY email`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listando membros: %w", err)
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Email, &m.Role, &m.AddedAt, &m.AddedBy); err != nil {
			return nil, fmt.Errorf("store: lendo membro: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (db *DB) GetMember(ctx context.Context, projectID, userID string) (Member, error) {
	var m Member
	err := db.sql.QueryRowContext(ctx, `
		SELECT project_id, user_id, email, role, added_at, added_by
		FROM launcher.project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID).
		Scan(&m.ProjectID, &m.UserID, &m.Email, &m.Role, &m.AddedAt, &m.AddedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrMemberNotFound
	}
	if err != nil {
		return Member{}, fmt.Errorf("store: lendo membro: %w", err)
	}
	return m, nil
}

// PutMember acrescenta o membro ou muda-lhe o papel.
func (db *DB) PutMember(ctx context.Context, m Member) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.project_members (project_id, user_id, email, role, added_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role, email = EXCLUDED.email`,
		m.ProjectID, m.UserID, m.Email, m.Role, m.AddedBy)
	if err != nil {
		return fmt.Errorf("store: gravando membro: %w", err)
	}
	return nil
}

func (db *DB) DeleteMember(ctx context.Context, projectID, userID string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID)
	if err != nil {
		return fmt.Errorf("store: removendo membro: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// ListProjectsForUser devolve os projetos de que userID é membro.
func (db *DB) ListProjectsForUser(ctx context.Context, accountID, userID string) ([]Project, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+projectColumns+` FROM launcher.projects
		WHERE account_id = $1 AND id IN (SELECT project_id FROM launcher.project_members WHERE user_id = $2)
		ORDER BY slug`, accountID, userID)
	if err != nil {
		return nil, fmt.Errorf("store: listando projetos do membro: %w", err)
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
