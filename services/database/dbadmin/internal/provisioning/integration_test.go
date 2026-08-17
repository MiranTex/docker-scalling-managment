package provisioning

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLifecycleIntegration(t *testing.T) {
	databaseURL := os.Getenv("DBADMIN_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DBADMIN_TEST_DATABASE_URL is not set")
	}
	publicHost := os.Getenv("DBADMIN_TEST_PUBLIC_HOST")
	if publicHost == "" {
		publicHost = "localhost"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	suffix := time.Now().UnixNano() % 1_000_000_000
	schemaName := fmt.Sprintf("test_schema_%d", suffix)
	roleName := fmt.Sprintf("test_role_%d", suffix)
	manager := NewManager(pool, ConnectionConfig{Host: publicHost, Port: 5432, SSLMode: "disable"})
	if err := manager.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = manager.Delete(context.Background(), schemaName)
	}()

	created, err := manager.Create(ctx, schemaName, roleName, "integration-test")
	if err != nil {
		t.Fatal(err)
	}
	userPool, err := pgxpool.New(ctx, created.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := userPool.Ping(ctx); err != nil {
		userPool.Close()
		t.Fatalf("generated DATABASE_URL cannot connect: %v", err)
	}
	var currentSchema string
	if err := userPool.QueryRow(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		userPool.Close()
		t.Fatal(err)
	}
	if currentSchema != schemaName {
		userPool.Close()
		t.Fatalf("current_schema()=%q, want %q", currentSchema, schemaName)
	}
	if _, err := userPool.Exec(ctx, "CREATE TABLE lifecycle_check (id bigint)"); err != nil {
		userPool.Close()
		t.Fatalf("role cannot create in its schema: %v", err)
	}
	userPool.Close()

	reset, err := manager.ResetPassword(ctx, schemaName)
	if err != nil {
		t.Fatal(err)
	}
	oldPool, err := pgxpool.New(ctx, created.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldPool.Ping(ctx); err == nil {
		oldPool.Close()
		t.Fatal("old password still authenticates after reset")
	}
	oldPool.Close()
	newPool, err := pgxpool.New(ctx, reset.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := newPool.Ping(ctx); err != nil {
		newPool.Close()
		t.Fatalf("new password cannot connect: %v", err)
	}
	newPool.Close()

	if err := manager.Delete(ctx, schemaName); err != nil {
		t.Fatal(err)
	}
	var remains bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname = $1)
			OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $2)`, schemaName, roleName).Scan(&remains); err != nil {
		t.Fatal(err)
	}
	if remains {
		t.Fatal("schema or role still exists after delete")
	}
}
