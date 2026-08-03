package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"auth/internal/apikey"
)

// APIKeyStore devolve um adaptador que satisfaz apikey.Store. Precisa
// existir porque *DB já implementa refresh.Store com métodos chamados
// Create/FindByHash/Revoke (assinaturas diferentes, mas Go não permite
// dois métodos com o mesmo nome no mesmo tipo) -- por isso os métodos
// abaixo têm nomes próprios (CreateAPIKey, etc), e este adaptador é quem
// apresenta os nomes que apikey.Store espera.
func (db *DB) APIKeyStore() apikey.Store {
	return apiKeyStore{db: db}
}

type apiKeyStore struct{ db *DB }

func (s apiKeyStore) Create(ctx context.Context, k apikey.Key) error {
	return s.db.CreateAPIKey(ctx, k)
}

func (s apiKeyStore) FindByHash(ctx context.Context, keyHash string) (apikey.Key, bool, error) {
	return s.db.FindAPIKeyByHash(ctx, keyHash)
}

func (s apiKeyStore) Revoke(ctx context.Context, id, owner string, revokedAt time.Time) (bool, error) {
	return s.db.RevokeAPIKey(ctx, id, owner, revokedAt)
}

func (s apiKeyStore) ListByOwner(ctx context.Context, owner string) ([]apikey.Key, error) {
	return s.db.ListAPIKeysByOwner(ctx, owner)
}

// CreateAPIKey persiste uma API key nova.
func (db *DB) CreateAPIKey(ctx context.Context, k apikey.Key) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO auth.api_keys (id, owner, key_hash, scopes, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		k.ID, k.Owner, k.KeyHash, joinScopes(k.Scopes), k.CreatedAt, k.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: criando API key: %w", err)
	}
	return nil
}

// FindAPIKeyByHash implementa apikey.Store.
func (db *DB) FindAPIKeyByHash(ctx context.Context, keyHash string) (apikey.Key, bool, error) {
	var k apikey.Key
	var scopes string
	var expiresAt, revokedAt sql.NullTime
	err := db.sql.QueryRowContext(ctx, `
		SELECT id, owner, key_hash, scopes, created_at, expires_at, revoked_at
		FROM auth.api_keys
		WHERE key_hash = $1`,
		keyHash,
	).Scan(&k.ID, &k.Owner, &k.KeyHash, &scopes, &k.CreatedAt, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return apikey.Key{}, false, nil
	}
	if err != nil {
		return apikey.Key{}, false, fmt.Errorf("store: buscando API key: %w", err)
	}
	k.Scopes = splitScopes(scopes)
	if expiresAt.Valid {
		k.ExpiresAt = &expiresAt.Time
	}
	if revokedAt.Valid {
		k.RevokedAt = &revokedAt.Time
	}
	return k, true, nil
}

// RevokeAPIKey implementa apikey.Store.
func (db *DB) RevokeAPIKey(ctx context.Context, id, owner string, revokedAt time.Time) (bool, error) {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE auth.api_keys SET revoked_at = $3 WHERE id = $1 AND owner = $2 AND revoked_at IS NULL`,
		id, owner, revokedAt,
	)
	if err != nil {
		return false, fmt.Errorf("store: revogando API key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: confirmando revogação de API key: %w", err)
	}
	return n > 0, nil
}

// ListAPIKeysByOwner implementa apikey.Store — devolve todas as chaves de
// owner, mais recentes primeiro.
func (db *DB) ListAPIKeysByOwner(ctx context.Context, owner string) ([]apikey.Key, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, owner, key_hash, scopes, created_at, expires_at, revoked_at
		FROM auth.api_keys
		WHERE owner = $1
		ORDER BY created_at DESC`,
		owner,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listando API keys: %w", err)
	}
	defer rows.Close()

	var keys []apikey.Key
	for rows.Next() {
		var k apikey.Key
		var scopes string
		var expiresAt, revokedAt sql.NullTime
		if err := rows.Scan(&k.ID, &k.Owner, &k.KeyHash, &scopes, &k.CreatedAt, &expiresAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("store: lendo API key: %w", err)
		}
		k.Scopes = splitScopes(scopes)
		if expiresAt.Valid {
			k.ExpiresAt = &expiresAt.Time
		}
		if revokedAt.Valid {
			k.RevokedAt = &revokedAt.Time
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterando API keys: %w", err)
	}
	return keys, nil
}

func joinScopes(scopes []string) string {
	return strings.Join(scopes, ",")
}

func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
