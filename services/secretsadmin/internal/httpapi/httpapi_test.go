package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"secretsadmin/internal/audit"
	"secretsadmin/internal/jwtverify"
	"secretsadmin/internal/store"
)

type fakeVerifier struct {
	tokens map[string]jwtverify.Claims
}

func (f *fakeVerifier) Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error) {
	claims, ok := f.tokens[tokenString]
	if !ok {
		return jwtverify.Claims{}, jwtverify.ErrInvalidToken
	}
	return claims, nil
}

// fakeSeal é um "cifrador" de teste que não cifra nada de verdade -- só
// marca visivelmente que passou por Seal/Open, o suficiente para os
// handlers exercitarem o caminho completo sem precisar de AES real.
type fakeSeal struct{}

func (fakeSeal) Seal(plaintext []byte) ([]byte, []byte, error) {
	return append([]byte("sealed:"), plaintext...), []byte("nonce"), nil
}

func (fakeSeal) Open(ciphertext, nonce []byte) ([]byte, error) {
	return ciphertext[len("sealed:"):], nil
}

type fakeStore struct {
	mu      sync.Mutex
	secrets map[string]store.Secret
}

func newFakeStore() *fakeStore {
	return &fakeStore{secrets: map[string]store.Secret{}}
}

func (f *fakeStore) List(ctx context.Context) ([]store.SecretInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.SecretInfo, 0, len(f.secrets))
	for _, s := range f.secrets {
		out = append(out, store.SecretInfo{Name: s.Name, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, UpdatedBy: s.UpdatedBy})
	}
	return out, nil
}

func (f *fakeStore) Get(ctx context.Context, name string) (store.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.secrets[name]
	if !ok {
		return store.Secret{}, store.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) GetMany(ctx context.Context, names []string) (map[string]store.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]store.Secret, len(names))
	for _, n := range names {
		if s, ok := f.secrets[n]; ok {
			out[n] = s
		}
	}
	return out, nil
}

func (f *fakeStore) Upsert(ctx context.Context, name string, ciphertext, nonce []byte, updatedBy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.secrets[name]
	now := time.Now()
	createdAt := now
	if ok {
		createdAt = existing.CreatedAt
	}
	f.secrets[name] = store.Secret{Name: name, Ciphertext: ciphertext, Nonce: nonce, CreatedAt: createdAt, UpdatedAt: now, UpdatedBy: updatedBy}
	return nil
}

func (f *fakeStore) Delete(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.secrets[name]; !ok {
		return store.ErrNotFound
	}
	delete(f.secrets, name)
	return nil
}

func (f *fakeStore) Ping(ctx context.Context) error { return nil }

func newTestHandler(s Store) (*Handler, *fakeVerifier) {
	verifier := &fakeVerifier{tokens: map[string]jwtverify.Claims{
		"infra-admin-token": {Subject: "operator@example.com", Role: RoleInfraAdmin},
		"service-token":     {Subject: "svc-group-authd", Role: RoleService},
		"user-token":        {Subject: "user@example.com", Role: "user"},
		"super-admin-token": {Subject: "root@example.com", Role: RoleSuperAdmin},
	}}
	return NewHandler(verifier, s, fakeSeal{}, audit.NewLogger(slog.Default())), verifier
}

func doRequest(t *testing.T, mux *http.ServeMux, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRequireRoleRejectsMissingAndWrongRole(t *testing.T) {
	h, _ := newTestHandler(newFakeStore())
	mux := h.Routes()

	if rec := doRequest(t, mux, "GET", "/v1/secrets", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sem token: status = %d, want 401", rec.Code)
	}
	if rec := doRequest(t, mux, "GET", "/v1/secrets", "user-token", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("role errada: status = %d, want 403", rec.Code)
	}
	// Uma conta "service" não consegue gerir nomes -- só resolver.
	if rec := doRequest(t, mux, "GET", "/v1/secrets", "service-token", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("role service em endpoint de gestão: status = %d, want 403", rec.Code)
	}
	// Um infra-admin não consegue resolver -- só gerir.
	if rec := doRequest(t, mux, "POST", "/v1/secrets/resolve", "infra-admin-token", `{"names":["x"]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("role infra-admin em endpoint de resolve: status = %d, want 403", rec.Code)
	}
}

// TestSuperAdminBypassesEveryRoleCheck confirma o bypass universal (ver
// services/auth/internal/store/role.go): super-admin consegue gerir nomes
// (RoleInfraAdmin) E resolver valores (RoleService), sem precisar de duas
// contas separadas.
func TestSuperAdminBypassesEveryRoleCheck(t *testing.T) {
	fake := newFakeStore()
	h, _ := newTestHandler(fake)
	mux := h.Routes()

	if rec := doRequest(t, mux, "GET", "/v1/secrets", "super-admin-token", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/secrets com super-admin: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	putRec := doRequest(t, mux, "PUT", "/v1/secrets/x", "super-admin-token", `{"value":"y"}`)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT com super-admin: status = %d, want 200, body=%s", putRec.Code, putRec.Body.String())
	}

	resolveRec := doRequest(t, mux, "POST", "/v1/secrets/resolve", "super-admin-token", `{"names":["x"]}`)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("resolve com super-admin: status = %d, want 200, body=%s", resolveRec.Code, resolveRec.Body.String())
	}
}

func TestUpsertRejectsInvalidName(t *testing.T) {
	h, _ := newTestHandler(newFakeStore())
	mux := h.Routes()

	rec := doRequest(t, mux, "PUT", "/v1/secrets/nome%20com%20espaco", "infra-admin-token", `{"value":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpsertRejectsEmptyValue(t *testing.T) {
	h, _ := newTestHandler(newFakeStore())
	mux := h.Routes()

	rec := doRequest(t, mux, "PUT", "/v1/secrets/auth-db-password", "infra-admin-token", `{"value":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpsertListDeleteRoundTrip(t *testing.T) {
	fake := newFakeStore()
	h, _ := newTestHandler(fake)
	mux := h.Routes()

	putRec := doRequest(t, mux, "PUT", "/v1/secrets/auth-db-password", "infra-admin-token", `{"value":"segredo123"}`)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put: status = %d, want 200, body=%s", putRec.Code, putRec.Body.String())
	}
	var putBody map[string]string
	json.NewDecoder(putRec.Body).Decode(&putBody)
	if putBody["name"] != "auth-db-password" {
		t.Fatalf("resposta do put não devolve o nome: %+v", putBody)
	}
	if bytes.Contains(putRec.Body.Bytes(), []byte("segredo123")) {
		t.Fatalf("resposta do put ecoa o valor em claro -- NUNCA deveria")
	}

	listRec := doRequest(t, mux, "GET", "/v1/secrets", "infra-admin-token", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", listRec.Code)
	}
	var infos []store.SecretInfo
	json.NewDecoder(listRec.Body).Decode(&infos)
	if len(infos) != 1 || infos[0].Name != "auth-db-password" {
		t.Fatalf("list inesperada: %+v", infos)
	}
	if bytes.Contains(listRec.Body.Bytes(), []byte("segredo123")) {
		t.Fatalf("list vaza o valor em claro -- NUNCA deveria")
	}

	delRec := doRequest(t, mux, "DELETE", "/v1/secrets/auth-db-password", "infra-admin-token", "")
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204", delRec.Code)
	}

	delAgainRec := doRequest(t, mux, "DELETE", "/v1/secrets/auth-db-password", "infra-admin-token", "")
	if delAgainRec.Code != http.StatusNotFound {
		t.Fatalf("delete de novo: status = %d, want 404", delAgainRec.Code)
	}
}

func TestResolveReturnsPlaintextOnlyToServiceRole(t *testing.T) {
	fake := newFakeStore()
	h, _ := newTestHandler(fake)
	mux := h.Routes()

	doRequest(t, mux, "PUT", "/v1/secrets/auth-db-password", "infra-admin-token", `{"value":"segredo123"}`)

	resolveRec := doRequest(t, mux, "POST", "/v1/secrets/resolve", "service-token", `{"names":["auth-db-password"]}`)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("resolve: status = %d, want 200, body=%s", resolveRec.Code, resolveRec.Body.String())
	}
	var resp resolveResponse
	json.NewDecoder(resolveRec.Body).Decode(&resp)
	if resp.Values["auth-db-password"] != "segredo123" {
		t.Fatalf("valor resolvido inesperado: %+v", resp.Values)
	}
}

func TestResolveFailsFastOnUnknownName(t *testing.T) {
	fake := newFakeStore()
	h, _ := newTestHandler(fake)
	mux := h.Routes()

	doRequest(t, mux, "PUT", "/v1/secrets/existe", "infra-admin-token", `{"value":"x"}`)

	rec := doRequest(t, mux, "POST", "/v1/secrets/resolve", "service-token", `{"names":["existe","nao-existe"]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestResolveEmptyNamesReturnsEmptyMap(t *testing.T) {
	h, _ := newTestHandler(newFakeStore())
	mux := h.Routes()

	rec := doRequest(t, mux, "POST", "/v1/secrets/resolve", "service-token", `{"names":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp resolveResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Values) != 0 {
		t.Fatalf("esperava mapa vazio, veio %+v", resp.Values)
	}
}

func TestHealthzAndReadyzArePublic(t *testing.T) {
	h, _ := newTestHandler(newFakeStore())
	mux := h.Routes()

	if rec := doRequest(t, mux, "GET", "/healthz", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("healthz: status = %d, want 200", rec.Code)
	}
	if rec := doRequest(t, mux, "GET", "/readyz", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("readyz: status = %d, want 200", rec.Code)
	}
}
