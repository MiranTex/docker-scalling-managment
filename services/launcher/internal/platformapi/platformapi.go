// Package platformapi expõe internal/platform por HTTP: projetos,
// ambientes e tudo o que vive dentro de um ambiente. Só traduz pedidos e
// erros -- as decisões estão em internal/platform.
package platformapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"launcher/internal/apikeyclient"
	"launcher/internal/audit"
	"launcher/internal/jwtverify"
	"launcher/internal/platform"
	"launcher/internal/store"
)

const (
	roleInfraAdmin = "infra-admin"
	roleSuperAdmin = "super-admin"
)

type TokenVerifier interface {
	Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error)
}

// APIKeys valida API keys pessoais (ak_...) -- ver internal/apikeyclient.
// nil desliga-as: só access tokens.
type APIKeys interface {
	Introspect(ctx context.Context, key string) (apikeyclient.Result, error)
}

// platformScope é o scope que uma API key pessoal precisa para chegar a
// esta API: uma key criada para outra coisa não ganha acesso à plataforma
// só porque o dono é admin.
const platformScope = "platform"

// Users resolve um email numa conta do auth, em nome de quem pede (ver
// internal/authusers). nil desliga adicionar membros por email.
type Users interface {
	Resolve(ctx context.Context, bearer, email string) (id, canonicalEmail string, err error)
	Me(ctx context.Context, bearer string) (id, email, role string, err error)
}

type API struct {
	svc      *platform.Service
	tokens   TokenVerifier
	apiKeys  APIKeys
	users    Users
	grafana  Grafana
	term     *terminals
	registry *registryTokens
	agents   *agentSettings
	cli      *cliLogins
	dial     dialer
	audit    *audit.Logger
	logger   *slog.Logger
}

func New(svc *platform.Service, tokens TokenVerifier, apiKeys APIKeys, users Users, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}
	return &API{svc: svc, tokens: tokens, apiKeys: apiKeys, users: users, term: newTerminals(), dial: (&net.Dialer{}).DialContext, audit: audit.NewLogger(logger), logger: logger, cli: newCLILogins()}
}

// Register acrescenta as rotas a um mux existente (o do httpapi), para o
// launcher continuar a servir tudo na mesma porta.
//
// Cada rota diz o mínimo que exige (ver services/launcher/README.md,
// "Membros"): platformAdmin = infra-admin/super-admin; viewer, developer e
// admin = papel no projeto do URL (os admins da plataforma passam sempre).
// deploy* aceitam também um token de deploy (CI) desse projeto.
func (a *API) Register(mux *http.ServeMux) {
	platformAdmin := a.requirePlatformAdmin
	viewer := a.requireMember(store.MemberViewer)
	developer := a.requireMember(store.MemberDeveloper)
	admin := a.requireMember(store.MemberAdmin)
	deployRead := a.orDeployToken(viewer)
	deployWrite := a.orDeployToken(developer)

	mux.HandleFunc("GET /v1/projects", a.requireUser(a.listProjects))
	mux.HandleFunc("POST /v1/projects", platformAdmin(a.createProject))
	mux.HandleFunc("GET /v1/projects/{project}", viewer(a.getProject))
	mux.HandleFunc("PATCH /v1/projects/{project}", platformAdmin(a.updateProject))
	mux.HandleFunc("DELETE /v1/projects/{project}", platformAdmin(a.deleteProject))
	mux.HandleFunc("GET /v1/projects/{project}/usage", viewer(a.projectUsage))
	mux.HandleFunc("GET /v1/projects/{project}/grafana", viewer(a.projectGrafana))
	mux.HandleFunc("POST /v1/grafana/session", a.requireUser(a.grafanaSession))

	mux.HandleFunc("GET /v1/projects/{project}/members", viewer(a.listMembers))
	mux.HandleFunc("POST /v1/projects/{project}/members", admin(a.addMember))
	mux.HandleFunc("PATCH /v1/projects/{project}/members/{user}", admin(a.setMemberRole))
	mux.HandleFunc("DELETE /v1/projects/{project}/members/{user}", admin(a.removeMember))

	mux.HandleFunc("POST /v1/projects/{project}/environments", admin(a.createEnvironment))
	mux.HandleFunc("DELETE /v1/projects/{project}/environments/{env}", admin(a.deleteEnvironment))

	mux.HandleFunc("GET /v1/projects/{project}/environments/{env}/network", viewer(a.getNetwork))
	mux.HandleFunc("GET /v1/projects/{project}/environments/{env}/secrets", viewer(a.listSecrets))
	mux.HandleFunc("PUT /v1/projects/{project}/environments/{env}/secrets/{name}", developer(a.putSecret))
	mux.HandleFunc("DELETE /v1/projects/{project}/environments/{env}/secrets/{name}", developer(a.deleteSecret))

	mux.HandleFunc("GET /v1/nodes", platformAdmin(a.listNodes))
	mux.HandleFunc("PATCH /v1/nodes/{node}", platformAdmin(a.updateNode))
	mux.HandleFunc("DELETE /v1/nodes/{node}", platformAdmin(a.deleteNode))
	mux.HandleFunc("POST /v1/nodes/{node}/check", platformAdmin(a.checkNode))
	mux.HandleFunc("POST /v1/nodes/{node}/drain", platformAdmin(a.drainNode))
	mux.HandleFunc("POST /v1/nodes/join-tokens", platformAdmin(a.createJoinToken))
	// Os agentes dos nós remotos (sem sessão de utilizador -- ver agents.go).
	// Login da CLI pelo browser (ver clilogin.go): o bs começa e pergunta
	// sem sessão; a página do portal autoriza com a sessão de quem entrou.
	mux.HandleFunc("POST /v1/cli/login", a.cliLoginStart)
	mux.HandleFunc("POST /v1/cli/login/poll", a.cliLoginPoll)
	mux.HandleFunc("GET /v1/cli/login/{code}", a.requireUser(a.cliLoginGet))
	mux.HandleFunc("POST /v1/cli/login/{code}/approve", a.requireUser(a.cliLoginApprove))
	// Instalar a CLI por curl (ver clidownload.go).
	mux.HandleFunc("GET /v1/cli/install.sh", a.cliInstallScript)
	mux.HandleFunc("GET /v1/cli/download/{name}", a.cliDownload)
	mux.HandleFunc("GET /v1/agent/ca", a.agentCA)
	mux.HandleFunc("POST /v1/agent/join", a.agentJoin)
	mux.HandleFunc("GET /v1/agent/connect", a.agentConnect)
	mux.HandleFunc("POST /v1/agent/leave", a.agentLeave)

	mux.HandleFunc("GET /v1/volumes", platformAdmin(a.listVolumes))
	mux.HandleFunc("DELETE /v1/volumes/{name}", platformAdmin(a.deleteVolume))

	const addons = "/v1/projects/{project}/environments/{env}/addons"
	mux.HandleFunc("GET "+addons, viewer(a.listAddons))
	mux.HandleFunc("POST "+addons, developer(a.createAddon))
	mux.HandleFunc("PATCH "+addons+"/{addon}", developer(a.updateAddon))
	mux.HandleFunc("DELETE "+addons+"/{addon}", developer(a.deleteAddon))
	mux.HandleFunc("GET "+addons+"/{addon}/backups", viewer(a.listBackups))
	mux.HandleFunc("POST "+addons+"/{addon}/backups", developer(a.createBackup))
	mux.HandleFunc("POST "+addons+"/{addon}/restore", developer(a.restoreBackup))
	mux.HandleFunc("POST "+addons+"/{addon}/terminal", developer(a.addonTerminal))
	// Para a CLI (bs port-forward): túnel até ao addon e as credenciais
	// para o cliente local -- ver tunnel.go.
	mux.HandleFunc("POST "+addons+"/{addon}/tunnel", developer(a.addonTunnel))
	mux.HandleFunc("GET "+addons+"/{addon}/credentials", developer(a.addonCredentials))
	// Buckets de um storage e o acesso público (ver platform/storagepublic.go).
	const bucket = addons + "/{addon}/buckets/{bucket}"
	mux.HandleFunc("GET "+addons+"/{addon}/buckets", viewer(a.listBuckets))
	mux.HandleFunc("POST "+addons+"/{addon}/buckets", developer(a.createBucket))
	mux.HandleFunc("PATCH "+bucket, developer(a.updateBucket))
	mux.HandleFunc("DELETE "+bucket, developer(a.deleteBucket))
	mux.HandleFunc("POST "+bucket+"/domains", developer(a.addBucketDomain))
	mux.HandleFunc("POST "+bucket+"/domains/{hostname}/verify", developer(a.verifyBucketDomain))
	mux.HandleFunc("DELETE "+bucket+"/domains/{hostname}", developer(a.deleteBucketDomain))
	mux.HandleFunc("GET /v1/projects/{project}/tunnel/ws", a.tunnelSocket)

	const services = "/v1/projects/{project}/environments/{env}/services"
	const svc = services + "/{svc}"
	mux.HandleFunc("GET "+services, viewer(a.listServices))
	mux.HandleFunc("POST "+services, developer(a.createService))
	mux.HandleFunc("GET "+svc, deployRead(a.getService))
	mux.HandleFunc("PATCH "+svc, developer(a.updateService))
	mux.HandleFunc("DELETE "+svc, developer(a.deleteService))
	mux.HandleFunc("POST "+svc+"/stop", developer(a.setServiceState("stopped")))
	mux.HandleFunc("POST "+svc+"/start", developer(a.setServiceState("running")))
	mux.HandleFunc("GET "+svc+"/revisions", viewer(a.listRevisions))
	mux.HandleFunc("GET "+svc+"/replicas", viewer(a.listReplicas))
	// Um comando corre com os segredos do ambiente e o output pode
	// mostrá-los -- por isso não é para viewers.
	mux.HandleFunc("POST "+svc+"/run", developer(a.runCommand))
	// O terminal: pede-se um bilhete (developer, pela mesma razão do /run)
	// e com ele abre-se o WebSocket -- ver terminal.go.
	mux.HandleFunc("POST "+svc+"/terminal", developer(a.serviceTerminal))
	mux.HandleFunc("GET /v1/projects/{project}/terminal/ws", a.terminalSocket)
	// Servidor de tokens do registry da plataforma (Basic auth, ver
	// registry.go); o Traefik serve-o em registry.<domínio>/token.
	mux.HandleFunc("GET /v1/registry/token", a.registryToken)
	mux.HandleFunc("GET "+svc+"/deployments", deployRead(a.listDeployments))
	mux.HandleFunc("POST "+svc+"/deployments", deployWrite(a.createDeployment))
	mux.HandleFunc("GET "+svc+"/deployments/{deployment}", deployRead(a.getDeployment))

	mux.HandleFunc("GET "+svc+"/domains", viewer(a.listDomains))
	mux.HandleFunc("POST "+svc+"/domains", developer(a.addDomain))
	mux.HandleFunc("POST "+svc+"/domains/{hostname}/verify", developer(a.verifyDomain))
	mux.HandleFunc("DELETE "+svc+"/domains/{hostname}", developer(a.deleteDomain))
	// Proteção com password à frente do serviço (ver platform/basicauth.go).
	mux.HandleFunc("PUT "+svc+"/basic-auth", developer(a.setBasicAuth))
	mux.HandleFunc("DELETE "+svc+"/basic-auth", developer(a.clearBasicAuth))

	const templates = "/v1/projects/{project}/templates"
	mux.HandleFunc("GET "+templates, viewer(a.listTemplates))
	mux.HandleFunc("POST "+templates, developer(a.createTemplate))
	mux.HandleFunc("GET "+templates+"/{template}", viewer(a.getTemplate))
	mux.HandleFunc("PATCH "+templates+"/{template}", developer(a.updateTemplateInfo))
	mux.HandleFunc("DELETE "+templates+"/{template}", developer(a.archiveTemplate))
	mux.HandleFunc("POST "+templates+"/{template}/versions", developer(a.addTemplateVersion))
	mux.HandleFunc("GET "+svc+"/template-update", viewer(a.previewTemplateUpdate))
	mux.HandleFunc("POST "+svc+"/template-update", developer(a.applyTemplateUpdate))
	mux.HandleFunc("POST "+svc+"/save-as-template", developer(a.saveAsTemplate))

	mux.HandleFunc("GET /v1/projects/{project}/deploy-tokens", viewer(a.listDeployTokens))
	mux.HandleFunc("POST /v1/projects/{project}/deploy-tokens", developer(a.createDeployToken))
	mux.HandleFunc("DELETE /v1/projects/{project}/deploy-tokens/{token}", developer(a.revokeDeployToken))

	mux.HandleFunc("GET /v1/projects/{project}/images", viewer(a.listImages))
	mux.HandleFunc("GET /v1/projects/{project}/registries", viewer(a.listRegistries))
	mux.HandleFunc("PUT /v1/projects/{project}/registries/{registry}", developer(a.putRegistry))
	mux.HandleFunc("DELETE /v1/projects/{project}/registries/{registry}", developer(a.deleteRegistry))

	mux.HandleFunc("GET /v1/projects/{project}/environments/{env}/jobs", viewer(a.listJobs))
	mux.HandleFunc("GET /v1/projects/{project}/environments/{env}/jobs/{job}", viewer(a.getJob))
	mux.HandleFunc("POST /v1/projects/{project}/environments/{env}/jobs/{job}/cancel", developer(a.cancelJob))
}

// principal é quem fez o pedido: uma conta (Subject = id no auth) ou um
// token de deploy (Subject = "deploy-token:<nome>").
type principal struct {
	Subject string
	// PlatformAdmin: infra-admin ou super-admin -- vê e gere tudo.
	PlatformAdmin bool
	// Role: o papel no projeto do URL ("" fora de rotas de projeto, ou
	// para admins da plataforma).
	Role string
	// bearer é o access token do pedido, para chamar o auth em nome de
	// quem pediu (ex: resolver o email de um membro novo). Vazio com API
	// keys e tokens de deploy.
	bearer string
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, who principal)

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

func isPlatformRole(role string) bool { return role == roleInfraAdmin || role == roleSuperAdmin }

// authenticate identifica quem pede: um access token do auth (qualquer
// role -- o acesso a um projeto vem de ser membro), ou uma API key
// pessoal com o scope "platform". Escreve a resposta de erro e devolve
// false se não conseguir.
func (a *API) authenticate(w http.ResponseWriter, r *http.Request) (principal, bool) {
	token := bearer(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing_token", "cabeçalho Authorization: Bearer <token> é obrigatório")
		return principal{}, false
	}
	if strings.HasPrefix(token, apikeyclient.Prefix) {
		return a.authenticateAPIKey(w, r, token)
	}
	claims, err := a.tokens.Verify(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "access token inválido ou expirado")
		return principal{}, false
	}
	return principal{Subject: claims.Subject, PlatformAdmin: isPlatformRole(claims.Role), bearer: token}, true
}

func (a *API) authenticateAPIKey(w http.ResponseWriter, r *http.Request, key string) (principal, bool) {
	if a.apiKeys == nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "API keys não estão ligadas nesta API")
		return principal{}, false
	}
	res, err := a.apiKeys.Introspect(r.Context(), key)
	switch {
	case err != nil:
		a.logger.Error("validando API key", "err", err)
		writeError(w, http.StatusBadGateway, "auth_unavailable", "não foi possível validar a API key")
	case !res.Active:
		writeError(w, http.StatusUnauthorized, "invalid_token", "API key inválida, expirada ou revogada")
	case !res.HasScope(platformScope):
		writeError(w, http.StatusForbidden, "forbidden", "esta API key não tem o scope \""+platformScope+"\"")
	default:
		return principal{Subject: res.Owner, PlatformAdmin: isPlatformRole(res.Role)}, true
	}
	return principal{}, false
}

// requireUser: qualquer conta autenticada (ex: listar os seus projetos).
func (a *API) requireUser(next handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if who, ok := a.authenticate(w, r); ok {
			next(w, r, who)
		}
	}
}

// requirePlatformAdmin: só infra-admin e super-admin.
func (a *API) requirePlatformAdmin(next handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		if !who.PlatformAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "só administradores da plataforma (infra-admin) podem fazer isto")
			return
		}
		next(w, r, who)
	}
}

// requireMember exige pelo menos o papel min no projeto do URL. Quem não é
// membro recebe 404, como se o projeto não existisse -- não fica a saber
// que projetos há.
func (a *API) requireMember(min string) func(handlerFunc) http.HandlerFunc {
	return func(next handlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			who, ok := a.authenticate(w, r)
			if !ok {
				return
			}
			if who.PlatformAdmin {
				next(w, r, who)
				return
			}
			role, err := a.svc.MemberRole(r.Context(), r.PathValue("project"), who.Subject)
			if err != nil {
				a.fail(w, err)
				return
			}
			if role == "" {
				writeError(w, http.StatusNotFound, "not_found", "projeto não encontrado")
				return
			}
			if !platform.RoleAtLeast(role, min) {
				writeError(w, http.StatusForbidden, "forbidden", "esta ação exige o papel "+min+" neste projeto (tens "+role+")")
				return
			}
			who.Role = role
			next(w, r, who)
		}
	}
}

// orDeployToken: um token "bsd_..." é validado contra o projeto/ambiente
// do próprio URL; qualquer outro bearer segue o caminho de membro.
func (a *API) orDeployToken(member func(handlerFunc) http.HandlerFunc) func(handlerFunc) http.HandlerFunc {
	return func(next handlerFunc) http.HandlerFunc {
		asMember := member(next)
		return func(w http.ResponseWriter, r *http.Request) {
			token := bearer(r)
			if !strings.HasPrefix(token, platform.DeployTokenPrefix) {
				asMember(w, r)
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
	case errors.Is(err, platform.ErrUnauthorizedNode):
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
	case errors.Is(err, platform.ErrNoNode):
		writeError(w, http.StatusConflict, "no_node", err.Error())
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
