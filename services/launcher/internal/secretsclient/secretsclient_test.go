package secretsclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
)

func TestExtractNamesDedupsAndFindsAllReferences(t *testing.T) {
	env := []string{
		"DATABASE_URL=postgres://auth:${secret:auth-db-password}@database:5432/app",
		"OTHER=plain-value",
		"API_KEY=${secret:api-key}",
		"REPEATED=${secret:auth-db-password}", // mesmo nome, não deve duplicar
	}

	got := ExtractNames(env)
	sort.Strings(got)
	want := []string{"api-key", "auth-db-password"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractNames = %v, want %v", got, want)
	}
}

func TestExtractNamesNoReferencesIsEmpty(t *testing.T) {
	got := ExtractNames([]string{"A=1", "B=2"})
	if len(got) != 0 {
		t.Fatalf("esperava lista vazia, veio %v", got)
	}
}

func TestSubstituteReplacesAllOccurrences(t *testing.T) {
	values := map[string]string{"auth-db-password": "s3nha", "api-key": "abc123"}
	env := "DATABASE_URL=postgres://auth:${secret:auth-db-password}@database:5432/app?key=${secret:api-key}"

	got := substitute(env, values)
	want := "DATABASE_URL=postgres://auth:s3nha@database:5432/app?key=abc123"
	if got != want {
		t.Fatalf("substitute = %q, want %q", got, want)
	}
}

func TestResolveEnvSkipsNetworkWhenNoReferences(t *testing.T) {
	// Sem secretsAdminURL/refreshToken nenhum configurado -- se
	// ResolveEnv tentasse mesmo assim chamar a rede, isto falharia com um
	// erro de conexão, não silenciosamente. Passar significa que o
	// caminho rápido (sem referências) realmente não toca na rede.
	c := New("", "", "")
	env := []string{"A=1", "B=2"}

	got, err := c.ResolveEnv(context.Background(), env)
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	if !reflect.DeepEqual(got, env) {
		t.Fatalf("ResolveEnv = %v, want env inalterado %v", got, env)
	}
}

func TestResolveEnvFailsWhenUnconfiguredButReferenced(t *testing.T) {
	c := New("", "", "")
	_, err := c.ResolveEnv(context.Background(), []string{"A=${secret:x}"})
	if err == nil {
		t.Fatalf("esperava erro (não configurado, mas há referência)")
	}
}

// newFakeBackend simula tanto o endpoint de refresh do auth service
// quanto o /v1/secrets/resolve do secretsadmin, para exercitar
// ResolveEnv de ponta a ponta sem nenhum serviço real de pé.
func newFakeBackend(t *testing.T, secretValues map[string]string) (authURL, secretsURL string, refreshCalls *int) {
	t.Helper()
	calls := 0
	refreshCalls = &calls

	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/token/refresh" {
			http.NotFound(w, r)
			return
		}
		calls++
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-" + string(rune('0'+calls)),
			"refresh_token": "refresh-" + string(rune('0'+calls)),
			"expires_in":    900,
		})
	}))
	t.Cleanup(authSrv.Close)

	secretsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/secrets/resolve" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct{ Names []string }
		json.NewDecoder(r.Body).Decode(&req)

		values := map[string]string{}
		for _, n := range req.Names {
			v, ok := secretValues[n]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "secret_not_found", "message": "segredo desconhecido: " + n})
				return
			}
			values[n] = v
		}
		json.NewEncoder(w).Encode(map[string]any{"values": values})
	}))
	t.Cleanup(secretsSrv.Close)

	return authSrv.URL, secretsSrv.URL, refreshCalls
}

func TestResolveEnvEndToEnd(t *testing.T) {
	authURL, secretsURL, refreshCalls := newFakeBackend(t, map[string]string{"auth-db-password": "s3nha"})
	c := New(secretsURL, authURL, "initial-refresh-token")

	env := []string{"DATABASE_URL=postgres://auth:${secret:auth-db-password}@database:5432/app"}
	got, err := c.ResolveEnv(context.Background(), env)
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	want := []string{"DATABASE_URL=postgres://auth:s3nha@database:5432/app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveEnv = %v, want %v", got, want)
	}
	if *refreshCalls != 1 {
		t.Fatalf("refresh chamado %d vezes, want 1", *refreshCalls)
	}

	// Uma segunda chamada, com o access token ainda válido em cache, não
	// deveria voltar a chamar refresh.
	if _, err := c.ResolveEnv(context.Background(), env); err != nil {
		t.Fatalf("segunda ResolveEnv: %v", err)
	}
	if *refreshCalls != 1 {
		t.Fatalf("refresh chamado %d vezes após segunda resolução, want continuar 1 (cache)", *refreshCalls)
	}
}

func TestResolveEnvPropagatesUnknownSecretError(t *testing.T) {
	authURL, secretsURL, _ := newFakeBackend(t, map[string]string{"existe": "valor"})
	c := New(secretsURL, authURL, "initial-refresh-token")

	_, err := c.ResolveEnv(context.Background(), []string{"A=${secret:nao-existe}"})
	if err == nil {
		t.Fatalf("esperava erro para segredo desconhecido")
	}
}
