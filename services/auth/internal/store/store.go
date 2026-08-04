// Package store é a única parte do serviço que fala SQL — persiste
// utilizadores, credenciais de senha e refresh tokens em Postgres (schema
// "auth", isolado de qualquer outro schema que o mesmo servidor Postgres
// venha a hospedar para outros serviços).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" pro database/sql

	"auth/internal/idgen"
	"auth/internal/refresh"
)

// DB encapsula a conexão com o Postgres. Satisfaz refresh.Store (ver
// métodos Create/FindByHash/Revoke/RevokeFamily abaixo) — o pacote refresh
// não sabe nem precisa saber que quem o implementa é Postgres.
type DB struct {
	sql *sql.DB
}

// Open conecta ao Postgres em dsn (ex: "postgres://user:pass@host:5432/db?sslmode=disable")
// e aplica as migrations pendentes antes de devolver.
func Open(ctx context.Context, dsn string) (*DB, error) {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: abrindo conexão: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: conectando ao Postgres: %w", err)
	}
	if err := Migrate(ctx, sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return &DB{sql: sqlDB}, nil
}

// Close fecha a pool de conexões.
func (db *DB) Close() error {
	return db.sql.Close()
}

// Ping confirma que o Postgres está alcançável -- usado pelo endpoint
// /readyz: o processo pode estar de pé (liveness) mas incapaz de servir
// nada de útil se a base de dados caiu.
func (db *DB) Ping(ctx context.Context) error {
	return db.sql.PingContext(ctx)
}

// User é uma conta, tal como persistida.
type User struct {
	ID        string
	Email     string
	CreatedAt time.Time
	// EmailVerifiedAt é nil até o dono provar que tem acesso a este
	// e-mail (ver internal/verification) -- usado por oauth.Manager.Login
	// pra decidir se uma conta encontrada por e-mail pode ser confiada
	// como está, ou se precisa ser "reclamada" (ver ReclaimUnverifiedAccount).
	EmailVerifiedAt *time.Time
	// Role é uma das constantes em role.go -- vai como claim no JWT
	// emitido no login/refresh (ver httpapi.issueTokenPair), é assim que
	// outro serviço decide se esta conta pode entrar numa superfície
	// administrativa.
	Role string
}

// ErrEmailTaken é devolvido por CreateUserWithPassword quando o e-mail já
// está registado.
var ErrEmailTaken = errors.New("store: e-mail já registado")

// ErrUserNotFound é devolvido quando nenhum utilizador bate com o critério
// de busca.
var ErrUserNotFound = errors.New("store: utilizador não encontrado")

// CreateUserWithPassword cria um utilizador novo já com uma credencial de
// senha, atomicamente — nunca fica um user sem credencial nenhuma nem uma
// credencial órfã se algo falhar a meio.
func (db *DB) CreateUserWithPassword(ctx context.Context, email, passwordHash string) (User, error) {
	id, err := idgen.New()
	if err != nil {
		return User{}, fmt.Errorf("store: gerando id de utilizador: %w", err)
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("store: iniciando transação: %w", err)
	}
	defer tx.Rollback()

	var createdAt time.Time
	err = tx.QueryRowContext(ctx,
		`INSERT INTO auth.users (id, email) VALUES ($1, $2) RETURNING created_at`,
		id, email,
	).Scan(&createdAt)
	// Note: email_verified_at fica NULL aqui de propósito -- uma conta
	// criada por senha só fica "verificada" depois de confirmar o e-mail
	// (ver internal/verification e POST /v1/verify-email).
	if isUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("store: criando utilizador: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO auth.password_credentials (user_id, password_hash) VALUES ($1, $2)`,
		id, passwordHash,
	); err != nil {
		return User{}, fmt.Errorf("store: criando credencial de senha: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("store: confirmando criação de utilizador: %w", err)
	}

	// Self-registration nunca aceita role do cliente -- toda conta criada
	// por aqui nasce "user" (o DEFAULT da coluna). RoleUser aqui é só para
	// o valor devolvido bater com o que está mesmo na base.
	return User{ID: id, Email: email, CreatedAt: createdAt, Role: RoleUser}, nil
}

// FindUserByEmailWithPassword devolve o utilizador e o hash da senha
// guardada, usado no login. ErrUserNotFound se não existir conta com esse
// e-mail, ou se existir mas não tiver credencial de senha (ex: só OAuth).
func (db *DB) FindUserByEmailWithPassword(ctx context.Context, email string) (User, string, error) {
	var u User
	var passwordHash string
	var emailVerifiedAt sql.NullTime
	err := db.sql.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.created_at, u.email_verified_at, u.role, p.password_hash
		FROM auth.users u
		JOIN auth.password_credentials p ON p.user_id = u.id
		WHERE u.email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.CreatedAt, &emailVerifiedAt, &u.Role, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrUserNotFound
	}
	if err != nil {
		return User{}, "", fmt.Errorf("store: buscando utilizador por e-mail: %w", err)
	}
	if emailVerifiedAt.Valid {
		u.EmailVerifiedAt = &emailVerifiedAt.Time
	}
	return u, passwordHash, nil
}

// FindUserByID devolve o utilizador pelo ID -- usado em
// POST /v1/token/refresh para reler a role atual (pode ter mudado desde
// que o access token anterior foi emitido) antes de assinar um novo.
func (db *DB) FindUserByID(ctx context.Context, id string) (User, bool, error) {
	var u User
	var emailVerifiedAt sql.NullTime
	err := db.sql.QueryRowContext(ctx,
		`SELECT id, email, created_at, email_verified_at, role FROM auth.users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Email, &u.CreatedAt, &emailVerifiedAt, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("store: buscando utilizador por id: %w", err)
	}
	if emailVerifiedAt.Valid {
		u.EmailVerifiedAt = &emailVerifiedAt.Time
	}
	return u, true, nil
}

// ListUsers devolve todas as contas, mais recentes primeiro -- usado pela
// gestão de utilizadores ("GET /v1/admin/users"). Nunca inclui o hash da
// senha (nem sequer é lido aqui).
func (db *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, email, created_at, email_verified_at, role
		FROM auth.users
		ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listando utilizadores: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var emailVerifiedAt sql.NullTime
		if err := rows.Scan(&u.ID, &u.Email, &u.CreatedAt, &emailVerifiedAt, &u.Role); err != nil {
			return nil, fmt.Errorf("store: lendo utilizador: %w", err)
		}
		if emailVerifiedAt.Valid {
			u.EmailVerifiedAt = &emailVerifiedAt.Time
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterando utilizadores: %w", err)
	}
	return users, nil
}

// CreateUserWithPasswordAndRole cria uma conta administrativamente (ao
// contrário de CreateUserWithPassword, usado no self-registration): aceita
// a role explicitamente, e a conta já nasce com o e-mail marcado como
// verificado -- é quem a está a criar (um super-admin autenticado) que
// está a vouch pelo e-mail, não a própria pessoa.
func (db *DB) CreateUserWithPasswordAndRole(ctx context.Context, email, passwordHash, role string) (User, error) {
	id, err := idgen.New()
	if err != nil {
		return User{}, fmt.Errorf("store: gerando id de utilizador: %w", err)
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("store: iniciando transação: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	var createdAt time.Time
	err = tx.QueryRowContext(ctx,
		`INSERT INTO auth.users (id, email, role, email_verified_at) VALUES ($1, $2, $3, $4) RETURNING created_at`,
		id, email, role, now,
	).Scan(&createdAt)
	if isUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("store: criando utilizador: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO auth.password_credentials (user_id, password_hash) VALUES ($1, $2)`,
		id, passwordHash,
	); err != nil {
		return User{}, fmt.Errorf("store: criando credencial de senha: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("store: confirmando criação de utilizador: %w", err)
	}

	return User{ID: id, Email: email, CreatedAt: createdAt, EmailVerifiedAt: &now, Role: role}, nil
}

// UpdateUserRole troca a role de uma conta. found=false se o ID não
// existir -- quem chama decide se isso é um 404.
func (db *DB) UpdateUserRole(ctx context.Context, id, role string) (found bool, err error) {
	res, err := db.sql.ExecContext(ctx, `UPDATE auth.users SET role = $2 WHERE id = $1`, id, role)
	if err != nil {
		return false, fmt.Errorf("store: atualizando role: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: confirmando atualização de role: %w", err)
	}
	return n > 0, nil
}

// CountUsersByRole é usado só no arranque do processo, para decidir se
// vale a pena tentar promover o super-admin de bootstrap (ver
// cmd/authd) -- nunca chamado a partir de um pedido HTTP.
func (db *DB) CountUsersByRole(ctx context.Context, role string) (int, error) {
	var n int
	if err := db.sql.QueryRowContext(ctx, `SELECT count(*) FROM auth.users WHERE role = $1`, role).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: contando utilizadores por role: %w", err)
	}
	return n, nil
}

// PromoteUserByEmail troca a role de quem tem este e-mail. found=false se
// não existir conta com esse e-mail (ex: a pessoa ainda não se
// registou) -- ver cmd/authd sobre o bootstrap do primeiro super-admin.
func (db *DB) PromoteUserByEmail(ctx context.Context, email, role string) (found bool, err error) {
	res, err := db.sql.ExecContext(ctx, `UPDATE auth.users SET role = $2 WHERE email = $1`, email, role)
	if err != nil {
		return false, fmt.Errorf("store: promovendo utilizador: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: confirmando promoção: %w", err)
	}
	return n > 0, nil
}

// ReclaimUnverifiedAccount é chamado quando um login social prova, via um
// provider que confirma o e-mail, que quem está a autenticar agora É o
// dono legítimo de um e-mail cuja conta aqui NUNCA foi verificada -- ou
// seja, essa conta pode ter sido criada por outra pessoa que só sabia o
// e-mail (registo por senha não exige prova nenhuma). Em vez de confiar
// cegamente e só ligar a identidade a essa conta, tratamos isto como
// reivindicação legítima: marcamos o e-mail como verificado agora,
// removemos qualquer credencial de senha que já existisse (quem a
// registou não é o dono, não devia continuar a conseguir entrar), e
// revogamos as sessões (refresh tokens) já ativas -- fecha a janela de
// account takeover sem duplicar a conta.
//
// Limitação aceite: um access token (JWT) já emitido para a conta antes
// disto continua válido até expirar (15 min por default) -- JWT não é
// revogável antes do tempo, só o refresh token é.
func (db *DB) ReclaimUnverifiedAccount(ctx context.Context, userID string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: iniciando transação: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE auth.users SET email_verified_at = $2 WHERE id = $1`, userID, now); err != nil {
		return fmt.Errorf("store: marcando e-mail como verificado: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth.password_credentials WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("store: removendo credencial de senha: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE auth.refresh_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`,
		userID, now,
	); err != nil {
		return fmt.Errorf("store: revogando sessões ativas: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: confirmando reclamação de conta: %w", err)
	}
	return nil
}

// Create implementa refresh.Store.
func (db *DB) Create(ctx context.Context, t refresh.Token) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO auth.refresh_tokens (id, user_id, family_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		t.ID, t.UserID, t.FamilyID, t.TokenHash, t.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: criando refresh token: %w", err)
	}
	return nil
}

// FindByHash implementa refresh.Store.
func (db *DB) FindByHash(ctx context.Context, tokenHash string) (refresh.Token, bool, error) {
	var t refresh.Token
	var revokedAt sql.NullTime
	err := db.sql.QueryRowContext(ctx, `
		SELECT id, user_id, family_id, token_hash, expires_at, revoked_at
		FROM auth.refresh_tokens
		WHERE token_hash = $1`,
		tokenHash,
	).Scan(&t.ID, &t.UserID, &t.FamilyID, &t.TokenHash, &t.ExpiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return refresh.Token{}, false, nil
	}
	if err != nil {
		return refresh.Token{}, false, fmt.Errorf("store: buscando refresh token: %w", err)
	}
	if revokedAt.Valid {
		t.RevokedAt = &revokedAt.Time
	}
	return t, true, nil
}

// Revoke implementa refresh.Store.
func (db *DB) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE auth.refresh_tokens SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`,
		id, revokedAt,
	)
	if err != nil {
		return fmt.Errorf("store: revogando refresh token: %w", err)
	}
	return nil
}

// RevokeFamily implementa refresh.Store.
func (db *DB) RevokeFamily(ctx context.Context, familyID string, revokedAt time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE auth.refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID, revokedAt,
	)
	if err != nil {
		return fmt.Errorf("store: revogando família de refresh tokens: %w", err)
	}
	return nil
}

// isUniqueViolation reconhece o erro de constraint UNIQUE do Postgres
// (SQLSTATE 23505) sem depender de importar os tipos internos do driver
// pgx — checar o texto é frágil em geral, mas o código de erro do Postgres
// em si (23505) é estável e documentado, é só o texto ao redor que muda
// entre drivers.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	type sqlStater interface{ SQLState() string }
	var pgErr sqlStater
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
