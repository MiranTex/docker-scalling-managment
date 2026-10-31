package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"auth/internal/mfa"
	"auth/internal/password"
	"auth/internal/store"
)

// Segundo fator (TOTP). Com a password certa, o login não emite tokens a
// quem tem 2FA: devolve um mfa_token curto que só serve para enviar o
// código (POST /v1/login/mfa). Os administradores da plataforma (super- e
// infra-admin) são obrigados a tê-lo: sem ele, o mfa_token só serve para o
// ativar (POST /v1/login/mfa/setup e /confirm) -- e só então entram.
//
// Sem chave (AUTH_MFA_KEY) o 2FA está desligado: nada disto se aplica e
// nada é obrigatório (melhor do que trancar os admins por configuração).

// MFAStore é a persistência do 2FA -- implementada por *store.DB.
type MFAStore interface {
	GetTOTP(ctx context.Context, userID string) (store.TOTP, bool, error)
	StartTOTP(ctx context.Context, userID, secretEnc string) error
	ConfirmTOTP(ctx context.Context, userID string, usedStep int64, recoveryHashes []string) error
	UseTOTPStep(ctx context.Context, userID string, step int64) (bool, error)
	UseRecoveryCode(ctx context.Context, userID, codeHash string) (bool, error)
	ReplaceRecoveryCodes(ctx context.Context, userID string, hashes []string) error
	RemainingRecoveryCodes(ctx context.Context, userID string) (int, error)
	DisableTOTP(ctx context.Context, userID string) error
	MFAEnabledUsers(ctx context.Context) (map[string]bool, error)
}

const (
	recoveryCodeCount = 10
	mfaVerifyTTL      = 5 * time.Minute
	mfaSetupTTL       = 15 * time.Minute // ler o QR e escrever o código leva tempo
	purposeVerify     = "verify"
	purposeSetup      = "setup"
)

// WithMFA liga o 2FA. issuer é o nome que aparece na app autenticadora.
func (h *Handler) WithMFA(st MFAStore, box *mfa.Box, issuer string) *Handler {
	h.mfaStore, h.mfaBox, h.mfaIssuer = st, box, issuer
	return h
}

func (h *Handler) mfaOn() bool { return h.mfaStore != nil && h.mfaBox != nil }

// WithMFAOptional deixa o 2FA à escolha de cada um, administradores
// incluídos (ex: uma instalação de desenvolvimento).
func (h *Handler) WithMFAOptional() *Handler {
	h.mfaOptional = true
	return h
}

// mfaMandatory: os administradores da plataforma têm de ter 2FA (a não ser
// que a instalação o deixe opcional).
func (h *Handler) mfaMandatory(role string) bool {
	return !h.mfaOptional && (role == store.RoleSuperAdmin || role == store.RoleInfraAdmin)
}

// Os mfa_token vivem em memória: minutos, e um restart só obriga a voltar
// a pôr a password.
type mfaChallenge struct {
	userID, role, email, purpose string
	expires                      time.Time
}

type mfaChallenges struct {
	mu sync.Mutex
	m  map[string]mfaChallenge
}

func (c *mfaChallenges) issue(u store.User, purpose string) string {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	tok := base64.RawURLEncoding.EncodeToString(raw)
	ttl := mfaVerifyTTL
	if purpose == purposeSetup {
		ttl = mfaSetupTTL
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	c.m[tok] = mfaChallenge{userID: u.ID, role: u.Role, email: u.Email, purpose: purpose, expires: now.Add(ttl)}
	return tok
}

func (c *mfaChallenges) get(tok, purpose string) (mfaChallenge, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.m[tok]
	if !ok || ch.purpose != purpose || time.Now().After(ch.expires) {
		return mfaChallenge{}, false
	}
	return ch, true
}

func (c *mfaChallenges) consume(tok string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, tok)
}

// mfaStep decide, depois da password certa, se o login precisa do 2FA.
// Devolve true se já respondeu (pedido de código ou de ativação).
func (h *Handler) mfaStep(w http.ResponseWriter, r *http.Request, u store.User) bool {
	if !h.mfaOn() {
		return false
	}
	t, found, err := h.mfaStore.GetTOTP(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro lendo o 2FA")
		return true
	}
	switch {
	case found && t.Confirmed:
		writeJSON(w, http.StatusOK, map[string]any{"mfa_required": true, "mfa_token": h.challenges.issue(u, purposeVerify)})
		return true
	case h.mfaMandatory(u.Role):
		writeJSON(w, http.StatusOK, map[string]any{"mfa_setup_required": true, "mfa_token": h.challenges.issue(u, purposeSetup)})
		return true
	}
	return false
}

type mfaLoginRequest struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"`
}

// handleLoginMFA: o segundo passo -- um código da app ou de recuperação.
func (h *Handler) handleLoginMFA(w http.ResponseWriter, r *http.Request) {
	var req mfaLoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, ok := h.challenges.get(req.MFAToken, purposeVerify)
	if !ok {
		writeError(w, http.StatusUnauthorized, "mfa_expired", "o pedido expirou: entra outra vez com a password")
		return
	}
	account := accountKey(ch.email)
	if wait := h.limiter.wait(account); wait > 0 {
		tooManyAttempts(w, wait)
		return
	}
	ok, err := h.checkSecondFactor(r.Context(), ch.userID, req.Code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro verificando o código")
		return
	}
	if !ok {
		h.limiter.fail(map[string]int{account: accountFreeFailures})
		writeError(w, http.StatusUnauthorized, "invalid_code", "código inválido")
		return
	}
	h.limiter.reset(account)
	h.challenges.consume(req.MFAToken)
	h.writeTokens(w, r, ch.userID, ch.role, nil)
}

// checkSecondFactor aceita um código TOTP (uma vez cada) ou um código de
// recuperação (uma vez cada).
func (h *Handler) checkSecondFactor(ctx context.Context, userID, code string) (bool, error) {
	t, found, err := h.mfaStore.GetTOTP(ctx, userID)
	if err != nil || !found || !t.Confirmed {
		return false, err
	}
	secret, err := h.mfaBox.Open(t.SecretEnc)
	if err != nil {
		return false, err
	}
	if s, ok := mfa.Verify(secret, code, time.Now(), t.LastStep); ok {
		return h.mfaStore.UseTOTPStep(ctx, userID, s)
	}
	if len(code) > 6 {
		return h.mfaStore.UseRecoveryCode(ctx, userID, mfa.HashRecovery(code))
	}
	return false, nil
}

func (h *Handler) writeTokens(w http.ResponseWriter, r *http.Request, userID, role string, recovery []string) {
	pair, err := h.issueTokenPair(r.Context(), userID, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro emitindo tokens")
		return
	}
	if recovery == nil {
		writeJSON(w, http.StatusOK, pair)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": pair.AccessToken, "refresh_token": pair.RefreshToken, "token_type": pair.TokenType,
		"expires_in": pair.ExpiresIn, "recovery_codes": recovery,
	})
}

// startSetup cria (ou recria) o segredo por confirmar e devolve o que a
// app precisa de ler.
func (h *Handler) startSetup(w http.ResponseWriter, r *http.Request, userID, email string) {
	secret, err := mfa.NewSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro gerando o segredo")
		return
	}
	enc, err := h.mfaBox.Seal(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro guardando o segredo")
		return
	}
	if err := h.mfaStore.StartTOTP(r.Context(), userID, enc); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro guardando o segredo")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_uri": mfa.URI(h.mfaIssuer, email, secret)})
}

// confirmSetup ativa o 2FA se o código bater e devolve os códigos de
// recuperação (a única vez que se veem).
func (h *Handler) confirmSetup(ctx context.Context, userID, code string) ([]string, int, string, string) {
	t, found, err := h.mfaStore.GetTOTP(ctx, userID)
	switch {
	case err != nil:
		return nil, http.StatusInternalServerError, "internal_error", "erro lendo o 2FA"
	case !found:
		return nil, http.StatusBadRequest, "mfa_not_started", "começa por ler o código QR"
	case t.Confirmed:
		return nil, http.StatusConflict, "mfa_enabled", "o 2FA já está ativo"
	}
	secret, err := h.mfaBox.Open(t.SecretEnc)
	if err != nil {
		return nil, http.StatusInternalServerError, "internal_error", "erro lendo o 2FA"
	}
	s, ok := mfa.Verify(secret, code, time.Now(), 0)
	if !ok {
		return nil, http.StatusUnauthorized, "invalid_code", "o código não bate certo -- confirma a hora do telemóvel e tenta o seguinte"
	}
	codes, err := mfa.RecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, http.StatusInternalServerError, "internal_error", "erro gerando códigos de recuperação"
	}
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = mfa.HashRecovery(c)
	}
	if err := h.mfaStore.ConfirmTOTP(ctx, userID, s, hashes); err != nil {
		return nil, http.StatusInternalServerError, "internal_error", "erro ativando o 2FA"
	}
	return codes, 0, "", ""
}

type mfaTokenRequest struct {
	MFAToken string `json:"mfa_token"`
}

// handleLoginMFASetup: um admin sem 2FA, a meio do login, começa a ativá-lo.
func (h *Handler) handleLoginMFASetup(w http.ResponseWriter, r *http.Request) {
	var req mfaTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, ok := h.challenges.get(req.MFAToken, purposeSetup)
	if !ok {
		writeError(w, http.StatusUnauthorized, "mfa_expired", "o pedido expirou: entra outra vez com a password")
		return
	}
	h.startSetup(w, r, ch.userID, ch.email)
}

// handleLoginMFAConfirm: ... confirma com o primeiro código e entra.
func (h *Handler) handleLoginMFAConfirm(w http.ResponseWriter, r *http.Request) {
	var req mfaLoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, ok := h.challenges.get(req.MFAToken, purposeSetup)
	if !ok {
		writeError(w, http.StatusUnauthorized, "mfa_expired", "o pedido expirou: entra outra vez com a password")
		return
	}
	codes, status, code, msg := h.confirmSetup(r.Context(), ch.userID, req.Code)
	if status != 0 {
		writeError(w, status, code, msg)
		return
	}
	h.challenges.consume(req.MFAToken)
	h.writeTokens(w, r, ch.userID, ch.role, codes)
}

// A própria conta (com sessão).

func (h *Handler) mfaEnabledCheck(w http.ResponseWriter) bool {
	if !h.mfaOn() {
		writeError(w, http.StatusNotFound, "mfa_disabled", "o 2FA não está ligado nesta instalação")
		return false
	}
	return true
}

func (h *Handler) handleGetMyMFA(w http.ResponseWriter, r *http.Request, userID string) {
	if !h.mfaEnabledCheck(w) {
		return
	}
	u, found, err := h.users.FindUserByID(r.Context(), userID)
	if err != nil || !found {
		writeError(w, http.StatusUnauthorized, "unauthorized", "conta não encontrada")
		return
	}
	t, on, err := h.mfaStore.GetTOTP(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro lendo o 2FA")
		return
	}
	left := 0
	if on && t.Confirmed {
		left, _ = h.mfaStore.RemainingRecoveryCodes(r.Context(), userID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": on && t.Confirmed, "required": h.mfaMandatory(u.Role), "recovery_codes_left": left})
}

func (h *Handler) handleStartMyMFA(w http.ResponseWriter, r *http.Request, userID string) {
	if !h.mfaEnabledCheck(w) {
		return
	}
	u, found, err := h.users.FindUserByID(r.Context(), userID)
	if err != nil || !found {
		writeError(w, http.StatusUnauthorized, "unauthorized", "conta não encontrada")
		return
	}
	if t, on, _ := h.mfaStore.GetTOTP(r.Context(), userID); on && t.Confirmed {
		writeError(w, http.StatusConflict, "mfa_enabled", "o 2FA já está ativo")
		return
	}
	h.startSetup(w, r, userID, u.Email)
}

type codeRequest struct {
	Code string `json:"code"`
}

func (h *Handler) handleConfirmMyMFA(w http.ResponseWriter, r *http.Request, userID string) {
	if !h.mfaEnabledCheck(w) {
		return
	}
	var req codeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	codes, status, code, msg := h.confirmSetup(r.Context(), userID, req.Code)
	if status != 0 {
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// handleNewRecoveryCodes: códigos novos (os antigos deixam de servir),
// com um código da app.
func (h *Handler) handleNewRecoveryCodes(w http.ResponseWriter, r *http.Request, userID string) {
	if !h.mfaEnabledCheck(w) {
		return
	}
	var req codeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ok, err := h.checkSecondFactor(r.Context(), userID, req.Code)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid_code", "código inválido")
		return
	}
	codes, _ := mfa.RecoveryCodes(recoveryCodeCount)
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = mfa.HashRecovery(c)
	}
	if err := h.mfaStore.ReplaceRecoveryCodes(r.Context(), userID, hashes); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro gravando os códigos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

type disableMFARequest struct {
	Password string `json:"password"`
}

// handleDisableMyMFA: desligar o próprio 2FA, com a password -- não para
// quem é obrigado a tê-lo.
func (h *Handler) handleDisableMyMFA(w http.ResponseWriter, r *http.Request, userID string) {
	if !h.mfaEnabledCheck(w) {
		return
	}
	var req disableMFARequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u, found, err := h.users.FindUserByID(r.Context(), userID)
	if err != nil || !found {
		writeError(w, http.StatusUnauthorized, "unauthorized", "conta não encontrada")
		return
	}
	if h.mfaMandatory(u.Role) {
		writeError(w, http.StatusForbidden, "mfa_required", "o 2FA é obrigatório para administradores da plataforma")
		return
	}
	_, hash, err := h.users.FindUserByEmailWithPassword(r.Context(), u.Email)
	if err != nil || hash == "" || password.Verify(req.Password, hash) != nil {
		writeError(w, http.StatusForbidden, "wrong_password", "a password não está certa")
		return
	}
	if err := h.mfaStore.DisableTOTP(r.Context(), userID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro desligando o 2FA")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminDisableMFA: um super-admin desliga o 2FA de uma conta (ex:
// perdeu o telemóvel e os códigos); as sessões dela terminam. Se for
// obrigatória, a pessoa volta a ativá-lo no próximo login.
func (h *Handler) handleAdminDisableMFA(w http.ResponseWriter, r *http.Request, adminID string) {
	if !h.mfaEnabledCheck(w) {
		return
	}
	id := r.PathValue("id")
	if _, found, err := h.users.FindUserByID(r.Context(), id); err != nil || !found {
		writeError(w, http.StatusNotFound, "not_found", "utilizador não encontrado")
		return
	}
	if err := h.mfaStore.DisableTOTP(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro desligando o 2FA")
		return
	}
	_ = h.userAdmin.RevokeUserSessions(r.Context(), id, time.Now())
	h.logger.Info("2FA desligado por um administrador", "user", id, "admin", adminID)
	w.WriteHeader(http.StatusNoContent)
}
