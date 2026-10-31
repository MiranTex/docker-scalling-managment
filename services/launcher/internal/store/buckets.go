package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrBucketNotFound = errors.New("store: bucket não encontrado")

// StorageBucket é um bucket de um addon storage (ver
// platform/storagepublic.go).
type StorageBucket struct {
	AddonID string `json:"-"`
	Name    string `json:"name"`
	// Public: leitura anónima dos objetos (CDN).
	Public bool `json:"public"`
	// CORSOrigins: de onde um browser pode usar o bucket.
	CORSOrigins []string  `json:"corsOrigins"`
	CreatedAt   time.Time `json:"createdAt"`
	CreatedBy   string    `json:"createdBy"`
}

func (db *DB) ListBuckets(ctx context.Context, addonID string) ([]StorageBucket, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT addon_id, name, public, cors_origins, created_at, created_by
		FROM launcher.storage_buckets WHERE addon_id = $1 ORDER BY name`, addonID)
	if err != nil {
		return nil, fmt.Errorf("store: listando buckets: %w", err)
	}
	defer rows.Close()
	out := []StorageBucket{}
	for rows.Next() {
		var b StorageBucket
		var cors []byte
		if err := rows.Scan(&b.AddonID, &b.Name, &b.Public, &cors, &b.CreatedAt, &b.CreatedBy); err != nil {
			return nil, fmt.Errorf("store: lendo bucket: %w", err)
		}
		if err := json.Unmarshal(cors, &b.CORSOrigins); err != nil {
			return nil, fmt.Errorf("store: cors do bucket %s: %w", b.Name, err)
		}
		if b.CORSOrigins == nil {
			b.CORSOrigins = []string{}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (db *DB) GetBucket(ctx context.Context, addonID, name string) (StorageBucket, error) {
	buckets, err := db.ListBuckets(ctx, addonID)
	if err != nil {
		return StorageBucket{}, err
	}
	for _, b := range buckets {
		if b.Name == name {
			return b, nil
		}
	}
	return StorageBucket{}, ErrBucketNotFound
}

func (db *DB) CreateBucket(ctx context.Context, b StorageBucket) error {
	cors, _ := json.Marshal(nonNil(b.CORSOrigins))
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO launcher.storage_buckets (addon_id, name, public, cors_origins, created_by) VALUES ($1, $2, $3, $4, $5)`,
		b.AddonID, b.Name, b.Public, cors, b.CreatedBy)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: gravando bucket %q: %w", b.Name, err)
	}
	return nil
}

func (db *DB) UpdateBucket(ctx context.Context, b StorageBucket) error {
	cors, _ := json.Marshal(nonNil(b.CORSOrigins))
	res, err := db.sql.ExecContext(ctx, `
		UPDATE launcher.storage_buckets SET public = $3, cors_origins = $4 WHERE addon_id = $1 AND name = $2`,
		b.AddonID, b.Name, b.Public, cors)
	if err != nil {
		return fmt.Errorf("store: atualizando bucket %q: %w", b.Name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBucketNotFound
	}
	return nil
}

// DeleteBucket apaga o bucket e (em cascata) os domínios dele.
func (db *DB) DeleteBucket(ctx context.Context, addonID, name string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM launcher.storage_buckets WHERE addon_id = $1 AND name = $2`, addonID, name)
	if err != nil {
		return fmt.Errorf("store: apagando bucket %q: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBucketNotFound
	}
	return nil
}

func (db *DB) SetAddonPublicEndpoint(ctx context.Context, addonID string, on bool) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE launcher.addons SET public_endpoint = $2 WHERE id = $1`, addonID, on)
	if err != nil {
		return fmt.Errorf("store: endereço público do addon: %w", err)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
