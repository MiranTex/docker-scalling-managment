package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrTemplateNotFound        = errors.New("store: modelo não encontrado")
	ErrTemplateVersionNotFound = errors.New("store: versão do modelo não encontrada")
)

type Template struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"projectId"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"createdAt"`
	CreatedBy   string     `json:"createdBy"`
	ArchivedAt  *time.Time `json:"archivedAt,omitempty"`
	// LatestVersion e UsedBy vêm de agregados na listagem.
	LatestVersion int `json:"latestVersion"`
	UsedBy        int `json:"usedBy"`
}

type TemplateVersion struct {
	ID         string    `json:"id"`
	TemplateID string    `json:"templateId"`
	Version    int       `json:"version"`
	Spec       []byte    `json:"-"`
	Image      string    `json:"image"`
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"createdAt"`
	CreatedBy  string    `json:"createdBy"`
}

const templateColumns = `t.id, t.project_id, t.slug, t.name, t.description, t.created_at, t.created_by, t.archived_at,
	COALESCE((SELECT MAX(version) FROM launcher.template_versions v WHERE v.template_id = t.id), 0),
	(SELECT COUNT(*) FROM launcher.services s WHERE s.template_id = t.id)`

func scanTemplate(row interface{ Scan(...any) error }) (Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.ProjectID, &t.Slug, &t.Name, &t.Description, &t.CreatedAt, &t.CreatedBy, &t.ArchivedAt, &t.LatestVersion, &t.UsedBy)
	return t, err
}

func (db *DB) ListTemplates(ctx context.Context, projectID string) ([]Template, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+templateColumns+` FROM launcher.project_templates t WHERE t.project_id = $1 ORDER BY t.slug`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listando modelos: %w", err)
	}
	defer rows.Close()
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

func (db *DB) GetTemplateBySlug(ctx context.Context, projectID, slug string) (Template, error) {
	t, err := scanTemplate(db.sql.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM launcher.project_templates t WHERE t.project_id = $1 AND t.slug = $2`, projectID, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("store: lendo modelo %q: %w", slug, err)
	}
	return t, nil
}

func (db *DB) GetTemplate(ctx context.Context, id string) (Template, error) {
	t, err := scanTemplate(db.sql.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM launcher.project_templates t WHERE t.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("store: lendo modelo %q: %w", id, err)
	}
	return t, nil
}

// CreateTemplate grava o modelo e a sua versão 1 numa só transação --
// um modelo sem versões não serve para nada.
func (db *DB) CreateTemplate(ctx context.Context, t Template, v TemplateVersion) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO launcher.project_templates (id, project_id, slug, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)`, t.ID, t.ProjectID, t.Slug, t.Name, t.Description, t.CreatedBy)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: gravando modelo %q: %w", t.Slug, err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO launcher.template_versions (id, template_id, version, spec, image, notes, created_by)
		VALUES ($1, $2, 1, $3, $4, $5, $6)`, v.ID, t.ID, v.Spec, v.Image, v.Notes, v.CreatedBy)
	if err != nil {
		return fmt.Errorf("store: gravando versão 1 de %q: %w", t.Slug, err)
	}
	return tx.Commit()
}

// UpdateTemplateInfo muda nome e descrição -- não é configuração, por isso
// não cria versão.
func (db *DB) UpdateTemplateInfo(ctx context.Context, id, name, description string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.project_templates SET name = $2, description = $3 WHERE id = $1`, id, name, description)
	return err
}

func (db *DB) ArchiveTemplate(ctx context.Context, id string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.project_templates SET archived_at = now() WHERE id = $1 AND archived_at IS NULL`, id)
	return err
}

const templateVersionColumns = `id, template_id, version, spec, image, notes, created_at, created_by`

func scanTemplateVersion(row interface{ Scan(...any) error }) (TemplateVersion, error) {
	var v TemplateVersion
	err := row.Scan(&v.ID, &v.TemplateID, &v.Version, &v.Spec, &v.Image, &v.Notes, &v.CreatedAt, &v.CreatedBy)
	return v, err
}

// CreateTemplateVersion grava a versão seguinte; o UNIQUE apanha duas
// gravações em simultâneo.
func (db *DB) CreateTemplateVersion(ctx context.Context, v TemplateVersion) (TemplateVersion, error) {
	out, err := scanTemplateVersion(db.sql.QueryRowContext(ctx, `
		INSERT INTO launcher.template_versions (id, template_id, version, spec, image, notes, created_by)
		VALUES ($1, $2, (SELECT COALESCE(MAX(version), 0) + 1 FROM launcher.template_versions WHERE template_id = $2), $3, $4, $5, $6)
		RETURNING `+templateVersionColumns, v.ID, v.TemplateID, v.Spec, v.Image, v.Notes, v.CreatedBy))
	if isUniqueViolation(err) {
		return TemplateVersion{}, ErrConflict
	}
	if err != nil {
		return TemplateVersion{}, fmt.Errorf("store: gravando versão do modelo: %w", err)
	}
	return out, nil
}

func (db *DB) GetTemplateVersion(ctx context.Context, templateID string, version int) (TemplateVersion, error) {
	v, err := scanTemplateVersion(db.sql.QueryRowContext(ctx, `SELECT `+templateVersionColumns+` FROM launcher.template_versions WHERE template_id = $1 AND version = $2`, templateID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return TemplateVersion{}, ErrTemplateVersionNotFound
	}
	if err != nil {
		return TemplateVersion{}, fmt.Errorf("store: lendo versão %d: %w", version, err)
	}
	return v, nil
}

func (db *DB) ListTemplateVersions(ctx context.Context, templateID string) ([]TemplateVersion, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+templateVersionColumns+` FROM launcher.template_versions WHERE template_id = $1 ORDER BY version DESC`, templateID)
	if err != nil {
		return nil, fmt.Errorf("store: listando versões: %w", err)
	}
	defer rows.Close()
	out := []TemplateVersion{}
	for rows.Next() {
		v, err := scanTemplateVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo versão: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetServiceTemplate regista de que modelo/versão vem um serviço.
func (db *DB) SetServiceTemplate(ctx context.Context, serviceID, templateID string, version int) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.services SET template_id = $2, template_version = $3, updated_at = now() WHERE id = $1`, serviceID, templateID, version)
	if err != nil {
		return fmt.Errorf("store: ligando serviço ao modelo: %w", err)
	}
	return nil
}
