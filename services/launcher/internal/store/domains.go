package store

import (
	"context"
	"fmt"
	"time"
)

const (
	DomainPending = "pending"
	DomainActive  = "active"
	DomainFailed  = "failed"
)

// Domain é de um serviço (ServiceID) ou de um bucket de um addon storage
// (AddonID + Bucket).
type Domain struct {
	Hostname   string     `json:"hostname"`
	ServiceID  string     `json:"serviceId,omitempty"`
	AddonID    string     `json:"addonId,omitempty"`
	Bucket     string     `json:"bucket,omitempty"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	CreatedBy  string     `json:"createdBy"`
}

const domainColumns = `hostname, COALESCE(service_id, ''), COALESCE(addon_id, ''), COALESCE(bucket, ''), status, error, checked_at, verified_at, created_at, created_by`

func (db *DB) queryDomains(ctx context.Context, query string, args ...any) ([]Domain, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listando domínios: %w", err)
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.Hostname, &d.ServiceID, &d.AddonID, &d.Bucket, &d.Status, &d.Error, &d.CheckedAt, &d.VerifiedAt, &d.CreatedAt, &d.CreatedBy); err != nil {
			return nil, fmt.Errorf("store: lendo domínio: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (db *DB) ListDomains(ctx context.Context, serviceID string) ([]Domain, error) {
	return db.queryDomains(ctx, `SELECT `+domainColumns+` FROM launcher.domains WHERE service_id = $1 ORDER BY hostname`, serviceID)
}

func (db *DB) ListActiveDomains(ctx context.Context, serviceID string) ([]Domain, error) {
	return db.queryDomains(ctx, `SELECT `+domainColumns+` FROM launcher.domains WHERE service_id = $1 AND status = 'active' ORDER BY hostname`, serviceID)
}

// ListUnverifiedDomains são os que o reconciliador volta a verificar.
func (db *DB) ListUnverifiedDomains(ctx context.Context) ([]Domain, error) {
	return db.queryDomains(ctx, `SELECT `+domainColumns+` FROM launcher.domains WHERE status <> 'active' ORDER BY created_at`)
}

func (db *DB) CreateDomain(ctx context.Context, d Domain) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.domains (hostname, service_id, created_by) VALUES ($1, $2, $3)`, d.Hostname, d.ServiceID, d.CreatedBy)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: gravando domínio %q: %w", d.Hostname, err)
	}
	return nil
}

func (db *DB) SetDomainStatus(ctx context.Context, hostname, status, errMsg string) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.domains SET status = $2, error = $3, checked_at = now(),
			verified_at = CASE WHEN $2 = 'active' THEN now() ELSE verified_at END
		WHERE hostname = $1`, hostname, status, errMsg)
	if err != nil {
		return fmt.Errorf("store: atualizando domínio %q: %w", hostname, err)
	}
	return nil
}

func (db *DB) DeleteDomain(ctx context.Context, serviceID, hostname string) (bool, error) {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.domains WHERE service_id = $1 AND hostname = $2`, serviceID, hostname)
	if err != nil {
		return false, fmt.Errorf("store: apagando domínio %q: %w", hostname, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (db *DB) ListBucketDomains(ctx context.Context, addonID, bucket string) ([]Domain, error) {
	return db.queryDomains(ctx, `SELECT `+domainColumns+` FROM launcher.domains WHERE addon_id = $1 AND bucket = $2 ORDER BY hostname`, addonID, bucket)
}

// ListActiveAddonDomains: os domínios ativos de todos os buckets de um addon.
func (db *DB) ListActiveAddonDomains(ctx context.Context, addonID string) ([]Domain, error) {
	return db.queryDomains(ctx, `SELECT `+domainColumns+` FROM launcher.domains WHERE addon_id = $1 AND status = 'active' ORDER BY hostname`, addonID)
}

func (db *DB) CreateBucketDomain(ctx context.Context, d Domain) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.domains (hostname, addon_id, bucket, created_by) VALUES ($1, $2, $3, $4)`, d.Hostname, d.AddonID, d.Bucket, d.CreatedBy)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: gravando domínio %q: %w", d.Hostname, err)
	}
	return nil
}

func (db *DB) DeleteBucketDomain(ctx context.Context, addonID, bucket, hostname string) (bool, error) {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.domains WHERE addon_id = $1 AND bucket = $2 AND hostname = $3`, addonID, bucket, hostname)
	if err != nil {
		return false, fmt.Errorf("store: apagando domínio %q: %w", hostname, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
