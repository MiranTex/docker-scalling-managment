// Package httpapi expõe a API de administração da base de dados: estado
// e backups (PITR e comandos de manutenção ficam para fases seguintes --
// ver o plano). Mesmo padrão de services/auth/internal/httpapi:
// interfaces pequenas nas dependências, requireRole para os endpoints
// administrativos.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"dbadmin/internal/audit"
	"dbadmin/internal/backupjob"
	"dbadmin/internal/jwtverify"
	"dbadmin/internal/pgbackrest"
	"dbadmin/internal/pgstat"
	"dbadmin/internal/restorejob"
)

// RoleInfraAdmin é a única role aceite pelos endpoints administrativos --
// ver services/auth/internal/store/role.go, onde já existe explicitamente
// para "gestão de infraestrutura (ex: autoscaler)". Esta API de
// administração da base de dados é mais um consumidor dessa mesma role,
// não um novo conceito de autorização.
const RoleInfraAdmin = "infra-admin"

// TokenVerifier valida o access token do auth service -- implementado
// por *jwtverify.Verifier.
type TokenVerifier interface {
	Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error)
}

// DBStatus lê estado operacional do Postgres local -- implementado por
// *pgstat.Client.
type DBStatus interface {
	Status(ctx context.Context) (pgstat.Status, error)
	Ping(ctx context.Context) error
}

type Handler struct {
	tokens   TokenVerifier
	db       DBStatus
	backups  pgbackrest.Client
	jobs     *backupjob.Manager
	restores *restorejob.Manager
	audit    *audit.Logger
}

func NewHandler(tokens TokenVerifier, db DBStatus, backups pgbackrest.Client, jobs *backupjob.Manager, restores *restorejob.Manager, auditLogger *audit.Logger) *Handler {
	return &Handler{tokens: tokens, db: db, backups: backups, jobs: jobs, restores: restores, audit: auditLogger}
}

func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", h.requireRole(RoleInfraAdmin, h.handleStatus))
	mux.HandleFunc("GET /v1/backups", h.requireRole(RoleInfraAdmin, h.handleListBackups))
	mux.HandleFunc("POST /v1/backups", h.requireRole(RoleInfraAdmin, h.handleTriggerBackup))
	mux.HandleFunc("GET /v1/backups/jobs/{id}", h.requireRole(RoleInfraAdmin, h.handleGetJob))
	mux.HandleFunc("POST /v1/restore", h.requireRole(RoleInfraAdmin, h.handleTriggerRestore))
	mux.HandleFunc("GET /v1/restore/jobs/{id}", h.requireRole(RoleInfraAdmin, h.handleGetRestoreJob))
	mux.HandleFunc("POST /v1/restore/jobs/{id}/confirm", h.requireRole(RoleInfraAdmin, h.handleConfirmRestore))
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

// requireRole espelha services/auth/internal/httpapi.requireRole: exige
// um access token válido cuja claim "role" seja exatamente role. A
// autorização real vive aqui, não no portal -- o portal é só um proxy
// fino (ver services/portal/app/api/admin/database/*), então mesmo um
// pedido direto a esta API (sem passar pelo portal) fica igualmente
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
		if claims.Role != role {
			writeError(w, http.StatusForbidden, "forbidden", "esta ação exige a role "+role)
			return
		}
		next(w, r, claims.Subject)
	}
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request, _ string) {
	status, err := h.db.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "db_unreachable", "não foi possível ler o estado do Postgres: "+err.Error())
		return
	}

	info, err := h.backups.Info(r.Context())
	if err != nil {
		// Estado do Postgres é mais importante que o de backups -- não
		// falha o endpoint inteiro por causa do pgbackrest, só reporta a
		// info de backup como indisponível.
		writeJSON(w, http.StatusOK, map[string]any{
			"postgres":          status,
			"last_backup_at":    nil,
			"backup_info_error": err.Error(),
		})
		return
	}

	last := pgbackrest.LastBackupAt(info)
	resp := map[string]any{
		"postgres":       status,
		"last_backup_at": nil,
	}
	if !last.IsZero() {
		resp["last_backup_at"] = last
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleListBackups(w http.ResponseWriter, r *http.Request, _ string) {
	info, err := h.backups.Info(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "pgbackrest_unreachable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type triggerBackupRequest struct {
	Type string `json:"type"`
}

func (h *Handler) handleTriggerBackup(w http.ResponseWriter, r *http.Request, subject string) {
	var req triggerBackupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido, esperado {\"type\": \"full|diff|incr\"}")
		return
	}

	job, err := h.jobs.Start(req.Type, subject)
	if errors.Is(err, backupjob.ErrAlreadyRunning) {
		h.audit.Log(subject, "backup.trigger", req.Type, "rejeitado: já existe um backup em andamento")
		writeError(w, http.StatusConflict, "backup_in_progress", "já existe um backup em andamento")
		return
	}
	if err != nil {
		h.audit.Log(subject, "backup.trigger", req.Type, "rejeitado: "+err.Error())
		writeError(w, http.StatusBadRequest, "invalid_type", err.Error())
		return
	}

	h.audit.Log(subject, "backup.trigger", req.Type, "iniciado, job="+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) handleGetJob(w http.ResponseWriter, r *http.Request, _ string) {
	id := r.PathValue("id")
	job, ok := h.jobs.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "job desconhecido: "+id)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

type triggerRestoreRequest struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

// handleTriggerRestore inicia um PITR: para o Postgres, corre pgbackrest
// restore, arranca-o de novo em recovery pausado (ver
// internal/restorerunner). A confirmação (typed confirmation) de que
// isto é mesmo destrutivo já aconteceu do lado da UI antes deste pedido
// existir -- não repetimos essa checagem aqui, mas o audit log regista
// quem pediu, para quando isso precisar de ser auditado depois.
func (h *Handler) handleTriggerRestore(w http.ResponseWriter, r *http.Request, subject string) {
	var req triggerRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido, esperado {\"type\": \"time|xid|lsn|name\", \"target\": \"...\"}")
		return
	}
	if req.Type != "" && !pgbackrest.ValidRestoreType(req.Type) {
		writeError(w, http.StatusBadRequest, "invalid_type", "tipo de restore inválido: "+req.Type)
		return
	}
	if req.Type != "" && req.Target == "" {
		writeError(w, http.StatusBadRequest, "missing_target", "\"target\" é obrigatório quando \"type\" é indicado")
		return
	}

	job, err := h.restores.Start(req.Type, req.Target, subject)
	if errors.Is(err, restorejob.ErrAlreadyRunning) {
		h.audit.Log(subject, "restore.trigger", req.Type+" "+req.Target, "rejeitado: já existe um backup/restore em andamento")
		writeError(w, http.StatusConflict, "operation_in_progress", "já existe um backup ou restore em andamento")
		return
	}

	h.audit.Log(subject, "restore.trigger", req.Type+" "+req.Target, "iniciado, job="+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) handleGetRestoreJob(w http.ResponseWriter, r *http.Request, _ string) {
	id := r.PathValue("id")
	job, ok := h.restores.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "job desconhecido: "+id)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleConfirmRestore promove o recovery pausado -- a partir daqui a
// operação é IRREVERSÍVEL (ver services/database/README.md sobre timeline
// nova). Não há "cancel": uma vez que o pgbackrest restore já reescreveu
// o PGDATA, a única forma de "desistir" é fazer outro restore, não existe
// um "desfazer" seguro no meio do caminho.
func (h *Handler) handleConfirmRestore(w http.ResponseWriter, r *http.Request, subject string) {
	id := r.PathValue("id")
	job, err := h.restores.Confirm(r.Context(), id, subject)
	if errors.Is(err, restorejob.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "job desconhecido: "+id)
		return
	}
	if errors.Is(err, restorejob.ErrWrongState) {
		writeError(w, http.StatusConflict, "wrong_state", "job não está à espera de confirmação")
		return
	}
	if err != nil {
		h.audit.Log(subject, "restore.confirm", id, "falhou: "+err.Error())
		writeError(w, http.StatusInternalServerError, "confirm_failed", err.Error())
		return
	}

	h.audit.Log(subject, "restore.confirm", id, "confirmado")
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ping(r.Context()); err != nil {
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
