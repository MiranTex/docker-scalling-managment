package store

import (
	"context"
	"testing"
	"time"

	"auth/internal/idgen"
	"auth/internal/refresh"
)

func mustNewID(t *testing.T) string {
	t.Helper()
	id, err := idgen.New()
	if err != nil {
		t.Fatalf("idgen.New: %v", err)
	}
	return id
}

// Este ficheiro testa que *DB implementa corretamente os métodos que
// oauth.Store exige (FindUserByOAuthIdentity, FindUserByEmail,
// CreateUserWithoutPassword, LinkOAuthIdentity) contra Postgres de
// verdade. Não importa o pacote oauth diretamente -- isso causaria um
// ciclo de import (oauth já importa store para o tipo store.User); o
// fluxo OAuth2 completo (via um IdP falso) já é coberto pelos testes de
// internal/oauth, com um fake de Store em memória.
func TestOAuthAccountLinkingLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	created, err := db.CreateUserWithoutPassword(ctx, uniqueEmail(t), false)
	if err != nil {
		t.Fatalf("CreateUserWithoutPassword: %v", err)
	}

	if err := db.LinkOAuthIdentity(ctx, created.ID, "google", "ext-123", created.Email); err != nil {
		t.Fatalf("LinkOAuthIdentity: %v", err)
	}

	found, ok, err := db.FindUserByOAuthIdentity(ctx, "google", "ext-123")
	if err != nil {
		t.Fatalf("FindUserByOAuthIdentity: %v", err)
	}
	if !ok || found.ID != created.ID {
		t.Fatalf("esperava encontrar o utilizador %q, got found=%v user=%+v", created.ID, ok, found)
	}

	byEmail, ok, err := db.FindUserByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindUserByEmail: %v", err)
	}
	if !ok || byEmail.ID != created.ID {
		t.Fatalf("FindUserByEmail não achou o utilizador esperado: found=%v user=%+v", ok, byEmail)
	}
}

func TestFindUserByOAuthIdentityNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, ok, err := db.FindUserByOAuthIdentity(context.Background(), "google", "nao-existe"); err != nil || ok {
		t.Fatalf("esperava not-found, got ok=%v err=%v", ok, err)
	}
}

func TestCreateUserWithoutPasswordThenFindByEmailWithPasswordFails(t *testing.T) {
	// FindUserByEmailWithPassword faz INNER JOIN com password_credentials
	// -- uma conta criada só via OAuth (sem senha) não deve aparecer ali,
	// senão login por senha "funcionaria" com uma senha vazia/hash
	// inexistente pra uma conta social-only.
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if _, err := db.CreateUserWithoutPassword(ctx, email, false); err != nil {
		t.Fatalf("CreateUserWithoutPassword: %v", err)
	}

	if _, _, err := db.FindUserByEmailWithPassword(ctx, email); err != ErrUserNotFound {
		t.Fatalf("esperava ErrUserNotFound para conta sem senha, got %v", err)
	}
}

func TestCreateUserWithoutPasswordSetsVerifiedFlag(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	unverified, err := db.CreateUserWithoutPassword(ctx, uniqueEmail(t), false)
	if err != nil {
		t.Fatalf("CreateUserWithoutPassword (unverified): %v", err)
	}
	if unverified.EmailVerifiedAt != nil {
		t.Fatalf("esperava EmailVerifiedAt nil, got %v", unverified.EmailVerifiedAt)
	}

	verified, err := db.CreateUserWithoutPassword(ctx, uniqueEmail(t), true)
	if err != nil {
		t.Fatalf("CreateUserWithoutPassword (verified): %v", err)
	}
	if verified.EmailVerifiedAt == nil {
		t.Fatal("esperava EmailVerifiedAt preenchido para conta criada já verificada")
	}
}

// TestReclaimUnverifiedAccount é o teste mais importante deste ficheiro:
// confirma que reclamar uma conta squatted de verdade derruba o acesso do
// atacante (credencial de senha removida, sessões revogadas), não só
// marca um campo.
func TestReclaimUnverifiedAccount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	// O "atacante" regista a conta por senha -- nunca verificada.
	user, err := db.CreateUserWithPassword(ctx, email, "hash-do-atacante")
	if err != nil {
		t.Fatalf("CreateUserWithPassword: %v", err)
	}
	if user.EmailVerifiedAt != nil {
		t.Fatal("conta registada por senha não devia nascer verificada")
	}

	// E tem uma sessão ativa (refresh token).
	tokenHash := "hash-do-refresh-de-teste-" + user.ID
	if err := db.Create(ctx, refresh.Token{
		ID:        mustNewID(t),
		UserID:    user.ID,
		FamilyID:  mustNewID(t),
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("criando refresh token do atacante: %v", err)
	}

	if err := db.ReclaimUnverifiedAccount(ctx, user.ID); err != nil {
		t.Fatalf("ReclaimUnverifiedAccount: %v", err)
	}

	// 1. E-mail passa a verificado.
	reclaimed, _, err := db.FindUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("FindUserByEmail: %v", err)
	}
	if reclaimed.EmailVerifiedAt == nil {
		t.Fatal("esperava EmailVerifiedAt preenchido após reclamar a conta")
	}

	// 2. A senha do atacante deixou de funcionar.
	if _, _, err := db.FindUserByEmailWithPassword(ctx, email); err != ErrUserNotFound {
		t.Fatalf("credencial de senha devia ter sido removida, got err=%v", err)
	}

	// 3. Sessões (refresh tokens) já ativas foram revogadas.
	found, ok, err := db.FindByHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if !ok {
		t.Fatal("refresh token de setup não encontrado")
	}
	if found.RevokedAt == nil {
		t.Fatal("refresh token ativo devia ter sido revogado ao reclamar a conta")
	}
}
