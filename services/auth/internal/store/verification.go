package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"auth/internal/verification"
)

// EmailVerificationStore devolve um adaptador que satisfaz
// verification.Store -- mesma razão dos adaptadores de refresh/apikey:
// *DB não pode ter dois métodos chamados "Create"/"FindByHash" com
// assinaturas diferentes.
func (db *DB) EmailVerificationStore() verification.Store {
	return emailVerificationStore{db: db}
}

type emailVerificationStore struct{ db *DB }

func (s emailVerificationStore) Create(ctx context.Context, v verification.Verification) error {
	return s.db.CreateEmailVerification(ctx, v)
}

func (s emailVerificationStore) FindByHash(ctx context.Context, tokenHash string) (verification.Verification, bool, error) {
	return s.db.FindEmailVerificationByHash(ctx, tokenHash)
}

func (s emailVerificationStore) Consume(ctx context.Context, id string, consumedAt time.Time) error {
	return s.db.ConsumeEmailVerification(ctx, id, consumedAt)
}

func (s emailVerificationStore) MarkUserVerified(ctx context.Context, userID string, verifiedAt time.Time) error {
	return s.db.MarkEmailVerified(ctx, userID, verifiedAt)
}

// CreateEmailVerification persiste um token de verificação novo.
func (db *DB) CreateEmailVerification(ctx context.Context, v verification.Verification) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO auth.email_verifications (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`,
		v.ID, v.UserID, v.TokenHash, v.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: criando token de verificação: %w", err)
	}
	return nil
}

// FindEmailVerificationByHash busca um token de verificação pelo hash.
func (db *DB) FindEmailVerificationByHash(ctx context.Context, tokenHash string) (verification.Verification, bool, error) {
	var v verification.Verification
	var consumedAt sql.NullTime
	err := db.sql.QueryRowContext(ctx, `
		SELECT id, user_id, token_hash, expires_at, consumed_at
		FROM auth.email_verifications
		WHERE token_hash = $1`,
		tokenHash,
	).Scan(&v.ID, &v.UserID, &v.TokenHash, &v.ExpiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return verification.Verification{}, false, nil
	}
	if err != nil {
		return verification.Verification{}, false, fmt.Errorf("store: buscando token de verificação: %w", err)
	}
	if consumedAt.Valid {
		v.ConsumedAt = &consumedAt.Time
	}
	return v, true, nil
}

// ConsumeEmailVerification marca um token de verificação como usado.
func (db *DB) ConsumeEmailVerification(ctx context.Context, id string, consumedAt time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE auth.email_verifications SET consumed_at = $2 WHERE id = $1 AND consumed_at IS NULL`,
		id, consumedAt,
	)
	if err != nil {
		return fmt.Errorf("store: consumindo token de verificação: %w", err)
	}
	return nil
}

// MarkEmailVerified marca o e-mail de um utilizador como verificado.
func (db *DB) MarkEmailVerified(ctx context.Context, userID string, verifiedAt time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE auth.users SET email_verified_at = $2 WHERE id = $1`,
		userID, verifiedAt,
	)
	if err != nil {
		return fmt.Errorf("store: marcando e-mail como verificado: %w", err)
	}
	return nil
}
