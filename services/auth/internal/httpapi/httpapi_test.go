package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"auth/internal/apikey"
	"auth/internal/oauth"
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
	u := store.User{ID: fmt.Sprintf("user-%d", f.nextID), Email: email, CreatedAt: time.Now(), Role: store.RoleUser}
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

func (f *fakeUsers) FindUserByEmail(_ context.Context, email string) (store.User, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byEmail[email]
	return u, ok, nil
}

func (f *fakeUsers) FindUserByID(_ context.Context, id string) (store.User, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byEmail {
		if u.ID == id {
			return u, true, nil
		}
	}
	return store.User{}, false, nil
}

// ListUsers/CreateUserWithPasswordAndRole/UpdateUserRole implementam
// UserAdmin -- fakeUsers serve os dois papéis (UserStore e UserAdmin) nos
// testes, exatamente como *store.DB serve os dois em produção.
func (f *fakeUsers) ListUsers(_ context.Context) ([]store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	users := make([]store.User, 0, len(f.byEmail))
	for _, u := range f.byEmail {
		users = append(users, u)
	}
	return users, nil
}

func (f *fakeUsers) CreateUserWithPasswordAndRole(_ context.Context, email, hash, role string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.byEmail[email]; exists {
		return store.User{}, store.ErrEmailTaken
	}
	f.nextID++
	now := time.Now()
	u := store.User{ID: fmt.Sprintf("user-%d", f.nextID), Email: email, CreatedAt: now, EmailVerifiedAt: &now, Role: role}
	f.byEmail[email] = u
	f.hashes[u.ID] = hash
	return u, nil
}

func (f *fakeUsers) UpdateUserRole(_ context.Context, id, role string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for email, u := range f.byEmail {
		if u.ID == id {
			u.Role = role
			f.byEmail[email] = u
			return true, nil
		}
	}
	return false, nil
}

// setRole é um atalho só de teste para preparar um utilizador já
// promovido antes de fazer login (evita depender de UpdateUserRole + um
// ID que os testes teriam de descobrir primeiro).
func (f *fakeUsers) setRole(email, role string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.byEmail[email]
	u.Role = role
	f.byEmail[email] = u
}

// fakeEmailVerifier simula internal/verification.Manager -- cada teste
// pode espiar o último token emitido (lastIssuedToken) ou forçar Verify a
// falhar/suceder.
type fakeEmailVerifier struct {
	mu              sync.Mutex
	next            int
	lastIssuedToken string
	lastIssuedUser  string
	verifyErr       error
	verifyUserID    string
}

func newFakeEmailVerifier() *fakeEmailVerifier {
	return &fakeEmailVerifier{}
}

func (f *fakeEmailVerifier) Issue(_ context.Context, userID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	tok := fmt.Sprintf("verify-token-%d", f.next)
	f.lastIssuedToken = tok
	f.lastIssuedUser = userID
	return tok, nil
}

func (f *fakeEmailVerifier) Verify(_ context.Context, presented string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verifyErr != nil {
		return "", f.verifyErr
	}
	if presented != f.lastIssuedToken {
		return "", errors.New("token desconhecido")
	}
	return f.verifyUserID, nil
}

type fakeTokens struct{}

func (fakeTokens) Sign(subject, role string) (string, error) {
	return "access-for-" + subject + "|" + role, nil
}

func (fakeTokens) JWKS() token.JWKSDocument {
	return token.JWKSDocument{Keys: []token.JWK{{Kty: "RSA", Use: "sig", Alg: "RS256", Kid: "fake", N: "n", E: "e"}}}
}

// Verify é o inverso de Sign: só sabe desfazer o formato
// "access-for-<sub>|<role>" que este fake usa -- suficiente pra exercitar
// requireAuth/requireRole nos testes sem precisar de um Manager de JWT
// real.
func (fakeTokens) Verify(tokenString string) (token.Claims, error) {
	const prefix = "access-for-"
	if !strings.HasPrefix(tokenString, prefix) {
		return token.Claims{}, token.ErrInvalidToken
	}
	rest := strings.TrimPrefix(tokenString, prefix)
	subject, role, _ := strings.Cut(rest, "|")
	return token.Claims{Subject: subject, Role: role}, nil
}

type fakeAPIKeys struct {
	mu     sync.Mutex
	next   int
	byHash map[string]apikey.Key // por plaintext (não é um hash de verdade, é só o fake)
}

func newFakeAPIKeys() *fakeAPIKeys {
	return &fakeAPIKeys{byHash: map[string]apikey.Key{}}
}

func (f *fakeAPIKeys) Issue(_ context.Context, owner string, scopes []string, ttl *time.Duration) (string, apikey.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	plaintext := fmt.Sprintf("ak_fake_%d", f.next)
	k := apikey.Key{ID: fmt.Sprintf("key-%d", f.next), Owner: owner, Scopes: scopes, CreatedAt: time.Now()}
	if ttl != nil {
		exp := k.CreatedAt.Add(*ttl)
		k.ExpiresAt = &exp
	}
	f.byHash[plaintext] = k
	return plaintext, k, nil
}

func (f *fakeAPIKeys) Verify(_ context.Context, presented string) (apikey.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.byHash[presented]
	if !ok || k.RevokedAt != nil {
		return apikey.Key{}, apikey.ErrInvalid
	}
	if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
		return apikey.Key{}, apikey.ErrInvalid
	}
	return k, nil
}

func (f *fakeAPIKeys) List(_ context.Context, owner string) ([]apikey.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []apikey.Key
	for _, k := range f.byHash {
		if k.Owner == owner {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (f *fakeAPIKeys) Revoke(_ context.Context, id, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for plaintext, k := range f.byHash {
		if k.ID == id {
			if k.Owner != owner {
				return apikey.ErrNotFound
			}
			now := time.Now()
			k.RevokedAt = &now
			f.byHash[plaintext] = k
			return nil
		}
	}
	return apikey.ErrNotFound
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

// fakeOAuthLogin simula internal/oauth.Manager sem nenhum provider real
// nem rede -- cada teste configura diretamente o resultado que
// AuthCodeURL/Login devem devolver.
type fakeOAuthLogin struct {
	authCodeURLResult string
	authCodeURLErr    error
	loginResult       store.User
	loginErr          error
	lastLoginProvider string
	lastLoginCode     string
}

func (f *fakeOAuthLogin) AuthCodeURL(provider, state string) (string, error) {
	if f.authCodeURLErr != nil {
		return "", f.authCodeURLErr
	}
	return f.authCodeURLResult, nil
}

func (f *fakeOAuthLogin) Login(_ context.Context, provider, code string) (store.User, error) {
	f.lastLoginProvider = provider
	f.lastLoginCode = code
	if f.loginErr != nil {
		return store.User{}, f.loginErr
	}
	return f.loginResult, nil
}

type testDeps struct {
	users        *fakeUsers
	refresh      *fakeRefresh
	apiKeys      *fakeAPIKeys
	oauth        *fakeOAuthLogin
	verification *fakeEmailVerifier
	pinger       *fakePinger
	handler      *Handler
}

func newTestDeps() *testDeps {
	users := newFakeUsers()
	refreshMgr := newFakeRefresh()
	apiKeys := newFakeAPIKeys()
	oauthLogin := &fakeOAuthLogin{}
	emailVerifier := newFakeEmailVerifier()
	pinger := &fakePinger{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHandler(users, users, fakeTokens{}, refreshMgr, apiKeys, oauthLogin, emailVerifier, pinger, 15*time.Minute, logger)
	return &testDeps{
		users: users, refresh: refreshMgr, apiKeys: apiKeys, oauth: oauthLogin,
		verification: emailVerifier, pinger: pinger, handler: h,
	}
}

// loginAndGetAccessToken registra e autentica um utilizador novo,
// devolvendo o access token -- usado pelos testes de API keys, que
// exigem um Bearer token válido (ver requireAuth).
func loginAndGetAccessToken(t *testing.T, mux http.Handler, email string) string {
	t.Helper()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: email, Password: "senha-forte"})
	rec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: email, Password: "senha-forte"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	return decodeBody[tokenPair](t, rec).AccessToken
}

// loginAndGetAccessTokenWithRole é como loginAndGetAccessToken, mas
// promove a conta à role informada (via o atalho de teste
// fakeUsers.setRole) antes do login -- assim o access token devolvido já
// carrega essa role na claim, como aconteceria em produção depois de um
// super-admin chamar PATCH /v1/admin/users/{id}/role e a pessoa fazer
// login (ou refresh) de novo.
func loginAndGetAccessTokenWithRole(t *testing.T, mux http.Handler, users *fakeUsers, email, role string) string {
	t.Helper()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: email, Password: "senha-forte"})
	users.setRole(email, role)
	rec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: email, Password: "senha-forte"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	return decodeBody[tokenPair](t, rec).AccessToken
}

func doAuthedRequest(t *testing.T, mux http.Handler, method, path, accessToken string, body any) *httptest.ResponseRecorder {
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
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
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

	// O registo tem de emitir um token de verificação -- e ele NUNCA pode
	// vir na resposta HTTP (só "chega" por e-mail, fora deste teste).
	if deps.verification.lastIssuedUser != resp.UserID {
		t.Fatalf("esperava token de verificação emitido para %q, foi para %q", resp.UserID, deps.verification.lastIssuedUser)
	}
	if strings.Contains(rec.Body.String(), deps.verification.lastIssuedToken) {
		t.Fatal("o token de verificação NUNCA deve aparecer na resposta HTTP de registo")
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

func TestAdminEndpointsRejectNonSuperAdmin(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	userToken := loginAndGetAccessToken(t, mux, "ana@example.test")

	list := doAuthedRequest(t, mux, http.MethodGet, "/v1/admin/users", userToken, nil)
	if list.Code != http.StatusForbidden {
		t.Fatalf("GET /v1/admin/users: status = %d, want 403", list.Code)
	}

	create := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users", userToken,
		createUserRequest{Email: "novo@example.test", Password: "senha-forte", Role: store.RoleAdmin})
	if create.Code != http.StatusForbidden {
		t.Fatalf("POST /v1/admin/users: status = %d, want 403", create.Code)
	}
}

func TestAdminEndpointsRequireAuth(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodGet, "/v1/admin/users", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSuperAdminCanCreateListAndPromoteUsers(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	superToken := loginAndGetAccessTokenWithRole(t, mux, deps.users, "root@example.test", store.RoleSuperAdmin)

	createRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users", superToken,
		createUserRequest{Email: "nova@example.test", Password: "senha-forte", Role: store.RoleInfraAdmin})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", createRec.Code, createRec.Body.String())
	}
	created := decodeBody[userListItem](t, createRec)
	if created.Role != store.RoleInfraAdmin || created.EmailVerifiedAt == nil {
		t.Fatalf("utilizador criado inesperado: %+v", created)
	}

	listRec := doAuthedRequest(t, mux, http.MethodGet, "/v1/admin/users", superToken, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", listRec.Code)
	}
	users := decodeBody[[]userListItem](t, listRec)
	if len(users) != 2 { // root + nova
		t.Fatalf("esperava 2 utilizadores, veio %d: %+v", len(users), users)
	}

	updateRec := doAuthedRequest(t, mux, http.MethodPatch, "/v1/admin/users/"+created.ID+"/role", superToken,
		updateUserRoleRequest{Role: store.RoleAdmin})
	if updateRec.Code != http.StatusNoContent {
		t.Fatalf("update role: status = %d, want 204, body=%s", updateRec.Code, updateRec.Body.String())
	}

	// A conta atualizada precisa refletir a nova role num login seguinte.
	loginRec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "nova@example.test", Password: "senha-forte"})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login pós-promoção: status = %d", loginRec.Code)
	}
}

func TestMintServiceTokenIssuesTokenPairForServiceAccount(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	superToken := loginAndGetAccessTokenWithRole(t, mux, deps.users, "root@example.test", store.RoleSuperAdmin)

	createRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users", superToken,
		createUserRequest{Email: "svc-group-authd@service.test", Password: "não-vai-ser-usada-1", Role: store.RoleService})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201, body=%s", createRec.Code, createRec.Body.String())
	}
	created := decodeBody[userListItem](t, createRec)

	mintRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users/"+created.ID+"/tokens", superToken, nil)
	if mintRec.Code != http.StatusCreated {
		t.Fatalf("mint: status = %d, want 201, body=%s", mintRec.Code, mintRec.Body.String())
	}
	pair := decodeBody[tokenPair](t, mintRec)
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("tokens vazios: %+v", pair)
	}

	// O refresh token emitido tem de funcionar como qualquer outro.
	refreshRec := doRequest(t, mux, http.MethodPost, "/v1/token/refresh", refreshRequest{RefreshToken: pair.RefreshToken})
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh do token emitido: status = %d, want 200, body=%s", refreshRec.Code, refreshRec.Body.String())
	}
}

func TestMintServiceTokenRejectsNonServiceAccount(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	superToken := loginAndGetAccessTokenWithRole(t, mux, deps.users, "root@example.test", store.RoleSuperAdmin)

	createRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users", superToken,
		createUserRequest{Email: "humana@example.test", Password: "senha-forte", Role: store.RoleInfraAdmin})
	created := decodeBody[userListItem](t, createRec)

	mintRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users/"+created.ID+"/tokens", superToken, nil)
	if mintRec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (conta não é \"service\")", mintRec.Code)
	}
}

func TestMintServiceTokenRejectsUnknownID(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	superToken := loginAndGetAccessTokenWithRole(t, mux, deps.users, "root@example.test", store.RoleSuperAdmin)

	mintRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users/00000000-0000-0000-0000-000000000000/tokens", superToken, nil)
	if mintRec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", mintRec.Code)
	}
}

func TestMintServiceTokenRejectsNonSuperAdmin(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	userToken := loginAndGetAccessToken(t, mux, "ana@example.test")

	rec := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users/whatever/tokens", userToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCreateUserRejectsInvalidRole(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	superToken := loginAndGetAccessTokenWithRole(t, mux, deps.users, "root@example.test", store.RoleSuperAdmin)

	rec := doAuthedRequest(t, mux, http.MethodPost, "/v1/admin/users", superToken,
		createUserRequest{Email: "novo@example.test", Password: "senha-forte", Role: "super-usuario-inventado"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateUserRoleRejectsUnknownID(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	superToken := loginAndGetAccessTokenWithRole(t, mux, deps.users, "root@example.test", store.RoleSuperAdmin)

	rec := doAuthedRequest(t, mux, http.MethodPatch, "/v1/admin/users/nao-existe/role", superToken,
		updateUserRoleRequest{Role: store.RoleAdmin})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// RegisterRequest não tem campo "role" -- este teste é uma trava contra
// alguém adicionar um no futuro sem pensar no risco: JSON com uma chave
// que a struct não conhece é só ignorado pelo decoder, então "role" vindo
// do cliente aqui nunca teve efeito nenhum. Fixa esse comportamento.
func TestRegisterIgnoresRoleFieldFromClient(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()

	body := `{"email":"ana@example.test","password":"senha-forte","role":"super-admin"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}

	u, _, err := deps.users.FindUserByEmailWithPassword(context.Background(), "ana@example.test")
	if err != nil {
		t.Fatalf("FindUserByEmailWithPassword: %v", err)
	}
	if u.Role != store.RoleUser {
		t.Fatalf("role = %q, want %q (self-registration nunca deve aceitar role do cliente)", u.Role, store.RoleUser)
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

func TestCreateAPIKeyRequiresAuth(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/api-keys", createAPIKeyRequest{Scopes: []string{"read:x"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 sem Authorization", rec.Code)
	}
}

func TestCreateAPIKeyRejectsInvalidToken(t *testing.T) {
	deps := newTestDeps()
	rec := doAuthedRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/api-keys", "token-invalido", createAPIKeyRequest{})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 com token inválido", rec.Code)
	}
}

func TestCreateAndIntrospectAPIKey(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	access := loginAndGetAccessToken(t, mux, "ana@example.test")

	rec := doAuthedRequest(t, mux, http.MethodPost, "/v1/api-keys", access, createAPIKeyRequest{Scopes: []string{"read:x", "write:x"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	created := decodeBody[apiKeyResponse](t, rec)
	if created.Key == "" || created.ID == "" {
		t.Fatalf("resposta de criação incompleta: %+v", created)
	}

	introspectRec := doRequest(t, mux, http.MethodPost, "/v1/api-keys/introspect", introspectRequest{Key: created.Key})
	if introspectRec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", introspectRec.Code)
	}
	introspected := decodeBody[introspectResponse](t, introspectRec)
	if !introspected.Active || len(introspected.Scopes) != 2 {
		t.Fatalf("introspecção inesperada: %+v", introspected)
	}
}

func TestListAPIKeysRequiresAuth(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodGet, "/v1/api-keys", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestListAPIKeysReturnsOnlyOwnKeysWithoutSecret(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	ownerToken := loginAndGetAccessToken(t, mux, "dono@example.test")
	otherToken := loginAndGetAccessToken(t, mux, "outro@example.test")

	doAuthedRequest(t, mux, http.MethodPost, "/v1/api-keys", ownerToken, createAPIKeyRequest{Scopes: []string{"read:x"}})
	doAuthedRequest(t, mux, http.MethodPost, "/v1/api-keys", otherToken, createAPIKeyRequest{Scopes: []string{"read:y"}})

	rec := doAuthedRequest(t, mux, http.MethodGet, "/v1/api-keys", ownerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	keys := decodeBody[[]apiKeyListItem](t, rec)
	if len(keys) != 1 {
		t.Fatalf("esperava 1 chave do dono, veio %d: %+v", len(keys), keys)
	}
	if keys[0].Status != "active" {
		t.Fatalf("status = %q, want active", keys[0].Status)
	}
}

func TestListAPIKeysMarksRevokedStatus(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	access := loginAndGetAccessToken(t, mux, "ana@example.test")

	createRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/api-keys", access, createAPIKeyRequest{})
	created := decodeBody[apiKeyResponse](t, createRec)
	doAuthedRequest(t, mux, http.MethodDelete, "/v1/api-keys/"+created.ID, access, nil)

	rec := doAuthedRequest(t, mux, http.MethodGet, "/v1/api-keys", access, nil)
	keys := decodeBody[[]apiKeyListItem](t, rec)
	if len(keys) != 1 || keys[0].Status != "revoked" {
		t.Fatalf("esperava 1 chave revogada, veio: %+v", keys)
	}
}

func TestIntrospectUnknownKeyIsInactive(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/api-keys/introspect", introspectRequest{Key: "ak_nao-existe"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (introspecção sempre responde 200)", rec.Code)
	}
	if decodeBody[introspectResponse](t, rec).Active {
		t.Fatal("chave desconhecida não devia introspectar como ativa")
	}
}

func TestRevokeAPIKeyThenIntrospectIsInactive(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	access := loginAndGetAccessToken(t, mux, "ana@example.test")

	createRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/api-keys", access, createAPIKeyRequest{})
	created := decodeBody[apiKeyResponse](t, createRec)

	revokeRec := doAuthedRequest(t, mux, http.MethodDelete, "/v1/api-keys/"+created.ID, access, nil)
	if revokeRec.Code != http.StatusNoContent {
		t.Fatalf("status da revogação = %d, want 204, body=%s", revokeRec.Code, revokeRec.Body.String())
	}

	introspectRec := doRequest(t, mux, http.MethodPost, "/v1/api-keys/introspect", introspectRequest{Key: created.Key})
	if decodeBody[introspectResponse](t, introspectRec).Active {
		t.Fatal("chave revogada não devia introspectar como ativa")
	}
}

func TestRevokeAPIKeyOfAnotherUserFails(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	ownerToken := loginAndGetAccessToken(t, mux, "dono@example.test")
	otherToken := loginAndGetAccessToken(t, mux, "outro@example.test")

	createRec := doAuthedRequest(t, mux, http.MethodPost, "/v1/api-keys", ownerToken, createAPIKeyRequest{})
	created := decodeBody[apiKeyResponse](t, createRec)

	revokeRec := doAuthedRequest(t, mux, http.MethodDelete, "/v1/api-keys/"+created.ID, otherToken, nil)
	if revokeRec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 ao tentar revogar chave de outro utilizador", revokeRec.Code)
	}
}

func TestOAuthStartRedirectsWithStateCookie(t *testing.T) {
	deps := newTestDeps()
	deps.oauth.authCodeURLResult = "https://provider.example/authorize?client_id=x"

	rec := doRequest(t, deps.handler.Routes(), http.MethodGet, "/v1/oauth/google/start", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != deps.oauth.authCodeURLResult {
		t.Fatalf("Location = %q, want %q", loc, deps.oauth.authCodeURLResult)
	}

	cookie := findCookie(t, rec, oauthStateCookie)
	if cookie.Value == "" {
		t.Fatal("cookie de state veio vazio")
	}
	if !cookie.HttpOnly {
		t.Fatal("cookie de state devia ser HttpOnly")
	}
}

func TestOAuthStartRejectsUnknownProvider(t *testing.T) {
	deps := newTestDeps()
	deps.oauth.authCodeURLErr = oauth.ErrUnknownProvider

	rec := doRequest(t, deps.handler.Routes(), http.MethodGet, "/v1/oauth/nao-existe/start", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// doCallbackRequest monta a requisição de callback já com o cookie de
// state que handleOAuthStart teria colocado -- simula o browser
// devolvendo o mesmo cookie que recebeu.
func doCallbackRequest(t *testing.T, mux http.Handler, query, stateCookieValue string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/oauth/google/callback?"+query, nil)
	if stateCookieValue != "" {
		req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: stateCookieValue})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func findCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %q não encontrado na resposta", name)
	return nil
}

func TestOAuthCallbackSuccess(t *testing.T) {
	deps := newTestDeps()
	deps.oauth.loginResult = store.User{ID: "user-1", Email: "ana@example.test"}
	mux := deps.handler.Routes()

	rec := doCallbackRequest(t, mux, "code=auth-code&state=opaque-state", "opaque-state")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	pair := decodeBody[tokenPair](t, rec)
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("tokenPair incompleto: %+v", pair)
	}
	if deps.oauth.lastLoginProvider != "google" || deps.oauth.lastLoginCode != "auth-code" {
		t.Fatalf("Login chamado com argumentos errados: provider=%q code=%q", deps.oauth.lastLoginProvider, deps.oauth.lastLoginCode)
	}

	// O cookie de state tem que ser limpo na resposta do callback,
	// aconteça o que acontecer -- é de uso único.
	cleared := findCookie(t, rec, oauthStateCookie)
	if cleared.MaxAge >= 0 {
		t.Fatalf("cookie de state devia ser removido (MaxAge negativo), got %d", cleared.MaxAge)
	}
}

func TestOAuthCallbackRejectsMismatchedState(t *testing.T) {
	deps := newTestDeps()
	rec := doCallbackRequest(t, deps.handler.Routes(), "code=auth-code&state=state-do-atacante", "state-legitimo")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOAuthCallbackRejectsMissingCookie(t *testing.T) {
	deps := newTestDeps()
	rec := doCallbackRequest(t, deps.handler.Routes(), "code=auth-code&state=qualquer", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOAuthCallbackRejectsMissingCode(t *testing.T) {
	deps := newTestDeps()
	rec := doCallbackRequest(t, deps.handler.Routes(), "state=opaque-state", "opaque-state")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOAuthCallbackHandlesProviderDenied(t *testing.T) {
	deps := newTestDeps()
	rec := doCallbackRequest(t, deps.handler.Routes(), "error=access_denied", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOAuthCallbackMapsEmailNotVerified(t *testing.T) {
	deps := newTestDeps()
	deps.oauth.loginErr = oauth.ErrEmailNotVerified

	rec := doCallbackRequest(t, deps.handler.Routes(), "code=auth-code&state=opaque-state", "opaque-state")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestOAuthCallbackMapsUnknownProvider(t *testing.T) {
	deps := newTestDeps()
	deps.oauth.loginErr = oauth.ErrUnknownProvider

	rec := doCallbackRequest(t, deps.handler.Routes(), "code=auth-code&state=opaque-state", "opaque-state")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestVerifyEmailSuccess(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()

	registerRec := doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "senha-forte"})
	created := decodeBody[registerResponse](t, registerRec)
	deps.verification.verifyUserID = created.UserID

	rec := doRequest(t, mux, http.MethodPost, "/v1/verify-email", verifyEmailRequest{Token: deps.verification.lastIssuedToken})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}

func TestVerifyEmailRejectsInvalidToken(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/verify-email", verifyEmailRequest{Token: "token-que-nao-existe"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestVerifyEmailRejectsMissingToken(t *testing.T) {
	deps := newTestDeps()
	rec := doRequest(t, deps.handler.Routes(), http.MethodPost, "/v1/verify-email", verifyEmailRequest{Token: ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestResendVerificationAlwaysAccepted(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "senha-forte"})

	knownEmail := doRequest(t, mux, http.MethodPost, "/v1/verify-email/resend", resendVerificationRequest{Email: "ana@example.test"})
	unknownEmail := doRequest(t, mux, http.MethodPost, "/v1/verify-email/resend", resendVerificationRequest{Email: "ninguem@example.test"})

	// Mesma resposta pros dois casos -- não dá pra saber, por este
	// endpoint, se um e-mail está registado ou não.
	if knownEmail.Code != http.StatusAccepted || unknownEmail.Code != http.StatusAccepted {
		t.Fatalf("status codes = %d, %d, want 202, 202", knownEmail.Code, unknownEmail.Code)
	}
}

func TestResendVerificationIssuesNewTokenForUnverifiedAccount(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	registerRec := doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@example.test", Password: "senha-forte"})
	created := decodeBody[registerResponse](t, registerRec)

	firstToken := deps.verification.lastIssuedToken
	doRequest(t, mux, http.MethodPost, "/v1/verify-email/resend", resendVerificationRequest{Email: "ana@example.test"})

	if deps.verification.lastIssuedUser != created.UserID {
		t.Fatalf("resend não emitiu token para o utilizador certo: got %q", deps.verification.lastIssuedUser)
	}
	if deps.verification.lastIssuedToken == firstToken {
		t.Fatal("resend devia ter emitido um token novo")
	}
}
