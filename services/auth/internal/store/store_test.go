// Testes de integração deste pacote correm contra um Postgres de
// verdade -- é a única camada do serviço que fala SQL, então é a única
// onde faz sentido testar contra o motor real em vez de um fake (um fake
// de SQL não pegaria erros de query, tipos de coluna errados, ou a
// unique constraint de e-mail).
//
// Gated por AUTH_TEST_DATABASE_URL: se não estiver definida, os testes
// deste ficheiro são pulados (t.Skip), então "go test ./..." continua
// funcionando sem exigir Postgres disponível. Ver README para como subir
// um Postgres local pra rodar isto.
package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"auth/internal/idgen"
	"auth/internal/refresh"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("AUTH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUTH_TEST_DATABASE_URL não definida, pulando teste de integração com Postgres")
	}

	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Isola cada teste dos demais (e de execuções anteriores) sem exigir
	// um Postgres descartável por teste -- mais simples que subir/derrubar
	// containers a cada execução.
	if _, err := db.sql.ExecContext(ctx, `TRUNCATE auth.refresh_tokens, auth.password_credentials, auth.api_keys, auth.users CASCADE`); err != nil {
		t.Fatalf("limpando tabelas antes do teste: %v", err)
	}
	return db
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	id, err := idgen.New()
	if err != nil {
		t.Fatalf("idgen.New: %v", err)
	}
	return fmt.Sprintf("%s@example.test", id)
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	// openTestDB já chamou Open (que já migra); migrar de novo não deve
	// dar erro nem duplicar nada.
	if err := Migrate(context.Background(), db.sql); err != nil {
		t.Fatalf("segunda chamada a Migrate: %v", err)
	}
}

func TestCreateAndFindUserByEmail(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	created, err := db.CreateUserWithPassword(ctx, email, "hash-fake")
	if err != nil {
		t.Fatalf("CreateUserWithPassword: %v", err)
	}
	if created.ID == "" || created.Email != email {
		t.Fatalf("utilizador criado com campos inesperados: %+v", created)
	}

	found, hash, err := db.FindUserByEmailWithPassword(ctx, email)
	if err != nil {
		t.Fatalf("FindUserByEmailWithPassword: %v", err)
	}
	if found.ID != created.ID {
		t.Fatalf("ID = %q, want %q", found.ID, created.ID)
	}
	if hash != "hash-fake" {
		t.Fatalf("hash = %q, want hash-fake", hash)
	}
}

func TestCreateUserWithDuplicateEmail(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if _, err := db.CreateUserWithPassword(ctx, email, "hash-1"); err != nil {
		t.Fatalf("primeira criação: %v", err)
	}
	if _, err := db.CreateUserWithPassword(ctx, email, "hash-2"); err != ErrEmailTaken {
		t.Fatalf("esperava ErrEmailTaken, got %v", err)
	}
}

func TestFindUserByEmailNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, _, err := db.FindUserByEmailWithPassword(context.Background(), "ninguem@example.test"); err != ErrUserNotFound {
		t.Fatalf("esperava ErrUserNotFound, got %v", err)
	}
}

// TestRefreshTokenLifecycle exercita o DB como implementação real de
// refresh.Store, incluindo o cenário de deteção de reuso -- prova que a
// lógica já validada com o fake em internal/refresh também funciona com
// Postgres de verdade (constraints, tipos, tudo).
func TestRefreshTokenLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	user, err := db.CreateUserWithPassword(ctx, email, "hash-fake")
	if err != nil {
		t.Fatalf("CreateUserWithPassword: %v", err)
	}

	manager := refresh.NewManager(db, time.Hour)

	first, err := manager.Issue(ctx, user.ID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	second, userID, err := manager.Rotate(ctx, first)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if userID != user.ID {
		t.Fatalf("userID = %q, want %q", userID, user.ID)
	}

	// Reapresentar o token já rodado tem que ser tratado como reuso e
	// revogar a família inteira -- inclusive `second`, que é legítimo.
	if _, _, err := manager.Rotate(ctx, first); err != refresh.ErrReuseDetected {
		t.Fatalf("esperava ErrReuseDetected, got %v", err)
	}
	if _, _, err := manager.Rotate(ctx, second); err == nil {
		t.Fatal("token legítimo devia ter sido revogado junto pela deteção de reuso")
	}
}
