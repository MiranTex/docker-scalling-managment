package provisioning

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidateName(t *testing.T) {
	for _, name := range []string{"app_one", "tenant123", "abc"} {
		if err := validateName(name); err != nil {
			t.Errorf("validateName(%q) returned %v", name, err)
		}
	}
	for _, name := range []string{"ab", "App", "1app", "app-name", "public", "dbadmin", "pg_catalog"} {
		if err := validateName(name); err == nil {
			t.Errorf("validateName(%q) accepted an invalid or reserved name", name)
		}
	}
}

func TestCredentialsEscapesConnectionURL(t *testing.T) {
	manager := NewManager(nil, ConnectionConfig{Host: "db.internal", Port: 5432, SSLMode: "require"})
	schema := Schema{SchemaName: "orders", RoleName: "orders_user", CreatedAt: time.Now(), CreatedBy: "admin"}
	credentials := manager.credentials(schema, "app db", "p@ss:/?#")

	parsed, err := url.Parse(credentials.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() != schema.RoleName || password != "p@ss:/?#" {
		t.Fatalf("credentials were not preserved in URL: %s", credentials.DatabaseURL)
	}
	if parsed.Path != "/app db" || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("database path or sslmode is incorrect: %s", credentials.DatabaseURL)
	}
	for _, expected := range []string{"PGHOST=db.internal", "PGUSER=orders_user", "PGPASSWORD=p@ss:/?#", "PGSSLMODE=require"} {
		if !strings.Contains(credentials.PGEnv, expected) {
			t.Errorf("PG environment is missing %q", expected)
		}
	}
}

func TestGeneratedPasswordsAreLongAndUnique(t *testing.T) {
	first, err := generatePassword()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generatePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 32 || first == second {
		t.Fatalf("passwords are too short or repeated")
	}
}
