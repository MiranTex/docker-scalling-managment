package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// TOTP é o segundo fator de uma conta.
type TOTP struct {
	SecretEnc string
	Confirmed bool
	LastStep  int64
}

// GetTOTP devolve o TOTP da conta (found=false se não tem).
func (db *DB) GetTOTP(ctx context.Context, userID string) (TOTP, bool, error) {
	var t TOTP
	var confirmed sql.NullTime
	err := db.sql.QueryRowContext(ctx,
		`SELECT secret_enc, confirmed_at, last_step FROM auth.mfa_totp WHERE user_id = $1`, userID,
	).Scan(&t.SecretEnc, &confirmed, &t.LastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return TOTP{}, false, nil
	}
	if err != nil {
		return TOTP{}, false, fmt.Errorf("store: lendo 2FA: %w", err)
	}
	t.Confirmed = confirmed.Valid
	return t, true, nil
}

// StartTOTP guarda um segredo novo, por confirmar. Não mexe num 2FA já
// ativo (desligar é explícito).
func (db *DB) StartTOTP(ctx context.Context, userID, secretEnc string) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO auth.mfa_totp (user_id, secret_enc) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET secret_enc = EXCLUDED.secret_enc, last_step = 0, created_at = now()
		WHERE auth.mfa_totp.confirmed_at IS NULL`, userID, secretEnc)
	if err != nil {
		return fmt.Errorf("store: começando 2FA: %w", err)
	}
	return nil
}

// ConfirmTOTP ativa o 2FA (o primeiro código bateu) e põe os códigos de
// recuperação, numa transação.
func (db *DB) ConfirmTOTP(ctx context.Context, userID string, usedStep int64, recoveryHashes []string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE auth.mfa_totp SET confirmed_at = now(), last_step = $2 WHERE user_id = $1`, userID, usedStep); err != nil {
		return fmt.Errorf("store: ativando 2FA: %w", err)
	}
	if err := replaceRecovery(ctx, tx, userID, recoveryHashes); err != nil {
		return err
	}
	return tx.Commit()
}

// UseTOTPStep marca um passo como usado, se for posterior ao último (um
// código não serve duas vezes, mesmo com dois pedidos ao mesmo tempo).
func (db *DB) UseTOTPStep(ctx context.Context, userID string, s int64) (bool, error) {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE auth.mfa_totp SET last_step = $2 WHERE user_id = $1 AND last_step < $2`, userID, s)
	if err != nil {
		return false, fmt.Errorf("store: usando código 2FA: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// UseRecoveryCode gasta um código de recuperação (false se não existe ou
// já foi usado).
func (db *DB) UseRecoveryCode(ctx context.Context, userID, codeHash string) (bool, error) {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE auth.mfa_recovery_codes SET used_at = now() WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		userID, codeHash)
	if err != nil {
		return false, fmt.Errorf("store: usando código de recuperação: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReplaceRecoveryCodes troca os códigos de recuperação de uma conta.
func (db *DB) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes []string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceRecovery(ctx, tx, userID, hashes); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceRecovery(ctx context.Context, tx *sql.Tx, userID string, hashes []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth.mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("store: apagando códigos de recuperação: %w", err)
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO auth.mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
			return fmt.Errorf("store: gravando código de recuperação: %w", err)
		}
	}
	return nil
}

// RemainingRecoveryCodes: quantos códigos de recuperação ainda não foram
// usados.
func (db *DB) RemainingRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx,
		`SELECT count(*) FROM auth.mfa_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

// DisableTOTP desliga o 2FA (e apaga os códigos de recuperação).
func (db *DB) DisableTOTP(ctx context.Context, userID string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth.mfa_totp WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("store: desligando 2FA: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth.mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("store: desligando 2FA: %w", err)
	}
	return tx.Commit()
}

// MFAEnabledUsers: as contas com 2FA ativo (para a lista de utilizadores).
func (db *DB) MFAEnabledUsers(ctx context.Context) (map[string]bool, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT user_id FROM auth.mfa_totp WHERE confirmed_at IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: listando 2FA: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
