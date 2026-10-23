// Package storetest dá a cada teste uma base de dados Postgres nova e já
// migrada. Precisa de LAUNCHER_TEST_DATABASE_URL (um Postgres onde o
// utilizador possa criar bases); sem ela os testes que dependem disto são
// saltados, não falham.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"launcher/internal/store"
)

func New(t *testing.T) *store.DB {
	t.Helper()
	base := os.Getenv("LAUNCHER_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("LAUNCHER_TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })

	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	name := "launcher_test_" + hex.EncodeToString(buf)
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("criando base de teste: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE " + name + " WITH (FORCE)") })

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := store.Open(context.Background(), u.String())
	if err != nil {
		t.Fatalf("abrindo base de teste: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
