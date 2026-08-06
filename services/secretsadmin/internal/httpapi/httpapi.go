// Package httpapi expõe a API de administração de segredos: nomes/valores
// usados pelos launch templates do autoscaler via referências
// ${secret:NOME} (ver services/autoscaler/internal/secretsclient). Dois
// níveis de acesso, nunca sobrepostos:
//   - RoleInfraAdmin (humanos, via portal): gerem NOMES -- criam, atualizam,
//     apagam, listam metadados. NUNCA conseguem ler um valor de volta depois
//     de o gravarem (não há GET .../value nenhum para esta role).
//   - RoleService (contas de máquina, ex: cmd/group do autoscaler): só
//     conseguem resolver nomes para valores (POST /v1/secrets/resolve),
//     nunca listar/criar/apagar.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"secretsadmin/internal/audit"
	"secretsadmin/internal/jwtverify"
	"secretsadmin/internal/store"
)

const (
	RoleInfraAdmin = "infra-admin"
	RoleService    = "service"
	// RoleSuperAdmin funciona como bypass universal em todo requireRole
	// desta API -- ver services/auth/internal/store/role.go, onde esse
	// comportamento é o mesmo em qualquer serviço de recursos. Inclui,
	// deliberadamente, os endpoints RoleService (resolve) -- um
	// super-admin humano consegue ler valores em claro, algo que
	// infra-admin NUNCA consegue; aceite como parte do bypass ser
	// universal, não seletivo por endpoint.
	RoleSuperAdmin = "super-admin"
)

// nameRe é o mesmo charset aceite dentro de ${secret:NOME} pelo
// autoscaler (ver internal/secretsclient) -- manter os dois em sincronia,
// senão um nome válido aqui nunca conseguiria ser referenciado lá.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type TokenVerifier interface {
	Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error)
}

// Store é o subconjunto de *store.DB usado por este pacote -- interface
// pequena para permitir testar os handlers com um fake em memória, sem
// Postgres nenhum.
type Store interface {
	List(ctx context.Context) ([]store.SecretInfo, error)
	Get(ctx context.Context, name string) (store.Secret, error)
	GetMany(ctx context.Context, names []string) (map[string]store.Secret, error)
	Upsert(ctx context.Context, name string, ciphertext, nonce []byte, updatedBy string) error
	Delete(ctx context.Context, name string) error
	Ping(ctx context.Context) error
}

// Sealer é o subconjunto de *crypto.Sealer usado aqui.
type Sealer interface {
	Seal(plaintext []byte) (ciphertext, nonce []byte, err error)
	Open(ciphertext, nonce []byte) ([]byte, error)
}

type Handler struct {
	tokens TokenVerifier
	store  Store
	seal   Sealer
	audit  *audit.Logger
}

func NewHandler(tokens TokenVerifier, s Store, seal Sealer, auditLogger *audit.Logger) *Handler {
	return &Handler{tokens: tokens, store: s, seal: seal, audit: auditLogger}
}

func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/secrets", h.requireRole(RoleInfraAdmin, h.handleList))
	mux.HandleFunc("PUT /v1/secrets/{name}", h.requireRole(RoleInfraAdmin, h.handleUpsert))
	mux.HandleFunc("DELETE /v1/secrets/{name}", h.requireRole(RoleInfraAdmin, h.handleDelete))
	mux.HandleFunc("POST /v1/secrets/resolve", h.requireRole(RoleService, h.handleResolve))
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("GET /readyz", h.handleReadyz)
	return mux
}

var errMissingToken = errors.New("httpapi: cabeçalho Authorization ausente")

func (h *Handler) bearerClaims(r *http.Request) (jwtverify.Claims, error) {
	const prefix = "Bearer "
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, prefix) {
		return jwtverify.Claims{}, errMissingToken
	}
	return h.tokens.Verify(r.Context(), strings.TrimPrefix(authHeader, prefix))
}

func (h *Handler) requireRole(role string, next func(w http.ResponseWriter, r *http.Request, subject string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := h.bearerClaims(r)
		if errors.Is(err, errMissingToken) {
			writeError(w, http.StatusUnauthorized, "missing_token", "cabeçalho Authorization: Bearer <token> é obrigatório")
			return
		}
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_token", "access token inválido ou expirado")
			return
		}
		if claims.Role != role && claims.Role != RoleSuperAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "esta ação exige a role "+role)
			return
		}
		next(w, r, claims.Subject)
	}
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request, _ string) {
	secrets, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro listando segredos")
		return
	}
	writeJSON(w, http.StatusOK, secrets)
}

type upsertRequest struct {
	Value string `json:"value"`
}

// handleUpsert cria ou substitui o valor de um segredo. O valor em claro
// só existe em memória durante este pedido (decodificado do JSON, cifrado,
// e descartado) -- nunca é escrito em log nenhum, incluindo o audit log
// (que regista a ação e o nome, nunca o valor).
func (h *Handler) handleUpsert(w http.ResponseWriter, r *http.Request, subject string) {
	name := r.PathValue("name")
	if !nameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "nome inválido -- use só letras, números, \".\", \"_\" ou \"-\", até 128 caracteres")
		return
	}

	var req upsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido, esperado {\"value\": \"...\"}")
		return
	}
	if req.Value == "" {
		writeError(w, http.StatusBadRequest, "empty_value", "\"value\" não pode ser vazio")
		return
	}

	ciphertext, nonce, err := h.seal.Seal([]byte(req.Value))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro cifrando valor")
		return
	}

	if err := h.store.Upsert(r.Context(), name, ciphertext, nonce, subject); err != nil {
		h.audit.Log(subject, "secret.upsert", name, "falhou: "+err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "erro gravando segredo")
		return
	}

	h.audit.Log(subject, "secret.upsert", name, "aplicado")
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, subject string) {
	name := r.PathValue("name")
	err := h.store.Delete(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "segredo desconhecido: "+name)
		return
	}
	if err != nil {
		h.audit.Log(subject, "secret.delete", name, "falhou: "+err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "erro apagando segredo")
		return
	}

	h.audit.Log(subject, "secret.delete", name, "aplicado")
	w.WriteHeader(http.StatusNoContent)
}

type resolveRequest struct {
	Names []string `json:"names"`
}

type resolveResponse struct {
	Values map[string]string `json:"values"`
}

// handleResolve é o único caminho que devolve valores em claro -- só para
// RoleService (contas de máquina), nunca para infra-admin. Falha rápido
// (404) no primeiro nome desconhecido: quem chama (o autoscaler) trata
// isso como erro de scale-up e tenta de novo no próximo tick, depois de
// alguém criar o segredo em falta.
func (h *Handler) handleResolve(w http.ResponseWriter, r *http.Request, subject string) {
	var req resolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido, esperado {\"names\": [...]}")
		return
	}
	if len(req.Names) == 0 {
		writeJSON(w, http.StatusOK, resolveResponse{Values: map[string]string{}})
		return
	}

	secrets, err := h.store.GetMany(r.Context(), req.Names)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro lendo segredos")
		return
	}

	values := make(map[string]string, len(req.Names))
	for _, name := range req.Names {
		s, ok := secrets[name]
		if !ok {
			h.audit.Log(subject, "secret.resolve", name, "rejeitado: desconhecido")
			writeError(w, http.StatusNotFound, "secret_not_found", "segredo desconhecido: "+name)
			return
		}
		plaintext, err := h.seal.Open(s.Ciphertext, s.Nonce)
		if err != nil {
			h.audit.Log(subject, "secret.resolve", name, "falhou: erro decifrando")
			writeError(w, http.StatusInternalServerError, "internal_error", "erro decifrando segredo: "+name)
			return
		}
		values[name] = string(plaintext)
	}

	h.audit.Log(subject, "secret.resolve", strings.Join(req.Names, ","), "concedido")
	writeJSON(w, http.StatusOK, resolveResponse{Values: values})
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
