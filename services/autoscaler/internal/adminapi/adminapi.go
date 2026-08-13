// Package adminapi expõe a API de administração de uma instância do
// autoscaler group: estado atual, edição a quente da policy de scaling e
// restart lógico (termina e reconstrói as réplicas geridas, sem depender de
// o Docker reiniciar o container). Mesmo padrão de
// services/database/dbadmin/internal/httpapi: interfaces pequenas nas
// dependências, requireRole para os endpoints administrativos.
package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"autoscaler/internal/audit"
	"autoscaler/internal/jwtverify"
	"autoscaler/internal/scaler"
)

// RoleInfraAdmin é a única role aceite pelos endpoints administrativos --
// ver services/auth/internal/store/role.go, onde já existe explicitamente
// para "gestão de infraestrutura (ex: autoscaler)". Esta API é mais um
// consumidor dessa mesma role, não um novo conceito de autorização.
const RoleInfraAdmin = "infra-admin"

// RoleSuperAdmin funciona como bypass universal em todo requireRole desta
// API -- ver services/auth/internal/store/role.go, onde esse
// comportamento é o mesmo em qualquer serviço de recursos.
const RoleSuperAdmin = "super-admin"

// TokenVerifier valida o access token do auth service -- implementado por
// *jwtverify.Verifier.
type TokenVerifier interface {
	Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error)
}

// Replica é o estado observável de uma réplica gerida por este group, para
// GET /v1/status.
type Replica struct {
	ContainerID string  `json:"container_id"`
	Name        string  `json:"name"`
	State       string  `json:"state"`
	Status      string  `json:"status"`
	CPUPercent  float64 `json:"cpu_percent"`
}

// LaunchTemplateInfo descreve o launch-template atualmente em disco, sem
// expor o seu conteúdo (imagem/env/binds) por esta API -- só o suficiente
// para a UI saber se o ficheiro mudou desde o último restart.
type LaunchTemplateInfo struct {
	Path       string    `json:"path"`
	SHA256     string    `json:"sha256,omitempty"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
}

// ScaleAction é a última ação de scaling observada, para GET /v1/status.
type ScaleAction struct {
	Action string    `json:"action"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// Status é a resposta completa de GET /v1/status.
type Status struct {
	TargetService  string             `json:"target_service"`
	ReplicaCount   int                `json:"replica_count"`
	Replicas       []Replica          `json:"replicas"`
	Policy         PolicyDTO          `json:"policy"`
	LaunchTemplate LaunchTemplateInfo `json:"launch_template"`
	LastAction     *ScaleAction       `json:"last_scale_action,omitempty"`
	UptimeSeconds  float64            `json:"uptime_seconds"`
}

// GroupControl é a ponte para o estado vivo do processo cmd/group --
// implementada por um adaptador em cmd/group que fecha sobre as mesmas
// variáveis já usadas pelo reconcile loop. Definida como interface aqui
// (não em cmd/group) para adminapi não importar cmd/group, o que criaria um
// ciclo de imports (cmd/group importa adminapi).
type GroupControl interface {
	Status(ctx context.Context) (Status, error)
	Policy() scaler.Policy
	SetPolicy(p scaler.Policy) (scaler.Policy, error)
	Restart(ctx context.Context) error
	// AddReplica cria uma réplica extra imediatamente, fora do ciclo normal
	// do scaler (ex: pedido manual via UI) -- mesmo caminho que um scale up
	// automático usaria (local ou via launcher, ver cmd/group/main.go,
	// applyScaleUp), só que disparado a pedido em vez de por decisão do
	// scaler.Evaluator.
	AddReplica(ctx context.Context) (string, error)
	// RemoveReplica para e remove imediatamente UMA réplica específica,
	// escolhida por quem chama (não pelo critério de "menos carregada" que
	// SelectScaleDownTarget usaria num scale down automático).
	RemoveReplica(ctx context.Context, containerID string) error
}

// PolicyDTO é a policy exposta pela API, em snake_case e com ponteiros para
// distinguir "campo omitido" (não mexer) de "campo enviado como zero" (ex:
// cooldown_seconds:0 para desativar o cooldown) num PUT parcial.
type PolicyDTO struct {
	MinReplicas         *int     `json:"min_replicas,omitempty"`
	MaxReplicas         *int     `json:"max_replicas,omitempty"`
	CPUScaleUpPercent   *float64 `json:"cpu_scale_up_percent,omitempty"`
	CPUScaleDownPercent *float64 `json:"cpu_scale_down_percent,omitempty"`
	SustainedTicks      *int     `json:"sustained_ticks,omitempty"`
	CooldownSeconds     *int     `json:"cooldown_seconds,omitempty"`
}

// PolicyToDTO converte a policy interna (sempre totalmente preenchida) para
// a forma exposta pela API -- usado em respostas (GET, e o eco após um PUT
// bem-sucedido), nunca em pedidos.
func PolicyToDTO(p scaler.Policy) PolicyDTO {
	minR, maxR, up, down, sustained, cooldown := p.MinReplicas, p.MaxReplicas, p.CPUScaleUpPercent, p.CPUScaleDownPercent, p.SustainedTicks, int(p.Cooldown/time.Second)
	return PolicyDTO{
		MinReplicas:         &minR,
		MaxReplicas:         &maxR,
		CPUScaleUpPercent:   &up,
		CPUScaleDownPercent: &down,
		SustainedTicks:      &sustained,
		CooldownSeconds:     &cooldown,
	}
}

// applyTo devolve uma nova scaler.Policy com base em current, sobrepondo
// apenas os campos presentes em dto.
func (dto PolicyDTO) applyTo(current scaler.Policy) scaler.Policy {
	next := current
	if dto.MinReplicas != nil {
		next.MinReplicas = *dto.MinReplicas
	}
	if dto.MaxReplicas != nil {
		next.MaxReplicas = *dto.MaxReplicas
	}
	if dto.CPUScaleUpPercent != nil {
		next.CPUScaleUpPercent = *dto.CPUScaleUpPercent
	}
	if dto.CPUScaleDownPercent != nil {
		next.CPUScaleDownPercent = *dto.CPUScaleDownPercent
	}
	if dto.SustainedTicks != nil {
		next.SustainedTicks = *dto.SustainedTicks
	}
	if dto.CooldownSeconds != nil {
		next.Cooldown = time.Duration(*dto.CooldownSeconds) * time.Second
	}
	return next
}

// validatePolicy confere os mesmos invariantes que internal/scaler.Evaluate
// já assume implicitamente -- em particular, que o limiar de scale up é
// estritamente maior que o de scale down (senão a histerese desaparece e
// cada tick oscilaria entre subir e descer).
func validatePolicy(p scaler.Policy) error {
	if p.MinReplicas < 0 {
		return errors.New("min_replicas não pode ser negativo")
	}
	if p.MaxReplicas < 1 {
		return errors.New("max_replicas tem de ser pelo menos 1")
	}
	if p.MinReplicas > p.MaxReplicas {
		return errors.New("min_replicas não pode ser maior que max_replicas")
	}
	if p.CPUScaleDownPercent < 0 {
		return errors.New("cpu_scale_down_percent não pode ser negativo")
	}
	if p.CPUScaleUpPercent <= p.CPUScaleDownPercent {
		return errors.New("cpu_scale_up_percent tem de ser maior que cpu_scale_down_percent")
	}
	if p.SustainedTicks < 0 {
		return errors.New("sustained_ticks não pode ser negativo")
	}
	if p.Cooldown < 0 {
		return errors.New("cooldown_seconds não pode ser negativo")
	}
	return nil
}

type Handler struct {
	tokens TokenVerifier
	group  GroupControl
	audit  *audit.Logger
}

func NewHandler(tokens TokenVerifier, group GroupControl, auditLogger *audit.Logger) *Handler {
	return &Handler{tokens: tokens, group: group, audit: auditLogger}
}

// Register monta as rotas administrativas no mux dado -- pensado para ser
// chamado sobre o mesmo mux que já serve /healthz, /readyz e /metrics (ver
// cmd/group/metrics.go), não um listener separado.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/status", h.requireRole(RoleInfraAdmin, h.handleStatus))
	mux.HandleFunc("GET /v1/policy", h.requireRole(RoleInfraAdmin, h.handleGetPolicy))
	mux.HandleFunc("PUT /v1/policy", h.requireRole(RoleInfraAdmin, h.handlePutPolicy))
	mux.HandleFunc("POST /v1/restart", h.requireRole(RoleInfraAdmin, h.handleRestart))
	mux.HandleFunc("POST /v1/replicas", h.requireRole(RoleInfraAdmin, h.handleAddReplica))
	mux.HandleFunc("DELETE /v1/replicas/{id}", h.requireRole(RoleInfraAdmin, h.handleRemoveReplica))
}

var errMissingToken = errors.New("adminapi: cabeçalho Authorization ausente")

func (h *Handler) bearerClaims(r *http.Request) (jwtverify.Claims, error) {
	const prefix = "Bearer "
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, prefix) {
		return jwtverify.Claims{}, errMissingToken
	}
	return h.tokens.Verify(r.Context(), strings.TrimPrefix(authHeader, prefix))
}

// requireRole espelha services/database/dbadmin/internal/httpapi.requireRole
// (e, por trás dela, services/auth/internal/httpapi.requireRole): exige um
// access token válido cuja claim "role" seja exatamente role. A autorização
// real vive aqui, não no portal -- o portal é só um proxy fino, então mesmo
// um pedido direto a esta API (sem passar pelo portal) fica igualmente
// protegido.
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

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request, _ string) {
	status, err := h.group.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "status_unavailable", "não foi possível ler o estado do group: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) handleGetPolicy(w http.ResponseWriter, r *http.Request, _ string) {
	writeJSON(w, http.StatusOK, PolicyToDTO(h.group.Policy()))
}

func (h *Handler) handlePutPolicy(w http.ResponseWriter, r *http.Request, subject string) {
	var dto PolicyDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido, esperado JSON com os campos de policy a alterar")
		return
	}

	next := dto.applyTo(h.group.Policy())
	if err := validatePolicy(next); err != nil {
		h.audit.Log(subject, "policy.update", fmt.Sprintf("%+v", dto), "rejeitado: "+err.Error())
		writeError(w, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}

	effective, err := h.group.SetPolicy(next)
	if err != nil {
		h.audit.Log(subject, "policy.update", fmt.Sprintf("%+v", dto), "falhou: "+err.Error())
		writeError(w, http.StatusBadGateway, "policy_update_failed", err.Error())
		return
	}

	h.audit.Log(subject, "policy.update", fmt.Sprintf("%+v", dto), "aplicado")
	writeJSON(w, http.StatusOK, PolicyToDTO(effective))
}

func (h *Handler) handleRestart(w http.ResponseWriter, r *http.Request, subject string) {
	if err := h.group.Restart(r.Context()); err != nil {
		h.audit.Log(subject, "restart", "", "falhou: "+err.Error())
		writeError(w, http.StatusBadGateway, "restart_failed", err.Error())
		return
	}

	h.audit.Log(subject, "restart", "", "concluído")
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "restarted",
		"restarted_at": time.Now(),
	})
}

// handleAddReplica cria uma réplica extra imediatamente -- ação manual,
// não passa pelo scaler.Evaluator (ver GroupControl.AddReplica).
func (h *Handler) handleAddReplica(w http.ResponseWriter, r *http.Request, subject string) {
	containerID, err := h.group.AddReplica(r.Context())
	if err != nil {
		h.audit.Log(subject, "replica.add", "", "falhou: "+err.Error())
		writeError(w, http.StatusBadGateway, "add_replica_failed", err.Error())
		return
	}
	h.audit.Log(subject, "replica.add", containerID, "concluído")
	writeJSON(w, http.StatusCreated, map[string]string{"container_id": containerID})
}

// handleRemoveReplica para e remove uma réplica específica, escolhida por
// quem chama -- ação manual, ver GroupControl.RemoveReplica.
func (h *Handler) handleRemoveReplica(w http.ResponseWriter, r *http.Request, subject string) {
	id := r.PathValue("id")
	if err := h.group.RemoveReplica(r.Context(), id); err != nil {
		h.audit.Log(subject, "replica.remove", id, "falhou: "+err.Error())
		writeError(w, http.StatusBadGateway, "remove_replica_failed", err.Error())
		return
	}
	h.audit.Log(subject, "replica.remove", id, "concluído")
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
