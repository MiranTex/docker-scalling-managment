// Package httpapi expõe a API de administração de modelos de serviço: o
// launch template (imagem, cmd, env, labels, binds, network, extraHosts
// -- ver services/autoscaler/internal/executor.LaunchTemplate) de um
// container de aplicação, nada mais. NÃO inclui config de autoscaling
// (réplicas, thresholds de CPU, target service, portas de host) -- essa
// config passou a ser preenchida no momento de CRIAR um group a partir
// de um modelo, não a viver junto dele (ver services/launcher/internal/httpapi,
// POST /v1/instances com kind="group"). Só role infra-admin (humanos, via
// portal); não há role "service" aqui -- ao contrário do secretsadmin,
// nada neste serviço é segredo, então não faz sentido nenhuma conta de
// máquina "resolver" um modelo em runtime.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"templatesadmin/internal/audit"
	"templatesadmin/internal/jwtverify"
	"templatesadmin/internal/store"
)

const (
	RoleInfraAdmin = "infra-admin"
	// RoleSuperAdmin funciona como bypass universal em todo requireRole
	// desta API -- mesmo comportamento de services/secretsadmin,
	// services/database/dbadmin e da API admin do autoscaler.
	RoleSuperAdmin = "super-admin"
)

// nameRe é o charset aceite para o nome de um modelo -- mesmo padrão de
// services/secretsadmin/internal/httpapi (nome usado em caminhos de URL).
var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type TokenVerifier interface {
	Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error)
}

// Store é o subconjunto de *store.DB usado por este pacote -- interface
// pequena para permitir testar os handlers com um fake em memória.
type Store interface {
	List(ctx context.Context) ([]store.Template, error)
	Get(ctx context.Context, name string) (store.Template, error)
	Upsert(ctx context.Context, t store.Template, updatedBy string) error
	Delete(ctx context.Context, name string) error
	ListInstanceTypes(ctx context.Context) ([]store.InstanceType, error)
	GetInstanceType(ctx context.Context, name string) (store.InstanceType, error)
	UpsertInstanceType(ctx context.Context, it store.InstanceType, updatedBy string) error
	DeleteInstanceType(ctx context.Context, name string) error
	Ping(ctx context.Context) error
}

// ImageLister é o subconjunto do cliente Docker usado por este pacote --
// só listagem, nunca criação/remoção de containers ou imagens.
type ImageLister interface {
	ListImages(ctx context.Context) ([]string, error)
}

type Handler struct {
	tokens TokenVerifier
	store  Store
	images ImageLister
	audit  *audit.Logger
}

func NewHandler(tokens TokenVerifier, s Store, images ImageLister, auditLogger *audit.Logger) *Handler {
	return &Handler{tokens: tokens, store: s, images: images, audit: auditLogger}
}

func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/templates", h.requireRole(RoleInfraAdmin, h.handleList))
	mux.HandleFunc("GET /v1/templates/{name}", h.requireRole(RoleInfraAdmin, h.handleGet))
	mux.HandleFunc("PUT /v1/templates/{name}", h.requireRole(RoleInfraAdmin, h.handleUpsert))
	mux.HandleFunc("DELETE /v1/templates/{name}", h.requireRole(RoleInfraAdmin, h.handleDelete))
	mux.HandleFunc("GET /v1/instance-types", h.requireRole(RoleInfraAdmin, h.handleListInstanceTypes))
	mux.HandleFunc("GET /v1/instance-types/{name}", h.requireRole(RoleInfraAdmin, h.handleGetInstanceType))
	mux.HandleFunc("PUT /v1/instance-types/{name}", h.requireRole(RoleInfraAdmin, h.handleUpsertInstanceType))
	mux.HandleFunc("DELETE /v1/instance-types/{name}", h.requireRole(RoleInfraAdmin, h.handleDeleteInstanceType))
	mux.HandleFunc("GET /v1/images", h.requireRole(RoleInfraAdmin, h.handleListImages))
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
	templates, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro listando modelos")
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, _ string) {
	name := r.PathValue("name")
	t, err := h.store.Get(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "modelo desconhecido: "+name)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro lendo modelo")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleUpsert cria ou substitui um modelo de serviço. O corpo é o
// store.Template inteiro (menos os campos geridos pelo servidor: name
// vem do path, created_at/updated_at/updated_by são calculados aqui).
func (h *Handler) handleUpsert(w http.ResponseWriter, r *http.Request, subject string) {
	name := r.PathValue("name")
	if !nameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "nome inválido -- use só letras, números, \".\", \"_\" ou \"-\", até 128 caracteres")
		return
	}

	var t store.Template
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido: "+err.Error())
		return
	}
	t.Name = name

	if t.Image == "" {
		writeError(w, http.StatusBadRequest, "empty_image", "\"image\" não pode ser vazio")
		return
	}

	if err := h.store.Upsert(r.Context(), t, subject); err != nil {
		h.audit.Log(subject, "template.upsert", name, "falhou: "+err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "erro gravando modelo")
		return
	}

	h.audit.Log(subject, "template.upsert", name, "aplicado")
	saved, err := h.store.Get(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"name": name})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, subject string) {
	name := r.PathValue("name")
	err := h.store.Delete(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "modelo desconhecido: "+name)
		return
	}
	if err != nil {
		h.audit.Log(subject, "template.delete", name, "falhou: "+err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "erro apagando modelo")
		return
	}

	h.audit.Log(subject, "template.delete", name, "aplicado")
	w.WriteHeader(http.StatusNoContent)
}

// handleListInstanceTypes devolve o catálogo de tamanhos. Inclui os
// desativados -- é este ecrã que os volta a ligar; quem apresenta um
// <select> de escolha é que filtra por "enabled".
func (h *Handler) handleListInstanceTypes(w http.ResponseWriter, r *http.Request, _ string) {
	types, err := h.store.ListInstanceTypes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro listando tipos de instância")
		return
	}
	writeJSON(w, http.StatusOK, types)
}

func (h *Handler) handleGetInstanceType(w http.ResponseWriter, r *http.Request, _ string) {
	name := r.PathValue("name")
	it, err := h.store.GetInstanceType(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "tipo de instância desconhecido: "+name)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro lendo tipo de instância")
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (h *Handler) handleUpsertInstanceType(w http.ResponseWriter, r *http.Request, subject string) {
	name := r.PathValue("name")
	if !nameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "nome inválido -- use só letras, números, \".\", \"_\" ou \"-\", até 128 caracteres")
		return
	}

	// Enabled default true: o corpo típico do portal ao criar um tipo não
	// traz a chave, e o zero value de bool diria o contrário do esperado.
	it := store.InstanceType{Enabled: true}
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido: "+err.Error())
		return
	}
	it.Name = name

	if it.VCPU <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_vcpu", "\"vcpu\" tem de ser maior que zero")
		return
	}
	// Limite duro do Docker: abaixo de 6 MiB o daemon recusa o container.
	if it.MemoryMB < 6 {
		writeError(w, http.StatusBadRequest, "invalid_memory", "\"memoryMb\" tem de ser pelo menos 6")
		return
	}
	if it.MemorySwapMB != nil && *it.MemorySwapMB < it.MemoryMB {
		writeError(w, http.StatusBadRequest, "invalid_memory_swap", "\"memorySwapMb\" não pode ser menor que \"memoryMb\"")
		return
	}
	if it.PidsLimit < 0 {
		writeError(w, http.StatusBadRequest, "invalid_pids_limit", "\"pidsLimit\" não pode ser negativo")
		return
	}

	if err := h.store.UpsertInstanceType(r.Context(), it, subject); err != nil {
		h.audit.Log(subject, "instance_type.upsert", name, "falhou: "+err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "erro gravando tipo de instância")
		return
	}

	h.audit.Log(subject, "instance_type.upsert", name, "aplicado")
	saved, err := h.store.GetInstanceType(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"name": name})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) handleDeleteInstanceType(w http.ResponseWriter, r *http.Request, subject string) {
	name := r.PathValue("name")
	err := h.store.DeleteInstanceType(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "tipo de instância desconhecido: "+name)
		return
	}
	if errors.Is(err, store.ErrInstanceTypeInUse) {
		writeError(w, http.StatusConflict, "in_use", "há modelos que usam "+name+" como tipo por omissão -- altere-os primeiro")
		return
	}
	if err != nil {
		h.audit.Log(subject, "instance_type.delete", name, "falhou: "+err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "erro apagando tipo de instância")
		return
	}

	h.audit.Log(subject, "instance_type.delete", name, "aplicado")
	w.WriteHeader(http.StatusNoContent)
}

// handleListImages devolve as imagens já conhecidas pelo daemon Docker
// local (buildadas com `docker build` ou já puxadas de um registry) --
// é daqui que a UI popula o <select> de imagem ao criar/editar um
// modelo, em vez de o infra-admin ter de adivinhar/digitar o nome exato.
func (h *Handler) handleListImages(w http.ResponseWriter, r *http.Request, subject string) {
	images, err := h.images.ListImages(r.Context())
	if err != nil {
		h.audit.Log(subject, "images.list", "", "falhou: "+err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "erro listando imagens do Docker")
		return
	}
	writeJSON(w, http.StatusOK, images)
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
