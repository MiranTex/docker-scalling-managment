// Package httpapi expõe o serviço de auth como API HTTP: registo, login,
// refresh, logout, JWKS e health/readiness. Depende só de interfaces
// pequenas (UserStore, TokenIssuer, RefreshIssuer, Pinger) — nos testes
// deste pacote, cada uma tem um fake em memória, então os handlers são
// testados sem precisar de Postgres nem de chaves RSA reais.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"auth/internal/apikey"
	"auth/internal/password"
	"auth/internal/refresh"
	"auth/internal/store"
	"auth/internal/token"
)

// UserStore é a persistência de contas necessária pelos handlers —
// implementada por *store.DB em produção.
type UserStore interface {
	CreateUserWithPassword(ctx context.Context, email, passwordHash string) (store.User, error)
	FindUserByEmailWithPassword(ctx context.Context, email string) (store.User, string, error)
}

// TokenIssuer emite, valida e expõe as chaves de access tokens (JWT) —
// implementado por *token.Manager.
type TokenIssuer interface {
	Sign(subject string) (string, error)
	Verify(tokenString string) (token.Claims, error)
	JWKS() token.JWKSDocument
}

// APIKeyIssuer gere o ciclo de vida das API keys (autenticação
// máquina-a-máquina) — implementado por *apikey.Manager.
type APIKeyIssuer interface {
	Issue(ctx context.Context, owner string, scopes []string, ttl *time.Duration) (plaintext string, key apikey.Key, err error)
	Verify(ctx context.Context, presented string) (apikey.Key, error)
	Revoke(ctx context.Context, id, owner string) error
}

// RefreshIssuer gere o ciclo de vida dos refresh tokens — implementado por
// *refresh.Manager.
type RefreshIssuer interface {
	Issue(ctx context.Context, userID string) (string, error)
	Rotate(ctx context.Context, presented string) (newPlaintext, userID string, err error)
	Revoke(ctx context.Context, presented string) error
}

// Pinger confirma que uma dependência externa (a base de dados) está
// alcançável — usado só pelo /readyz.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Handler agrupa as dependências de todos os endpoints.
type Handler struct {
	users     UserStore
	tokens    TokenIssuer
	refresh   RefreshIssuer
	apiKeys   APIKeyIssuer
	db        Pinger
	accessTTL time.Duration
}

// NewHandler monta o Handler. accessTTL é usado só para reportar
// "expires_in" nas respostas — a validade de verdade do token já está
// embutida no próprio JWT (claim exp), calculada por quem assina.
func NewHandler(users UserStore, tokens TokenIssuer, refreshTokens RefreshIssuer, apiKeys APIKeyIssuer, db Pinger, accessTTL time.Duration) *Handler {
	return &Handler{users: users, tokens: tokens, refresh: refreshTokens, apiKeys: apiKeys, db: db, accessTTL: accessTTL}
}

// Routes monta o mux com todos os endpoints deste serviço.
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/register", h.handleRegister)
	mux.HandleFunc("POST /v1/login", h.handleLogin)
	mux.HandleFunc("POST /v1/token/refresh", h.handleRefresh)
	mux.HandleFunc("POST /v1/logout", h.handleLogout)
	mux.HandleFunc("POST /v1/api-keys", h.requireAuth(h.handleCreateAPIKey))
	mux.HandleFunc("DELETE /v1/api-keys/{id}", h.requireAuth(h.handleRevokeAPIKey))
	// Introspecção não exige um access token do chamador -- é chamado por
	// OUTRO serviço, de posse só da API key que quer validar, não de uma
	// sessão de utilizador. Ver limitação na documentação: hoje qualquer
	// chamador na rede consegue introspectar; restrinja isto à rede
	// interna (o serviço que consome a API key), nunca exponha ao público.
	mux.HandleFunc("POST /v1/api-keys/introspect", h.handleIntrospectAPIKey)
	mux.HandleFunc("GET /.well-known/jwks.json", h.handleJWKS)
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("GET /readyz", h.handleReadyz)
	return mux
}

// requireAuth envolve um handler que precisa saber QUEM está a chamar:
// exige um access token válido no cabeçalho Authorization e passa o
// "sub" (user ID) do token adiante -- é assim que uma API key criada em
// POST /v1/api-keys fica associada a quem a criou.
func (h *Handler) requireAuth(next func(w http.ResponseWriter, r *http.Request, userID string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, prefix) {
			writeError(w, http.StatusUnauthorized, "missing_token", "cabeçalho Authorization: Bearer <token> é obrigatório")
			return
		}

		claims, err := h.tokens.Verify(strings.TrimPrefix(authHeader, prefix))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_token", "access token inválido ou expirado")
			return
		}
		next(w, r, claims.Subject)
	}
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func (h *Handler) issueTokenPair(ctx context.Context, userID string) (tokenPair, error) {
	access, err := h.tokens.Sign(userID)
	if err != nil {
		return tokenPair{}, err
	}
	refreshTok, err := h.refresh.Issue(ctx, userID)
	if err != nil {
		return tokenPair{}, err
	}
	return tokenPair{
		AccessToken:  access,
		RefreshToken: refreshTok,
		TokenType:    "Bearer",
		ExpiresIn:    int(h.accessTTL.Seconds()),
	}, nil
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// minPasswordLength segue a recomendação do NIST SP 800-63B: comprimento
// mínimo em vez de regras artificiais de "precisa maiúscula/símbolo" (que
// a evidência mostra levar a senhas previsíveis, tipo "Senha123!").
const minPasswordLength = 8

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_email", "e-mail inválido")
		return
	}
	if len(req.Password) < minPasswordLength {
		writeError(w, http.StatusBadRequest, "weak_password", "senha precisa de pelo menos 8 caracteres")
		return
	}

	hash, err := password.Hash(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro processando senha")
		return
	}

	user, err := h.users.CreateUserWithPassword(r.Context(), req.Email, hash)
	if errors.Is(err, store.ErrEmailTaken) {
		writeError(w, http.StatusConflict, "email_taken", "e-mail já registado")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro criando utilizador")
		return
	}

	writeJSON(w, http.StatusCreated, registerResponse{UserID: user.ID, Email: user.Email})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	user, hash, err := h.users.FindUserByEmailWithPassword(r.Context(), req.Email)
	// Mesma mensagem/status para "utilizador não existe" e "senha errada"
	// -- de propósito. Distinguir os dois numa API pública deixa um
	// atacante enumerar e-mails registados (username enumeration).
	invalidCredentials := func() {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "e-mail ou senha inválidos")
	}
	if errors.Is(err, store.ErrUserNotFound) {
		invalidCredentials()
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro buscando utilizador")
		return
	}
	if err := password.Verify(req.Password, hash); err != nil {
		invalidCredentials()
		return
	}

	pair, err := h.issueTokenPair(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro emitindo tokens")
		return
	}
	writeJSON(w, http.StatusOK, pair)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "missing_refresh_token", "refresh_token é obrigatório")
		return
	}

	newRefresh, userID, err := h.refresh.Rotate(r.Context(), req.RefreshToken)
	if errors.Is(err, refresh.ErrReuseDetected) {
		// A sessão inteira já foi revogada pelo Rotate -- do ponto de
		// vista do cliente, o efeito é o mesmo de um token inválido; quem
		// operar o serviço é que deve monitorar ErrReuseDetected nos logs
		// como um evento de segurança.
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "refresh token inválido")
		return
	}
	if errors.Is(err, refresh.ErrInvalid) {
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "refresh token inválido")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro rodando refresh token")
		return
	}

	access, err := h.tokens.Sign(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro emitindo access token")
		return
	}

	writeJSON(w, http.StatusOK, tokenPair{
		AccessToken:  access,
		RefreshToken: newRefresh,
		TokenType:    "Bearer",
		ExpiresIn:    int(h.accessTTL.Seconds()),
	})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "missing_refresh_token", "refresh_token é obrigatório")
		return
	}

	if err := h.refresh.Revoke(r.Context(), req.RefreshToken); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro revogando sessão")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createAPIKeyRequest struct {
	Scopes     []string `json:"scopes"`
	TTLSeconds *int     `json:"ttl_seconds,omitempty"`
}

type apiKeyResponse struct {
	ID        string     `json:"id"`
	Key       string     `json:"key,omitempty"` // só vem preenchido na criação -- nunca mais dá pra recuperar depois
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (h *Handler) handleCreateAPIKey(w http.ResponseWriter, r *http.Request, userID string) {
	var req createAPIKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	var ttl *time.Duration
	if req.TTLSeconds != nil {
		d := time.Duration(*req.TTLSeconds) * time.Second
		ttl = &d
	}

	plaintext, key, err := h.apiKeys.Issue(r.Context(), userID, req.Scopes, ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro criando API key")
		return
	}

	writeJSON(w, http.StatusCreated, apiKeyResponse{ID: key.ID, Key: plaintext, Scopes: key.Scopes, ExpiresAt: key.ExpiresAt})
}

func (h *Handler) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request, userID string) {
	id := r.PathValue("id")
	err := h.apiKeys.Revoke(r.Context(), id, userID)
	if errors.Is(err, apikey.ErrNotFound) {
		// Mesma resposta tanto para "não existe" quanto para "existe mas é
		// de outro dono" -- não confirmamos a um chamador que um ID de
		// chave alheio existe.
		writeError(w, http.StatusNotFound, "not_found", "API key não encontrada")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro revogando API key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type introspectRequest struct {
	Key string `json:"key"`
}

type introspectResponse struct {
	Active bool     `json:"active"`
	Owner  string   `json:"owner,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
}

// handleIntrospectAPIKey é chamado por OUTRO serviço, de posse de uma API
// key, pra confirmar que ela é válida e descobrir o dono/scopes -- o
// equivalente, pra API keys, ao que a JWKS é para JWT: a forma de um
// terceiro validar uma credencial emitida por este serviço.
func (h *Handler) handleIntrospectAPIKey(w http.ResponseWriter, r *http.Request) {
	var req introspectRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	key, err := h.apiKeys.Verify(r.Context(), req.Key)
	if err != nil {
		// "active: false" em vez de 401/404 -- introspecção sempre
		// responde 200; é o campo "active" que carrega o resultado (mesmo
		// espírito do RFC 7662, ainda que sem seguir o formato à risca).
		writeJSON(w, http.StatusOK, introspectResponse{Active: false})
		return
	}
	writeJSON(w, http.StatusOK, introspectResponse{Active: true, Owner: key.Owner, Scopes: key.Scopes})
}

func (h *Handler) handleJWKS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.tokens.JWKS())
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "base de dados indisponível")
		return
	}
	w.WriteHeader(http.StatusOK)
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var resp errorResponse
	resp.Error.Code = code
	resp.Error.Message = message
	writeJSON(w, status, resp)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// decodeJSON decodifica o corpo JSON em dst; se falhar, já escreve a
// resposta de erro e devolve false -- o handler chamador só precisa dar
// return.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo da requisição não é JSON válido")
		return false
	}
	return true
}
