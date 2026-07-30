package store

import (
	"context"
	"testing"
)

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

	created, err := db.CreateUserWithoutPassword(ctx, uniqueEmail(t))
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

	if _, err := db.CreateUserWithoutPassword(ctx, email); err != nil {
		t.Fatalf("CreateUserWithoutPassword: %v", err)
	}

	if _, _, err := db.FindUserByEmailWithPassword(ctx, email); err != ErrUserNotFound {
		t.Fatalf("esperava ErrUserNotFound para conta sem senha, got %v", err)
	}
}
