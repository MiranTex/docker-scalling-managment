package httpapi

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"auth/internal/mfa"
	"auth/internal/store"
)

// fakeMFA guarda o 2FA em memória.
type fakeMFA struct {
	mu       sync.Mutex
	totp     map[string]store.TOTP
	recovery map[string]map[string]bool // user -> hash -> usado
}

func newFakeMFA() *fakeMFA {
	return &fakeMFA{totp: map[string]store.TOTP{}, recovery: map[string]map[string]bool{}}
}

func (f *fakeMFA) GetTOTP(_ context.Context, id string) (store.TOTP, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.totp[id]
	return t, ok, nil
}
func (f *fakeMFA) StartTOTP(_ context.Context, id, enc string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.totp[id]; ok && t.Confirmed {
		return nil
	}
	f.totp[id] = store.TOTP{SecretEnc: enc}
	return nil
}
func (f *fakeMFA) ConfirmTOTP(_ context.Context, id string, s int64, hashes []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.totp[id]
	t.Confirmed, t.LastStep = true, s
	f.totp[id] = t
	f.recovery[id] = map[string]bool{}
	for _, h := range hashes {
		f.recovery[id][h] = false
	}
	return nil
}
func (f *fakeMFA) UseTOTPStep(_ context.Context, id string, s int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.totp[id]
	if s <= t.LastStep {
		return false, nil
	}
	t.LastStep = s
	f.totp[id] = t
	return true, nil
}
func (f *fakeMFA) UseRecoveryCode(_ context.Context, id, h string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	used, ok := f.recovery[id][h]
	if !ok || used {
		return false, nil
	}
	f.recovery[id][h] = true
	return true, nil
}
func (f *fakeMFA) ReplaceRecoveryCodes(_ context.Context, id string, hashes []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recovery[id] = map[string]bool{}
	for _, h := range hashes {
		f.recovery[id][h] = false
	}
	return nil
}
func (f *fakeMFA) RemainingRecoveryCodes(_ context.Context, id string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, used := range f.recovery[id] {
		if !used {
			n++
		}
	}
	return n, nil
}
func (f *fakeMFA) DisableTOTP(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.totp, id)
	delete(f.recovery, id)
	return nil
}
func (f *fakeMFA) MFAEnabledUsers(context.Context) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for id, t := range f.totp {
		out[id] = t.Confirmed
	}
	return out, nil
}

func mfaDeps(t *testing.T) (*testDeps, http.Handler) {
	t.Helper()
	deps := newTestDeps()
	box, err := mfa.NewBox("MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	if err != nil {
		t.Fatal(err)
	}
	deps.handler.WithMFA(newFakeMFA(), box, "base-stack")
	return deps, deps.handler.Routes()
}

// O admin sem 2FA é obrigado a ativá-lo para entrar; depois, cada login
// pede o código -- que só serve uma vez; um código de recuperação também.
func TestAdminMustEnableMFAAndThenUseIt(t *testing.T) {
	deps, mux := mfaDeps(t)
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "admin@x.pt", Password: "senha-forte"})
	deps.users.setRole("admin@x.pt", store.RoleSuperAdmin)

	rec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "admin@x.pt", Password: "senha-forte"})
	first := decodeBody[map[string]any](t, rec)
	if first["mfa_setup_required"] != true || first["access_token"] != nil {
		t.Fatalf("admin sem 2FA entrou direto: %v", first)
	}
	setupTok := first["mfa_token"].(string)
	// O token de ativação não serve para o passo do código.
	if rec := doRequest(t, mux, http.MethodPost, "/v1/login/mfa", mfaLoginRequest{MFAToken: setupTok, Code: "000000"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("token de ativação usado como de verificação: %d", rec.Code)
	}
	setup := decodeBody[map[string]string](t, doRequest(t, mux, http.MethodPost, "/v1/login/mfa/setup", mfaTokenRequest{MFAToken: setupTok}))
	secret := setup["secret"]
	if secret == "" || setup["otpauth_uri"] == "" {
		t.Fatalf("setup = %v", setup)
	}
	if rec := doRequest(t, mux, http.MethodPost, "/v1/login/mfa/confirm", mfaLoginRequest{MFAToken: setupTok, Code: "123456"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("código errado na ativação: %d", rec.Code)
	}
	now := time.Now()
	code, _ := mfa.Code(secret, now)
	confirmed := decodeBody[map[string]any](t, doRequest(t, mux, http.MethodPost, "/v1/login/mfa/confirm", mfaLoginRequest{MFAToken: setupTok, Code: code}))
	recovery, _ := confirmed["recovery_codes"].([]any)
	if confirmed["access_token"] == nil || len(recovery) != recoveryCodeCount {
		t.Fatalf("ativação: %v", confirmed)
	}

	// Login seguinte: pede o código.
	second := decodeBody[map[string]any](t, doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "admin@x.pt", Password: "senha-forte"}))
	if second["mfa_required"] != true {
		t.Fatalf("segundo login: %v", second)
	}
	verifyTok := second["mfa_token"].(string)
	// O código usado na ativação já não serve (o mesmo passo de 30s).
	if rec := doRequest(t, mux, http.MethodPost, "/v1/login/mfa", mfaLoginRequest{MFAToken: verifyTok, Code: code}); rec.Code != http.StatusUnauthorized {
		t.Errorf("código reutilizado: %d", rec.Code)
	}
	// Um código de recuperação entra -- uma vez.
	rc := recovery[0].(string)
	if rec := doRequest(t, mux, http.MethodPost, "/v1/login/mfa", mfaLoginRequest{MFAToken: verifyTok, Code: rc}); rec.Code != http.StatusOK {
		t.Fatalf("código de recuperação: %d %s", rec.Code, rec.Body)
	}
	third := decodeBody[map[string]any](t, doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "admin@x.pt", Password: "senha-forte"}))
	if rec := doRequest(t, mux, http.MethodPost, "/v1/login/mfa", mfaLoginRequest{MFAToken: third["mfa_token"].(string), Code: rc}); rec.Code != http.StatusUnauthorized {
		t.Errorf("código de recuperação reutilizado: %d", rec.Code)
	}
	// Um token de verificação gasta-se ao entrar.
	if rec := doRequest(t, mux, http.MethodPost, "/v1/login/mfa", mfaLoginRequest{MFAToken: verifyTok, Code: rc}); rec.Code != http.StatusUnauthorized {
		t.Errorf("token de verificação reutilizado: %d", rec.Code)
	}
}

// Um utilizador normal entra sem 2FA, pode ativá-lo e desligá-lo (com a
// password); um admin não o pode desligar; um super-admin desliga o de
// outra conta.
func TestOptionalMFAForUsersAndAdminReset(t *testing.T) {
	deps, mux := mfaDeps(t)
	user := loginAndGetAccessToken(t, mux, "ana@x.pt") // entra direto: 2FA opcional
	setup := decodeBody[map[string]string](t, doAuthedRequest(t, mux, http.MethodPost, "/v1/me/mfa/setup", user, nil))
	code, _ := mfa.Code(setup["secret"], time.Now())
	if rec := doAuthedRequest(t, mux, http.MethodPost, "/v1/me/mfa/confirm", user, codeRequest{Code: code}); rec.Code != http.StatusOK {
		t.Fatalf("ativar: %d %s", rec.Code, rec.Body)
	}
	if st := decodeBody[map[string]any](t, doAuthedRequest(t, mux, http.MethodGet, "/v1/me/mfa", user, nil)); st["enabled"] != true || st["required"] != false {
		t.Errorf("estado: %v", st)
	}
	if rec := doAuthedRequest(t, mux, http.MethodDelete, "/v1/me/mfa", user, disableMFARequest{Password: "errada"}); rec.Code != http.StatusForbidden {
		t.Errorf("desligar com a password errada: %d", rec.Code)
	}
	if rec := doAuthedRequest(t, mux, http.MethodDelete, "/v1/me/mfa", user, disableMFARequest{Password: "senha-forte"}); rec.Code != http.StatusNoContent {
		t.Errorf("desligar: %d", rec.Code)
	}

	admin := loginAndGetAccessTokenWithRole(t, mux, deps.users, "boss@x.pt", store.RoleUser)
	deps.users.setRole("boss@x.pt", store.RoleSuperAdmin)
	if rec := doAuthedRequest(t, mux, http.MethodDelete, "/v1/me/mfa", admin, disableMFARequest{Password: "senha-forte"}); rec.Code != http.StatusForbidden {
		t.Errorf("admin a desligar o próprio 2FA: %d", rec.Code)
	}
	adminTok := loginAndGetAccessTokenWithRole(t, mux, deps.users, "root@x.pt", store.RoleSuperAdmin)
	_ = adminTok
	ana, _, _ := deps.users.FindUserByEmail(t.Context(), "ana@x.pt")
	if rec := doAuthedRequest(t, mux, http.MethodDelete, "/v1/admin/users/"+ana.ID+"/mfa", user, nil); rec.Code != http.StatusForbidden {
		t.Errorf("utilizador normal a desligar 2FA de outro: %d", rec.Code)
	}
}

// Sem chave, o 2FA está desligado: um admin entra só com a password.
func TestNoMFAKeyMeansNoMFA(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "admin@x.pt", Password: "senha-forte"})
	deps.users.setRole("admin@x.pt", store.RoleSuperAdmin)
	body := decodeBody[map[string]any](t, doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "admin@x.pt", Password: "senha-forte"}))
	if body["access_token"] == nil {
		t.Errorf("sem chave de 2FA, o admin devia entrar: %v", body)
	}
}

// Com o 2FA opcional (ex: uma instalação de desenvolvimento), o admin entra
// sem o ativar e pode desligá-lo como qualquer conta.
func TestMFAOptionalLetsAdminsIn(t *testing.T) {
	deps, _ := mfaDeps(t)
	mux := deps.handler.WithMFAOptional().Routes()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "admin@x.pt", Password: "senha-forte"})
	deps.users.setRole("admin@x.pt", store.RoleSuperAdmin)
	body := decodeBody[map[string]any](t, doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "admin@x.pt", Password: "senha-forte"}))
	if body["access_token"] == nil || body["mfa_setup_required"] != nil {
		t.Errorf("com o 2FA opcional, o admin devia entrar: %v", body)
	}
}
