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

func TestCreateUserWithPasswordDefaultsToRoleUser(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	created, err := db.CreateUserWithPassword(ctx, email, "hash-fake")
	if err != nil {
		t.Fatalf("CreateUserWithPassword: %v", err)
	}
	if created.Role != RoleUser {
		t.Fatalf("role = %q, want %q", created.Role, RoleUser)
	}

	found, ok, err := db.FindUserByID(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("FindUserByID: found=%v err=%v", ok, err)
	}
	if found.Role != RoleUser {
		t.Fatalf("role lida de volta = %q, want %q", found.Role, RoleUser)
	}
}

func TestFindUserByIDNotFound(t *testing.T) {
	db := openTestDB(t)
	_, ok, err := db.FindUserByID(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("FindUserByID: %v", err)
	}
	if ok {
		t.Fatal("esperava ok=false para ID inexistente")
	}
}

func TestCreateUserWithPasswordAndRoleNasceVerificado(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	created, err := db.CreateUserWithPasswordAndRole(ctx, email, "hash-fake", RoleInfraAdmin)
	if err != nil {
		t.Fatalf("CreateUserWithPasswordAndRole: %v", err)
	}
	if created.Role != RoleInfraAdmin {
		t.Fatalf("role = %q, want %q", created.Role, RoleInfraAdmin)
	}
	if created.EmailVerifiedAt == nil {
		t.Fatal("conta criada administrativamente devia nascer já verificada")
	}
}

func TestUpdateUserRole(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	created, err := db.CreateUserWithPassword(ctx, email, "hash-fake")
	if err != nil {
		t.Fatalf("CreateUserWithPassword: %v", err)
	}

	found, err := db.UpdateUserRole(ctx, created.ID, RoleAdmin)
	if err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	if !found {
		t.Fatal("esperava found=true")
	}

	reread, ok, err := db.FindUserByID(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("FindUserByID: found=%v err=%v", ok, err)
	}
	if reread.Role != RoleAdmin {
		t.Fatalf("role = %q, want %q", reread.Role, RoleAdmin)
	}
}

func TestUpdateUserRoleUnknownID(t *testing.T) {
	db := openTestDB(t)
	found, err := db.UpdateUserRole(context.Background(), "00000000-0000-0000-0000-000000000000", RoleAdmin)
	if err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	if found {
		t.Fatal("esperava found=false para ID inexistente")
	}
}

func TestListUsers(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	emailA, emailB := uniqueEmail(t), uniqueEmail(t)
	if _, err := db.CreateUserWithPassword(ctx, emailA, "hash-a"); err != nil {
		t.Fatalf("CreateUserWithPassword A: %v", err)
	}
	if _, err := db.CreateUserWithPasswordAndRole(ctx, emailB, "hash-b", RoleSuperAdmin); err != nil {
		t.Fatalf("CreateUserWithPasswordAndRole B: %v", err)
	}

	users, err := db.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("esperava 2 utilizadores, veio %d", len(users))
	}
}

func TestCountUsersByRoleAndPromoteUserByEmail(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	if _, err := db.CreateUserWithPassword(ctx, email, "hash-fake"); err != nil {
		t.Fatalf("CreateUserWithPassword: %v", err)
	}

	n, err := db.CountUsersByRole(ctx, RoleSuperAdmin)
	if err != nil {
		t.Fatalf("CountUsersByRole: %v", err)
	}
	if n != 0 {
		t.Fatalf("esperava 0 super-admins antes da promoção, veio %d", n)
	}

	found, err := db.PromoteUserByEmail(ctx, email, RoleSuperAdmin)
	if err != nil {
		t.Fatalf("PromoteUserByEmail: %v", err)
	}
	if !found {
		t.Fatal("esperava found=true")
	}

	n, err = db.CountUsersByRole(ctx, RoleSuperAdmin)
	if err != nil {
		t.Fatalf("CountUsersByRole: %v", err)
	}
	if n != 1 {
		t.Fatalf("esperava 1 super-admin depois da promoção, veio %d", n)
	}
}

func TestPromoteUserByEmailUnknownEmail(t *testing.T) {
	db := openTestDB(t)
	found, err := db.PromoteUserByEmail(context.Background(), "ninguem@example.test", RoleSuperAdmin)
	if err != nil {
		t.Fatalf("PromoteUserByEmail: %v", err)
	}
	if found {
		t.Fatal("esperava found=false para e-mail inexistente")
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
