// Package platformapi expõe internal/platform por HTTP: projetos,
// ambientes e tudo o que vive dentro de um ambiente. Só traduz pedidos e
// erros -- as decisões estão em internal/platform.
package platformapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"launcher/internal/jwtverify"
	"launcher/internal/platform"
)

const (
	roleInfraAdmin = "infra-admin"
	roleSuperAdmin = "super-admin"
)

type TokenVerifier interface {
	Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error)
}

type API struct {
	svc    *platform.Service
	tokens TokenVerifier
	logger *slog.Logger
}

func New(svc *platform.Service, tokens TokenVerifier, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}
	return &API{svc: svc, tokens: tokens, logger: logger}
}

// Register acrescenta as rotas a um mux existente (o do httpapi), para o
// launcher continuar a servir tudo na mesma porta.
func (a *API) Register(mux *http.ServeMux) {
	admin := a.requireAdmin
	// deployer aceita também um token de deploy (CI) do projeto do URL.
	deployer := a.requireAdminOrDeployToken

	mux.HandleFunc("GET /v1/projects", admin(a.listProjects))
	mux.HandleFunc("POST /v1/projects", admin(a.createProject))
	mux.HandleFunc("GET /v1/projects/{project}", admin(a.getProject))
	mux.HandleFunc("PATCH /v1/projects/{project}", admin(a.updateProject))
	mux.HandleFunc("DELETE /v1/projects/{project}", admin(a.deleteProject))

	mux.HandleFunc("POST /v1/projects/{project}/environments", admin(a.createEnvironment))
	mux.HandleFunc("DELETE /v1/projects/{project}/environments/{env}", admin(a.deleteEnvironment))

	mux.HandleFunc("GET /v1/projects/{project}/environments/{env}/secrets", admin(a.listSecrets))
	mux.HandleFunc("PUT /v1/projects/{project}/environments/{env}/secrets/{name}", admin(a.putSecret))
	mux.HandleFunc("DELETE /v1/projects/{project}/environments/{env}/secrets/{name}", admin(a.deleteSecret))

	mux.HandleFunc("GET /v1/projects/{project}/usage", admin(a.projectUsage))

	mux.HandleFunc("GET /v1/volumes", admin(a.listVolumes))
	mux.HandleFunc("DELETE /v1/volumes/{name}", admin(a.deleteVolume))

	const addons = "/v1/projects/{project}/environments/{env}/addons"
	mux.HandleFunc("GET "+addons, admin(a.listAddons))
	mux.HandleFunc("POST "+addons, admin(a.createAddon))
	mux.HandleFunc("PATCH "+addons+"/{addon}", admin(a.updateAddon))
	mux.HandleFunc("DELETE "+addons+"/{addon}", admin(a.deleteAddon))
	mux.HandleFunc("GET "+addons+"/{addon}/backups", admin(a.listBackups))
	mux.HandleFunc("POST "+addons+"/{addon}/backups", admin(a.createBackup))
	mux.HandleFunc("POST "+addons+"/{addon}/restore", admin(a.restoreBackup))

	const services = "/v1/projects/{project}/environments/{env}/services"
	const svc = services + "/{svc}"
	mux.HandleFunc("GET "+services, admin(a.listServices))
	mux.HandleFunc("POST "+services, admin(a.createService))
	mux.HandleFunc("GET "+svc, deployer(a.getService))
	mux.HandleFunc("PATCH "+svc, admin(a.updateService))
	mux.HandleFunc("DELETE "+svc, admin(a.deleteService))
	mux.HandleFunc("POST "+svc+"/stop", admin(a.setServiceState("stopped")))
	mux.HandleFunc("POST "+svc+"/start", admin(a.setServiceState("running")))
	mux.HandleFunc("GET "+svc+"/revisions", admin(a.listRevisions))
	mux.HandleFunc("GET "+svc+"/replicas", admin(a.listReplicas))
	mux.HandleFunc("POST "+svc+"/run", admin(a.runCommand))
	mux.HandleFunc("GET "+svc+"/deployments", deployer(a.listDeployments))
	mux.HandleFunc("POST "+svc+"/deployments", deployer(a.createDeployment))
	mux.HandleFunc("GET "+svc+"/deployments/{deployment}", deployer(a.getDeployment))

	mux.HandleFunc("GET "+svc+"/domains", admin(a.listDomains))
	mux.HandleFunc("POST "+svc+"/domains", admin(a.addDomain))
	mux.HandleFunc("POST "+svc+"/domains/{hostname}/verify", admin(a.verifyDomain))
	mux.HandleFunc("DELETE "+svc+"/domains/{hostname}", admin(a.deleteDomain))

	mux.HandleFunc("GET /v1/projects/{project}/deploy-tokens", admin(a.listDeployTokens))
	mux.HandleFunc("POST /v1/projects/{project}/deploy-tokens", admin(a.createDeployToken))
	mux.HandleFunc("DELETE /v1/projects/{project}/deploy-tokens/{token}", admin(a.revokeDeployToken))

	mux.HandleFunc("GET /v1/projects/{project}/registries", admin(a.listRegistries))
	mux.HandleFunc("PUT /v1/projects/{project}/registries/{registry}", admin(a.putRegistry))
	mux.HandleFunc("DELETE /v1/projects/{project}/registries/{registry}", admin(a.deleteRegistry))

	mux.HandleFunc("GET /v1/projects/{project}/environments/{env}/jobs", admin(a.listJobs))
	mux.HandleFunc("GET /v1/projects/{project}/environments/{env}/jobs/{job}", admin(a.getJob))
}

// principal é quem fez o pedido: um utilizador (Subject = id da conta) ou
// um token de deploy (Subject = "deploy-token:<nome>").
type principal struct {
	Subject string
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, who principal)

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

// requireAdmin aceita um access token do auth service com role
// infra-admin (ou super-admin, o bypass universal da plataforma).
func (a *API) requireAdmin(next handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "cabeçalho Authorization: Bearer <token> é obrigatório")
			return
		}
		claims, err := a.tokens.Verify(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_token", "access token inválido ou expirado")
			return
		}
		if claims.Role != roleInfraAdmin && claims.Role != roleSuperAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "esta ação exige a role "+roleInfraAdmin)
			return
		}
		next(w, r, principal{Subject: claims.Subject})
	}
}

// requireAdminOrDeployToken: um token "bsd_..." é validado contra o
// projeto/ambiente do próprio URL; qualquer outro bearer segue o caminho
// normal de requireAdmin.
func (a *API) requireAdminOrDeployToken(next handlerFunc) http.HandlerFunc {
	admin := a.requireAdmin(next)
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if !strings.HasPrefix(token, platform.DeployTokenPrefix) {
			admin(w, r)
			return
		}
		t, err := a.svc.AuthorizeDeployToken(r.Context(), token, r.PathValue("project"), r.PathValue("env"))
		switch {
		case errors.Is(err, platform.ErrUnauthorized):
			writeError(w, http.StatusUnauthorized, "invalid_token", err.Error())
		case errors.Is(err, platform.ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden", err.Error())
		case err != nil:
			a.fail(w, err)
		default:
			next(w, r, principal{Subject: "deploy-token:" + t.Name})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

// fail traduz um erro de internal/platform no status certo. Erros não
// previstos são 500 com a mensagem completa -- é uma API interna de
// operador, e esconder a causa só atrasa o diagnóstico.
func (a *API) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platform.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, platform.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, platform.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, platform.ErrQuotaExceeded):
		writeError(w, http.StatusConflict, "quota_exceeded", err.Error())
	case errors.Is(err, platform.ErrSecretsDisabled):
		writeError(w, http.StatusServiceUnavailable, "secrets_disabled", err.Error())
	default:
		a.logger.Error("erro na API de plataforma", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido: "+err.Error())
		return false
	}
	return true
}
