package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"auth/internal/refresh"
	"auth/internal/store"
	"auth/internal/token"
)

// Os três fakes abaixo substituem Postgres e chaves RSA reais -- os
// handlers só conhecem as interfaces (UserStore/TokenIssuer/RefreshIssuer),
// então testar a lógica HTTP (validação, códigos de status, mapeamento de
// erros) não precisa de nenhuma infraestrutura real.

type fakeUsers struct {
	mu      sync.Mutex
	nextID  int
	byEmail map[string]store.User
	hashes  map[string]string // por userID
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byEmail: map[string]store.User{}, hashes: map[string]string{}}
}

func (f *fakeUsers) CreateUserWithPassword(_ context.Context, email, hash string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.byEmail[email]; exists {
		return store.User{}, store.ErrEmailTaken
	}
	f.nextID++
	u := store.User{ID: fmt.Sprintf("user-%d", f.nextID), Email: email, CreatedAt: time.Now()}
	f.byEmail[email] = u
	f.hashes[u.ID] = hash
	return u, nil
}

func (f *fakeUsers) FindUserByEmailWithPassword(_ context.Context, email string) (store.User, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byEmail[email]
	if !ok {
		return store.User{}, "", store.ErrUserNotFound
	}
	return u, f.hashes[u.ID], nil
}

type fakeTokens struct{}

func (fakeTokens) Sign(subject string) (string, error) {
	return "access-for-" + subject, nil
}

func (fakeTokens) JWKS() token.JWKSDocument {
	return token.JWKSDocument{Keys: []token.JWK{{Kty: "RSA", Use: "sig", Alg: "RS256", Kid: "fake", N: "n", E: "e"}}}
}

type fakeRefresh struct {
	mu      sync.Mutex
	next    int
	owner   map[string]string // token -> userID
	revoked map[string]bool
}

func newFakeRefresh() *fakeRefresh {
	return &fakeRefresh{owner: map[string]string{}, revoked: map[string]bool{}}
}

func (f *fakeRefresh) Issue(_ context.Context, userID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	tok := fmt.Sprintf("refresh-%d", f.next)
	f.owner[tok] = userID
	return tok, nil
}

func (f *fakeRefresh) Rotate(ctx context.Context, presented string) (string, string, error) {
	f.mu.Lock()
	userID, ok := f.owner[presented]
	if !ok {
		f.mu.Unlock()
		return "", "", refresh.ErrInvalid
	}
	if f.revoked[presented] {
		f.mu.Unlock()
		return "", "", refresh.ErrReuseDetected
	}
	f.revoked[presented] = true
	f.mu.Unlock()

	next, err := f.Issue(ctx, userID)
	return next, userID, err
}

func (f *fakeRefresh) Revoke(_ context.Context, presented string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[presented] = true
	return nil
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type testDeps struct {
	users   *fakeUsers
	refresh *fakeRefresh
	pinger  *fakePinger
	handler *Handler
}

func newTestDeps() *testDeps {
	users := newFakeUsers()
	refreshMgr := newFakeRefresh()
	pinger := &fakePinger{}
	h := NewHandler(users, fakeTokens{}, refreshMgr, pinger, 15*time.Minute)
	return &testDeps{users: users, refresh: refreshMgr, pinger: pinger, handler: h}
}

func doRequest(t *testing.T, mux http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decodificando resposta %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestRegisterSuccess(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()

	rec := doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "senha-forte"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeBody[registerResponse](t, rec)
	if resp.Email != "ana@example.test" || resp.UserID == "" {
		t.Fatalf("resposta inesperada: %+v", resp)
	}
}

func TestRegisterInvalidEmail(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/register", registerRequest{Email: "not-an-email", Password: "senha-forte"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRegisterWeakPassword(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "123"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	body := registerRequest{Email: "ana@example.test", Password: "senha-forte"}

	if rec := doRequest(t, mux, http.MethodPost, "/v1/register", body); rec.Code != http.StatusCreated {
		t.Fatalf("primeira criação: status = %d", rec.Code)
	}
	rec := doRequest(t, mux, http.MethodPost, "/v1/register", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestLoginSuccess(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "senha-forte"})

	rec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "ana@example.test", Password: "senha-forte"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	pair := decodeBody[tokenPair](t, rec)
	if pair.AccessToken == "" || pair.RefreshToken == "" || pair.TokenType != "Bearer" {
		t.Fatalf("tokenPair inesperado: %+v", pair)
	}
}

func TestLoginWrongPasswordAndUnknownEmailGiveSameError(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "senha-forte"})

	wrongPassword := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "ana@example.test", Password: "errada"})
	unknownEmail := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "ninguem@example.test", Password: "qualquer"})

	if wrongPassword.Code != http.StatusUnauthorized || unknownEmail.Code != http.StatusUnauthorized {
		t.Fatalf("status codes = %d, %d, want 401, 401", wrongPassword.Code, unknownEmail.Code)
	}

	// Não é só o status: o corpo (código de erro) também precisa ser
	// idêntico, senão dá pra distinguir os dois casos e enumerar e-mails.
	errA := decodeBody[errorResponse](t, wrongPassword)
	errB := decodeBody[errorResponse](t, unknownEmail)
	if errA.Error.Code != errB.Error.Code {
		t.Fatalf("códigos de erro diferentes entre senha errada e e-mail inexistente: %q vs %q", errA.Error.Code, errB.Error.Code)
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "senha-forte"})
	loginRec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "ana@example.test", Password: "senha-forte"})
	original := decodeBody[tokenPair](t, loginRec)

	rec := doRequest(t, mux, http.MethodPost, "/v1/token/refresh", refreshRequest{RefreshToken: original.RefreshToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	rotated := decodeBody[tokenPair](t, rec)
	if rotated.RefreshToken == original.RefreshToken {
		t.Fatal("refresh token não mudou após rotação")
	}

	// O token antigo já rodado não pode mais ser usado.
	reuse := doRequest(t, mux, http.MethodPost, "/v1/token/refresh", refreshRequest{RefreshToken: original.RefreshToken})
	if reuse.Code != http.StatusUnauthorized {
		t.Fatalf("status do reuso = %d, want 401", reuse.Code)
	}
}

func TestRefreshRejectsUnknownToken(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/token/refresh", refreshRequest{RefreshToken: "nunca-existiu"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRefreshRejectsMissingToken(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/token/refresh", refreshRequest{RefreshToken: ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestLogoutThenRefreshFails(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "senha-forte"})
	loginRec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "ana@example.test", Password: "senha-forte"})
	pair := decodeBody[tokenPair](t, loginRec)

	logoutRec := doRequest(t, mux, http.MethodPost, "/v1/logout", refreshRequest{RefreshToken: pair.RefreshToken})
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("status do logout = %d, want 204", logoutRec.Code)
	}

	rec := doRequest(t, mux, http.MethodPost, "/v1/token/refresh", refreshRequest{RefreshToken: pair.RefreshToken})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 após logout", rec.Code)
	}
}

func TestLogoutOfUnknownTokenIsNoContent(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/logout", refreshRequest{RefreshToken: "nunca-existiu"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestJWKSEndpoint(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodGet, "/.well-known/jwks.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	doc := decodeBody[token.JWKSDocument](t, rec)
	if len(doc.Keys) != 1 || doc.Keys[0].Kid != "fake" {
		t.Fatalf("JWKS inesperada: %+v", doc)
	}
}

func TestHealthzAlwaysOk(t *testing.T) {
	deps := newTestDeps()
	deps.pinger.err = errors.New("db caída") // healthz não depende da BD
	rec := doRequest(t, deps.handler.Routes(), http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestReadyzReflectsDBPing(t *testing.T) {
	deps := newTestDeps()

	ok := doRequest(t, deps.handler.Routes(), http.MethodGet, "/readyz", nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 com BD saudável", ok.Code)
	}

	deps.pinger.err = errors.New("db caída")
	down := doRequest(t, deps.handler.Routes(), http.MethodGet, "/readyz", nil)
	if down.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 com BD indisponível", down.Code)
	}
}
