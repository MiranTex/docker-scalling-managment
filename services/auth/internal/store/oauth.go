package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"auth/internal/idgen"
)

// FindUserByOAuthIdentity devolve o utilizador já ligado à identidade
// (provider, providerUserID), se existir -- é o caminho comum de login
// social depois da primeira vez (a ligação já existe).
func (db *DB) FindUserByOAuthIdentity(ctx context.Context, provider, providerUserID string) (User, bool, error) {
	var u User
	err := db.sql.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.created_at
		FROM auth.users u
		JOIN auth.oauth_identities i ON i.user_id = u.id
		WHERE i.provider = $1 AND i.provider_user_id = $2`,
		provider, providerUserID,
	).Scan(&u.ID, &u.Email, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("store: buscando utilizador por identidade OAuth: %w", err)
	}
	return u, true, nil
}

// FindUserByEmail devolve um utilizador só pelo e-mail, sem exigir que
// tenha credencial de senha -- ao contrário de FindUserByEmailWithPassword
// (que faz INNER JOIN com password_credentials e não acharia uma conta
// criada originalmente via OAuth).
func (db *DB) FindUserByEmail(ctx context.Context, email string) (User, bool, error) {
	var u User
	err := db.sql.QueryRowContext(ctx,
		`SELECT id, email, created_at FROM auth.users WHERE email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("store: buscando utilizador por e-mail: %w", err)
	}
	return u, true, nil
}

// CreateUserWithoutPassword cria uma conta nova sem nenhuma credencial de
// senha -- usado quando alguém se regista pela primeira vez via login
// social. A conta pode ganhar uma senha depois (fora do escopo desta fase).
func (db *DB) CreateUserWithoutPassword(ctx context.Context, email string) (User, error) {
	id, err := idgen.New()
	if err != nil {
		return User{}, fmt.Errorf("store: gerando id de utilizador: %w", err)
	}

	var createdAt time.Time
	err = db.sql.QueryRowContext(ctx,
		`INSERT INTO auth.users (id, email) VALUES ($1, $2) RETURNING created_at`,
		id, email,
	).Scan(&createdAt)
	if isUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("store: criando utilizador: %w", err)
	}

	return User{ID: id, Email: email, CreatedAt: createdAt}, nil
}

// LinkOAuthIdentity associa userID à identidade (provider, providerUserID)
// -- chamado tanto ao criar uma conta nova via OAuth quanto ao ligar uma
// conta já existente (mesmo e-mail, verificado pelo provider) a um login
// social novo.
func (db *DB) LinkOAuthIdentity(ctx context.Context, userID, provider, providerUserID, email string) error {
	id, err := idgen.New()
	if err != nil {
		return fmt.Errorf("store: gerando id de identidade OAuth: %w", err)
	}
	_, err = db.sql.ExecContext(ctx, `
		INSERT INTO auth.oauth_identities (id, user_id, provider, provider_user_id, email)
		VALUES ($1, $2, $3, $4, $5)`,
		id, userID, provider, providerUserID, email,
	)
	if err != nil {
		return fmt.Errorf("store: ligando identidade OAuth: %w", err)
	}
	return nil
}
