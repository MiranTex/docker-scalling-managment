// Package httpapi expõe a API do launcher: o único serviço que, de
// facto, cria containers a partir de um modelo (template) -- resolvendo
// primeiro qualquer referência ${secret:NOME} no seu "env" contra o
// secretsadmin. Dois tipos de chamador, duas rotas:
//
//   - Humanos (via portal), role infra-admin: POST/GET/DELETE /v1/instances
//     -- lançar/listar/parar uma instância "solo" (um container qualquer,
//     sem autoscaling nenhum) ou "group" (um autoscaler-group inteiro, que
//     depois passa a gerir as suas próprias réplicas).
//   - Um autoscaler-group já em execução, role service: POST /v1/replicas
//     -- pedir UMA réplica nova a partir do seu próprio launch template
//     (ainda com segredos por resolver). É esta rota que substitui o que
//     antes era feito localmente por cada cmd/group (ver
//     services/autoscaler/internal/launcherclient no lado de quem chama).
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"launcher/internal/dockerclient"
	"launcher/internal/jwtverify"
	"launcher/internal/store"
	"launcher/internal/templatesclient"
)

const (
	RoleInfraAdmin = "infra-admin"
	RoleService    = "service"
	// RoleSuperAdmin funciona como bypass universal em todo requireRole
	// desta API -- mesmo comportamento de services/secretsadmin e
	// services/templatesadmin.
	RoleSuperAdmin = "super-admin"
)

type TokenVerifier interface {
	Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error)
}

// Store é o subconjunto de *store.DB usado por este pacote.
type Store interface {
	List(ctx context.Context) ([]store.Instance, error)
	Create(ctx context.Context, i store.Instance) error
	UpdateStatus(ctx context.Context, id, status, errMsg, updatedBy string) error
	Ping(ctx context.Context) error
}

// Docker é o subconjunto do cliente Docker usado por este pacote -- só o
// necessário para criar/parar UM container por vez, nunca para operações
// de reconcile em massa (isso continua sendo trabalho de cada group,
// contra a sua PRÓPRIA cópia read-only de dockerclient).
type Docker interface {
	InspectImage(ctx context.Context, ref string) (*dockerclient.ImageInspect, error)
	PullImage(ctx context.Context, ref string) error
	CreateContainer(ctx context.Context, name string, req dockerclient.CreateContainerRequest) (string, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string, timeoutSeconds int) error
	RemoveContainer(ctx context.Context, id string, force bool) error
	// ListContainers só é usado por GET /v1/groups -- descoberta em tempo
	// real, contra o Docker, de todo container de group vivo (ver
	// labelGroupRole), independentemente de ter sido criado por este
	// launcher ou declarado estaticamente num docker-compose.yml (ex:
	// group-authd).
	ListContainers(ctx context.Context, all bool, filters map[string][]string) ([]dockerclient.Container, error)
	// Métodos de rede -- ver internal/dockerclient/networks.go. Cobrem
	// list/create/inspect/delete de uma rede, e connect/disconnect de UM
	// container -- nunca subnets, IPAM ou opções de driver.
	ListNetworks(ctx context.Context) ([]dockerclient.Network, error)
	CreateNetwork(ctx context.Context, name string) (string, error)
	InspectNetwork(ctx context.Context, id string) (*dockerclient.NetworkDetail, error)
	RemoveNetwork(ctx context.Context, id string) error
	ConnectNetwork(ctx context.Context, networkID, containerID string) error
	DisconnectNetwork(ctx context.Context, networkID, containerID string) error
}

// Secrets resolve ${secret:NOME} dentro de um "env" -- ver
// internal/secretsclient.Client.
type Secrets interface {
	ResolveEnv(ctx context.Context, env []string) ([]string, error)
}

// Templates busca um modelo de serviço pelo nome no templatesadmin -- ver
// internal/templatesclient.Client.
type Templates interface {
	Get(ctx context.Context, name, bearerToken string) (templatesclient.Template, error)
}

// GroupConfig reúne os valores que todo autoscaler-group lançado por este
// launcher precisa, além do seu próprio launch template -- os mesmos que
// hoje são escritos à mão no bloco "group-authd" de demo/docker-compose.yml.
type GroupConfig struct {
	Image          string // ex: "autoscaler-group:latest"
	AuthServiceURL string
	AuthIssuer     string
	AuthAudience   string
	// SelfURL é o endereço deste próprio launcher, alcançável pelo group
	// recém-criado (ex: "http://launcher:8094") -- é para onde o group vai
	// mandar os seus próprios pedidos de réplica (POST /v1/replicas).
	SelfURL string
	// DockerSocket é montado em todo group lançado por este launcher --
	// sem isso ele não conseguiria nem listar/parar as suas próprias
	// réplicas (leitura/monitorização, que continua local a cada group).
	DockerSocket string
}

const stopTimeoutSeconds = 10

// Convenção de labels para identificar um container de autoscaler-group
// (o processo cmd/group, nunca uma réplica da aplicação que ele gere) --
// tanto os lançados por este launcher (ver launchGroup) como os
// declarados estaticamente num docker-compose.yml (ex: group-authd, que
// precisa das mesmas labels em demo/docker-compose.yml para aparecer em
// GET /v1/groups). groupAdminPort é a porta do servidor admin/métricas de
// TODO cmd/group -- convenção herdada de METRICS_ADDR, cujo default
// (":9090") ninguém no demo sobrepõe.
const (
	labelGroupRole          = "autoscaler.role"
	labelGroupRoleValue     = "group"
	labelGroupTargetService = "autoscaler.target_service"
	groupAdminPort          = "9090"
	// labelServiceKey é a mesma convenção usada por cada cmd/group
	// (SERVICE_LABEL, default "autoscaler.service") -- toda RÉPLICA de
	// aplicação (nunca o processo group em si, que usa labelGroupRole)
	// carrega esta label com o nome do serviço que ela serve. Injetada
	// automaticamente pelo próprio cmd/group ao ler o seu launch template
	// (ver services/autoscaler/cmd/group/config.go), não por este
	// launcher -- é só o que permite descobrir réplicas vivas de QUALQUER
	// group em GET /v1/replicas, mesmo as criadas localmente por ele sem
	// nunca passar por aqui.
	labelServiceKey = "autoscaler.service"
)

type Handler struct {
	tokens    TokenVerifier
	store     Store
	docker    Docker
	secrets   Secrets
	templates Templates
	groups    GroupConfig
}

func NewHandler(tokens TokenVerifier, s Store, docker Docker, secrets Secrets, templates Templates, groups GroupConfig) *Handler {
	return &Handler{tokens: tokens, store: s, docker: docker, secrets: secrets, templates: templates, groups: groups}
}

func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/instances", h.requireRole(RoleInfraAdmin, h.handleCreateInstance))
	mux.HandleFunc("GET /v1/instances", h.requireRole(RoleInfraAdmin, h.handleListInstances))
	mux.HandleFunc("POST /v1/replicas", h.requireRole(RoleService, h.handleCreateReplica))
	mux.HandleFunc("GET /v1/replicas", h.requireRole(RoleInfraAdmin, h.handleListReplicas))
	mux.HandleFunc("GET /v1/groups", h.requireRole(RoleInfraAdmin, h.handleListGroups))
	mux.HandleFunc("DELETE /v1/groups/{containerId}", h.requireRole(RoleInfraAdmin, h.handleDeleteGroup))
	mux.HandleFunc("POST /v1/containers/{containerId}/stop", h.requireRole(RoleInfraAdmin, h.handleStopContainer))
	mux.HandleFunc("DELETE /v1/containers/{containerId}", h.requireRole(RoleInfraAdmin, h.handleRemoveContainer))
	mux.HandleFunc("GET /v1/networks", h.requireRole(RoleInfraAdmin, h.handleListNetworks))
	mux.HandleFunc("POST /v1/networks", h.requireRole(RoleInfraAdmin, h.handleCreateNetwork))
	mux.HandleFunc("GET /v1/networks/{name}", h.requireRole(RoleInfraAdmin, h.handleGetNetwork))
	mux.HandleFunc("DELETE /v1/networks/{name}", h.requireRole(RoleInfraAdmin, h.handleDeleteNetwork))
	mux.HandleFunc("POST /v1/networks/{name}/connect", h.requireRole(RoleInfraAdmin, h.handleConnectNetwork))
	mux.HandleFunc("POST /v1/networks/{name}/disconnect", h.requireRole(RoleInfraAdmin, h.handleDisconnectNetwork))
	mux.HandleFunc("GET /v1/containers", h.requireRole(RoleInfraAdmin, h.handleListAllContainers))
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("GET /readyz", h.handleReadyz)
	return mux
}

var errMissingToken = errors.New("httpapi: cabeçalho Authorization ausente")

func bearerToken(r *http.Request) (string, error) {
	const prefix = "Bearer "
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, prefix) {
		return "", errMissingToken
	}
	return strings.TrimPrefix(authHeader, prefix), nil
}

func (h *Handler) requireRole(role string, next func(w http.ResponseWriter, r *http.Request, subject, token string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := bearerToken(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "missing_token", "cabeçalho Authorization: Bearer <token> é obrigatório")
			return
		}
		claims, err := h.tokens.Verify(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_token", "access token inválido ou expirado")
			return
		}
		if claims.Role != role && claims.Role != RoleSuperAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "esta ação exige a role "+role)
			return
		}
		next(w, r, claims.Subject, token)
	}
}

// createInstanceRequest é o corpo de POST /v1/instances. TemplateName +
// Kind são sempre obrigatórios; os restantes campos só são lidos quando
// Kind="group" -- é aqui, não no modelo (templatesadmin.Template já não
// tem estes campos), que se decide COMO um group vai escalar o container
// descrito pelo modelo.
type createInstanceRequest struct {
	TemplateName string `json:"templateName"`
	Kind         string `json:"kind"`

	// Network, se preenchido, substitui a rede Docker do modelo (tmpl.Network)
	// só para ESTA instância -- ex: um modelo sem rede definida (ou com uma
	// rede errada) criava, por defeito, um container na rede "bridge" do
	// Docker, isolado do launcher/portal/database (ver "auth-net" no
	// demo) e por isso inatingível pela API admin. É um campo de
	// DEPLOYMENT (qual rede, não o que a aplicação precisa), por isso vive
	// aqui, não no modelo -- mesmo raciocínio de TargetService/réplicas.
	Network string `json:"network,omitempty"`

	// Campos usados só quando Kind="group" -- mesmo significado dos
	// campos homónimos que existiam em store.Template antes desta versão.
	TargetService       string  `json:"targetService,omitempty"`
	BackendPort         int     `json:"backendPort,omitempty"`
	MinReplicas         int     `json:"minReplicas,omitempty"`
	MaxReplicas         int     `json:"maxReplicas,omitempty"`
	CPUScaleUpPercent   float64 `json:"cpuScaleUpPercent,omitempty"`
	CPUScaleDownPercent float64 `json:"cpuScaleDownPercent,omitempty"`

	// ReplicaAuthToken só é usado quando Kind="group": é o refresh token
	// (role "service", emitido manualmente -- ver demo/README.md,
	// "Bootstrap da conta de serviço do autoscaler") que o group recém-criado
	// vai usar para se autenticar de volta contra este launcher em POST
	// /v1/replicas. Bootstrap manual aceite deliberadamente, não resolvido
	// nesta iteração -- mesmo espírito do SECRETS_REFRESH_TOKEN inicial que
	// já existia antes desta feature.
	ReplicaAuthToken string `json:"replicaAuthToken,omitempty"`
}

// handleCreateInstance lança uma instância nova a partir de um modelo já
// guardado no templatesadmin. O bearer token de quem chamou é reenviado
// ao templatesadmin (ver internal/templatesclient) -- este launcher não
// tem credencial própria pra ler modelos, só para resolver segredos.
func (h *Handler) handleCreateInstance(w http.ResponseWriter, r *http.Request, subject, token string) {
	var req createInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido: "+err.Error())
		return
	}
	if req.Kind != store.KindSolo && req.Kind != store.KindGroup {
		writeError(w, http.StatusBadRequest, "invalid_kind", `"kind" deve ser "solo" ou "group"`)
		return
	}
	if req.TemplateName == "" {
		writeError(w, http.StatusBadRequest, "empty_template_name", `"templateName" não pode ser vazio`)
		return
	}

	tmpl, err := h.templates.Get(r.Context(), req.TemplateName, token)
	if err != nil {
		writeError(w, http.StatusBadGateway, "template_lookup_failed", "erro lendo modelo no templatesadmin: "+err.Error())
		return
	}

	id, err := newInstanceID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro gerando id")
		return
	}

	if err := h.ensureImage(r.Context(), tmpl.Image); err != nil {
		writeError(w, http.StatusBadGateway, "image_unavailable", err.Error())
		return
	}

	var containerID string
	if req.Kind == store.KindSolo {
		containerID, err = h.launchSolo(r.Context(), id, tmpl, req)
	} else {
		containerID, err = h.launchGroup(r.Context(), id, tmpl, req)
	}

	instance := store.Instance{
		ID:           id,
		Kind:         req.Kind,
		TemplateName: req.TemplateName,
		ContainerID:  containerID,
		Status:       store.StatusRunning,
		UpdatedBy:    subject,
	}
	if err != nil {
		instance.Status = store.StatusFailed
		instance.Error = err.Error()
	}
	if dbErr := h.store.Create(r.Context(), instance); dbErr != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro gravando instância: "+dbErr.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "launch_failed", "erro lançando instância: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, instance)
}

// resolveNetwork devolve a rede Docker a usar nesta instância: a do
// pedido, se preenchida, senão a do modelo (pode ser "" também, caso em
// que o Docker usa "bridge" -- ver aviso em createInstanceRequest.Network).
func resolveNetwork(reqNetwork, templateNetwork string) string {
	if reqNetwork != "" {
		return reqNetwork
	}
	return templateNetwork
}

// launchSolo cria e inicia diretamente o container da aplicação descrita
// pelo modelo -- sem nenhum autoscaler entre o launcher e o container.
func (h *Handler) launchSolo(ctx context.Context, id string, tmpl templatesclient.Template, req createInstanceRequest) (string, error) {
	resolvedEnv, err := h.secrets.ResolveEnv(ctx, tmpl.Env)
	if err != nil {
		return "", fmt.Errorf("resolvendo segredos do modelo: %w", err)
	}

	labels := map[string]string{}
	for k, v := range tmpl.Labels {
		labels[k] = v
	}
	labels["launcher.instance"] = id
	labels[dockerclient.PlatformLabel] = dockerclient.PlatformLabelValue

	containerID, err := h.docker.CreateContainer(ctx, "", dockerclient.CreateContainerRequest{
		Image:  tmpl.Image,
		Cmd:    tmpl.Cmd,
		Env:    resolvedEnv,
		Labels: labels,
		HostConfig: &dockerclient.CreateHostConfig{
			Binds:       tmpl.Binds,
			NetworkMode: resolveNetwork(req.Network, tmpl.Network),
			ExtraHosts:  tmpl.ExtraHosts,
		},
	})
	if err != nil {
		return "", fmt.Errorf("criando container: %w", err)
	}
	if err := h.docker.StartContainer(ctx, containerID); err != nil {
		return "", fmt.Errorf("iniciando container: %w", err)
	}
	return containerID, nil
}

// launchGroup lança um autoscaler-group inteiro: o container que sobe é o
// cmd/group, não a aplicação em si -- é o group, uma vez de pé, quem passa
// a criar (via POST /v1/replicas contra este mesmo launcher) e remover as
// réplicas da aplicação.
//
// Importante: o "env" do modelo (tmpl.Env, com eventuais ${secret:NOME})
// NÃO é resolvido aqui -- é o env de cada RÉPLICA da aplicação, não do
// processo group. Fica intacto dentro de LAUNCH_TEMPLATE_JSON para o group
// mandar de volta, sem resolver, em cada POST /v1/replicas que ele fizer;
// resolver fica sempre a cargo deste launcher, no momento de cada réplica
// (mesmo timing de antes, ver services/autoscaler/cmd/group/main.go,
// applyScaleUp).
func (h *Handler) launchGroup(ctx context.Context, id string, tmpl templatesclient.Template, req createInstanceRequest) (string, error) {
	if req.TargetService == "" {
		return "", errors.New(`"targetService" é obrigatório para lançar como "group"`)
	}

	// A label da plataforma é injetada aqui, não deixada a cargo do
	// modelo -- sem isso, um group criado por este launcher a partir de um
	// modelo sem essa label (ex: qualquer um feito pelo templatesadmin,
	// que não a define) teria réplicas invisíveis a GET /v1/replicas e ao
	// picker de rede, exatamente o mesmo bug que o group-authd só evita
	// por ter a label escrita à mão no seu launch template estático.
	replicaLabels := map[string]string{}
	for k, v := range tmpl.Labels {
		replicaLabels[k] = v
	}
	replicaLabels[dockerclient.PlatformLabel] = dockerclient.PlatformLabelValue

	innerTemplate := struct {
		Image      string            `json:"image"`
		Cmd        []string          `json:"cmd,omitempty"`
		Env        []string          `json:"env,omitempty"`
		Labels     map[string]string `json:"labels,omitempty"`
		Binds      []string          `json:"binds,omitempty"`
		Network    string            `json:"network,omitempty"`
		ExtraHosts []string          `json:"extraHosts,omitempty"`
	}{tmpl.Image, tmpl.Cmd, tmpl.Env, replicaLabels, tmpl.Binds, tmpl.Network, tmpl.ExtraHosts}
	launchTemplateJSON, err := json.Marshal(innerTemplate)
	if err != nil {
		return "", fmt.Errorf("codificando launch template do group: %w", err)
	}

	groupEnv := []string{
		"TARGET_SERVICE=" + req.TargetService,
		"BACKEND_PORT=" + strconv.Itoa(orDefault(req.BackendPort, 80)),
		"MIN_REPLICAS=" + strconv.Itoa(req.MinReplicas),
		"MAX_REPLICAS=" + strconv.Itoa(orDefault(req.MaxReplicas, 3)),
		"CPU_SCALE_UP_PERCENT=" + strconv.FormatFloat(orDefaultF(req.CPUScaleUpPercent, 50), 'f', -1, 64),
		"CPU_SCALE_DOWN_PERCENT=" + strconv.FormatFloat(orDefaultF(req.CPUScaleDownPercent, 20), 'f', -1, 64),
		"AUTH_SERVICE_URL=" + h.groups.AuthServiceURL,
		"AUTH_ISSUER=" + h.groups.AuthIssuer,
		"AUTH_AUDIENCE=" + h.groups.AuthAudience,
		"LAUNCHER_SERVICE_URL=" + h.groups.SelfURL,
		"LAUNCHER_REFRESH_TOKEN=" + req.ReplicaAuthToken,
		"LAUNCH_TEMPLATE_JSON=" + string(launchTemplateJSON),
	}

	// Nome determinístico (não deixado ao Docker escolher, ao contrário de
	// launchSolo) -- é assim que GET /v1/groups e o portal conseguem
	// endereçar a API admin deste group por nome DNS (ver
	// handleListGroups), sem precisar de descobrir/guardar um IP.
	containerName := "group-" + id
	containerID, err := h.docker.CreateContainer(ctx, containerName, dockerclient.CreateContainerRequest{
		Image: h.groups.Image,
		Env:   groupEnv,
		Labels: map[string]string{
			"launcher.instance":        id,
			labelGroupRole:             labelGroupRoleValue,
			labelGroupTargetService:    req.TargetService,
			dockerclient.PlatformLabel: dockerclient.PlatformLabelValue,
		},
		HostConfig: &dockerclient.CreateHostConfig{
			NetworkMode: resolveNetwork(req.Network, tmpl.Network),
			Binds:       []string{h.groups.DockerSocket + ":/var/run/docker.sock"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("criando container do group: %w", err)
	}
	if err := h.docker.StartContainer(ctx, containerID); err != nil {
		return "", fmt.Errorf("iniciando container do group: %w", err)
	}
	return containerID, nil
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func orDefaultF(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}

// ensureImage confirma que a imagem existe localmente antes de a usar --
// mesma razão de services/autoscaler/cmd/group's validateLaunchTemplateImage:
// sem isso, um modelo com a imagem errada só falharia dentro do
// CreateContainer, com um erro menos claro.
func (h *Handler) ensureImage(ctx context.Context, image string) error {
	if _, err := h.docker.InspectImage(ctx, image); err == nil {
		return nil
	}
	if err := h.docker.PullImage(ctx, image); err != nil {
		return fmt.Errorf("imagem %q não encontrada localmente e pull falhou: %w", image, err)
	}
	return nil
}

func (h *Handler) handleListInstances(w http.ResponseWriter, r *http.Request, _, _ string) {
	instances, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro listando instâncias")
		return
	}
	writeJSON(w, http.StatusOK, instances)
}

type createReplicaRequest struct {
	Image      string            `json:"image"`
	Cmd        []string          `json:"cmd,omitempty"`
	Env        []string          `json:"env,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Binds      []string          `json:"binds,omitempty"`
	Network    string            `json:"network,omitempty"`
	ExtraHosts []string          `json:"extraHosts,omitempty"`
}

type createReplicaResponse struct {
	ContainerID string `json:"containerId"`
}

// handleCreateReplica é chamado por um autoscaler-group já em execução, a
// cada scale up -- substitui o que antes era feito localmente por
// executor.scaleUpFromTemplate + internal/secretsclient dentro do próprio
// cmd/group (ver services/autoscaler/internal/launcherclient, do lado de
// quem chama). Resolve ${secret:NOME} em req.Env e só então cria+inicia o
// container -- feito a cada chamada, nunca em cache, para que atualizar o
// VALOR de um segredo já existente se reflita na próxima réplica sem
// precisar de restart.
func (h *Handler) handleCreateReplica(w http.ResponseWriter, r *http.Request, _, _ string) {
	var req createReplicaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido: "+err.Error())
		return
	}
	if req.Image == "" {
		writeError(w, http.StatusBadRequest, "empty_image", `"image" não pode ser vazio`)
		return
	}

	resolvedEnv, err := h.secrets.ResolveEnv(r.Context(), req.Env)
	if err != nil {
		writeError(w, http.StatusBadGateway, "secrets_resolve_failed", "erro resolvendo segredos: "+err.Error())
		return
	}

	containerID, err := h.docker.CreateContainer(r.Context(), "", dockerclient.CreateContainerRequest{
		Image:  req.Image,
		Cmd:    req.Cmd,
		Env:    resolvedEnv,
		Labels: req.Labels,
		HostConfig: &dockerclient.CreateHostConfig{
			Binds:       req.Binds,
			NetworkMode: req.Network,
			ExtraHosts:  req.ExtraHosts,
		},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "create_failed", "erro criando container: "+err.Error())
		return
	}
	if err := h.docker.StartContainer(r.Context(), containerID); err != nil {
		writeError(w, http.StatusBadGateway, "start_failed", "erro iniciando container: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, createReplicaResponse{ContainerID: containerID})
}

// GroupSummary é uma linha de GET /v1/groups: um container de
// autoscaler-group vivo, descoberto contra o Docker (não contra a store
// deste launcher, que só sabe do que ele próprio lançou) -- por isso
// inclui igualmente groups estáticos de um docker-compose.yml (ex:
// group-authd), desde que tenham a mesma label (ver labelGroupRole).
type GroupSummary struct {
	ContainerID   string `json:"containerId"`
	Name          string `json:"name"`
	TargetService string `json:"targetService"`
	Image         string `json:"image"`
	State         string `json:"state"`
	// Network é só para exibição (ver GET /v1/networks para a lista
	// completa de redes existentes) -- um group normalmente só está numa
	// rede, a mesma da aplicação que gere.
	Network string `json:"network"`
	// AdminURL é o endereço da API admin deste group (ver
	// services/autoscaler/internal/adminapi), alcançável de dentro da
	// mesma rede Docker pelo nome do container -- nunca pelo proxy
	// (porta do LISTEN_ADDR), que serve tráfego da aplicação, não admin.
	AdminURL string `json:"adminUrl"`
}

func (h *Handler) handleListGroups(w http.ResponseWriter, r *http.Request, _, _ string) {
	containers, err := h.docker.ListContainers(r.Context(), false, map[string][]string{
		"label": {labelGroupRole + "=" + labelGroupRoleValue},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro listando groups: "+err.Error())
		return
	}

	groups := make([]GroupSummary, 0, len(containers))
	for _, c := range containers {
		name := containerDisplayName(c)
		groups = append(groups, GroupSummary{
			ContainerID:   c.ID,
			Name:          name,
			TargetService: c.Labels[labelGroupTargetService],
			Image:         c.Image,
			State:         c.State,
			Network:       c.PrimaryNetwork(),
			AdminURL:      "http://" + name + ":" + groupAdminPort,
		})
	}
	writeJSON(w, http.StatusOK, groups)
}

// containerDisplayName devolve o nome do container sem a barra inicial
// que a API do Docker sempre inclui em Names[0].
func containerDisplayName(c dockerclient.Container) string {
	if len(c.Names) > 0 {
		name := c.Names[0]
		if len(name) > 0 && name[0] == '/' {
			return name[1:]
		}
		return name
	}
	return c.ID
}

// syncInstanceStatus atualiza, de melhor esforço, o registo na store de UMA
// instância que corresponda a containerID -- usado depois de parar/remover
// um container por fora da store (por containerId, não pelo id de
// instância), já que nem todo container gerido por este launcher foi
// necessariamente lançado por ele (ex: group-authd, estático num
// docker-compose.yml, ou qualquer réplica de um group, nunca gravadas na
// store -- ver handleCreateReplica). Se nenhuma instância corresponder, ou
// a própria listagem falhar, não há nada a fazer e isso não é um erro.
func (h *Handler) syncInstanceStatus(ctx context.Context, containerID, status, subject string) {
	instances, err := h.store.List(ctx)
	if err != nil {
		return
	}
	for _, i := range instances {
		if i.ContainerID == containerID {
			_ = h.store.UpdateStatus(ctx, i.ID, status, "", subject)
			return
		}
	}
}

// handleDeleteGroup para e remove o container de UM autoscaler-group,
// identificado pelo containerId (não pelo id de instância deste
// launcher) -- diferente de handleRemoveContainer porque um group nem
// sempre foi lançado por este launcher (ex: group-authd, estático num
// docker-compose.yml, nunca teve uma linha na store). É esta rota,
// endereçável pelo mesmo containerId que GET /v1/groups já devolve, que
// o ecrã /admin/autoscaler do portal usa para "Parar grupo" -- o
// /admin/launcher deste portal deliberadamente não expõe este botão para
// instâncias kind="group" (ver LauncherAdminClient.tsx): matar um group é
// uma decisão que pertence ao ecrã que também mostra as suas
// réplicas/policy, não ao ecrã de lançamento (handleStopContainer e
// handleRemoveContainer, por isso, recusam-se a agir sobre um group --
// ver confirmManageable).
//
// Confirma primeiro, via label, que o id dado é mesmo um group -- sem
// isso, esta rota seria "parar/remover QUALQUER container por id", uma
// superfície de risco desnecessária.
func (h *Handler) handleDeleteGroup(w http.ResponseWriter, r *http.Request, subject, _ string) {
	containerID := r.PathValue("containerId")

	containers, err := h.docker.ListContainers(r.Context(), true, map[string][]string{
		"id":    {containerID},
		"label": {labelGroupRole + "=" + labelGroupRoleValue},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro confirmando group: "+err.Error())
		return
	}
	if len(containers) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "nenhum autoscaler-group com este container id: "+containerID)
		return
	}

	if err := h.docker.StopContainer(r.Context(), containerID, stopTimeoutSeconds); err != nil {
		writeError(w, http.StatusBadGateway, "stop_failed", "erro parando group: "+err.Error())
		return
	}
	if err := h.docker.RemoveContainer(r.Context(), containerID, false); err != nil {
		writeError(w, http.StatusBadGateway, "remove_failed", "erro removendo group: "+err.Error())
		return
	}

	h.syncInstanceStatus(r.Context(), containerID, store.StatusStopped, subject)
	w.WriteHeader(http.StatusNoContent)
}

// ReplicaSummary é uma linha de GET /v1/replicas: UMA réplica de aplicação
// viva, gerida por algum autoscaler-group -- descoberta contra o Docker
// pela mesma label que cada cmd/group injeta automaticamente no seu launch
// template (ver labelServiceKey), nunca contra a store deste launcher (que
// não sabe nada sobre réplicas -- ver handleCreateReplica). É isto que
// resolve réplicas "invisíveis" em /admin/launcher: antes desta rota, uma
// réplica só aparecia dentro do seu próprio group, em /admin/autoscaler.
type ReplicaSummary struct {
	ContainerID   string `json:"containerId"`
	Name          string `json:"name"`
	TargetService string `json:"targetService"`
	Image         string `json:"image"`
	State         string `json:"state"`
	Network       string `json:"network"`
}

func (h *Handler) handleListReplicas(w http.ResponseWriter, r *http.Request, _, _ string) {
	containers, err := h.docker.ListContainers(r.Context(), true, map[string][]string{
		"label": {labelServiceKey},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro listando réplicas: "+err.Error())
		return
	}

	replicas := make([]ReplicaSummary, 0, len(containers))
	for _, c := range containers {
		// Segunda camada de defesa, mesmo raciocínio de handleListAllContainers
		// -- o filtro acima já é específico à nossa convenção de labels
		// (nenhuma app não relacionada usaria a chave "autoscaler.service"),
		// mas nunca custa confirmar.
		if !hasPlatformLabel(c.Labels) {
			continue
		}
		replicas = append(replicas, ReplicaSummary{
			ContainerID:   c.ID,
			Name:          containerDisplayName(c),
			TargetService: c.Labels[labelServiceKey],
			Image:         c.Image,
			State:         c.State,
			Network:       c.PrimaryNetwork(),
		})
	}
	writeJSON(w, http.StatusOK, replicas)
}

// confirmManageable confirma que containerID é um container desta
// plataforma que NÃO é o processo de um autoscaler-group -- condição comum
// a handleStopContainer e handleRemoveContainer, ambos endereçáveis por
// QUALQUER containerId, então precisam desta verificação para não se
// tornarem "parar/remover QUALQUER container do host" nem "matar um group
// pela porta errada" (ver handleDeleteGroup, a única rota com permissão
// para isso). Escreve a resposta de erro e devolve false quando não pode
// prosseguir.
func (h *Handler) confirmManageable(w http.ResponseWriter, r *http.Request, containerID string) bool {
	containers, err := h.docker.ListContainers(r.Context(), true, map[string][]string{"id": {containerID}})
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro confirmando container: "+err.Error())
		return false
	}
	var found *dockerclient.Container
	for i := range containers {
		if containers[i].ID == containerID {
			found = &containers[i]
			break
		}
	}
	if found == nil || !hasPlatformLabel(found.Labels) {
		writeError(w, http.StatusNotFound, "not_found", "container desconhecido: "+containerID)
		return false
	}
	if found.Labels[labelGroupRole] == labelGroupRoleValue {
		writeError(w, http.StatusForbidden, "forbidden", "para parar/eliminar um autoscaler-group inteiro, use /admin/autoscaler")
		return false
	}
	return true
}

// handleStopContainer para (sem remover) um container desta plataforma,
// identificado pelo seu containerId -- funciona tanto para uma instância
// "solo" como para uma réplica de um group (nunca para o processo group em
// si, ver confirmManageable). Diferente de handleRemoveContainer: o
// container continua a existir, só parado -- é o que separa "Parar" de
// "Eliminar" em /admin/launcher.
func (h *Handler) handleStopContainer(w http.ResponseWriter, r *http.Request, subject, _ string) {
	containerID := r.PathValue("containerId")
	if !h.confirmManageable(w, r, containerID) {
		return
	}

	if err := h.docker.StopContainer(r.Context(), containerID, stopTimeoutSeconds); err != nil {
		writeError(w, http.StatusBadGateway, "stop_failed", "erro parando container: "+err.Error())
		return
	}

	h.syncInstanceStatus(r.Context(), containerID, store.StatusStopped, subject)
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveContainer para e remove um container desta plataforma,
// identificado pelo seu containerId -- mesma verificação de
// handleStopContainer. Se o container já estiver parado, StopContainer é
// um no-op no Docker, não um erro.
func (h *Handler) handleRemoveContainer(w http.ResponseWriter, r *http.Request, subject, _ string) {
	containerID := r.PathValue("containerId")
	if !h.confirmManageable(w, r, containerID) {
		return
	}

	if err := h.docker.StopContainer(r.Context(), containerID, stopTimeoutSeconds); err != nil {
		writeError(w, http.StatusBadGateway, "stop_failed", "erro parando container: "+err.Error())
		return
	}
	if err := h.docker.RemoveContainer(r.Context(), containerID, false); err != nil {
		writeError(w, http.StatusBadGateway, "remove_failed", "erro removendo container: "+err.Error())
		return
	}

	h.syncInstanceStatus(r.Context(), containerID, store.StatusStopped, subject)
	w.WriteHeader(http.StatusNoContent)
}

// networkNameRe é o charset aceite para o nome de uma rede nova -- mesmo
// padrão de nomes usados noutros sítios deste repo (ver
// services/templatesadmin/internal/httpapi.nameRe), suficiente para o
// que o Docker aceita sem abrir espaço a injeção de opções.
var networkNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// handleListNetworks devolve as redes Docker definidas pelo utilizador --
// é o que alimenta o <select> de rede na hora de lançar uma instância
// (ver internal/dockerclient.ListNetworks para o porquê de excluir as
// três redes automáticas do Docker).
func (h *Handler) handleListNetworks(w http.ResponseWriter, r *http.Request, _, _ string) {
	networks, err := h.docker.ListNetworks(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro listando redes: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, networks)
}

type createNetworkRequest struct {
	Name string `json:"name"`
}

// handleCreateNetwork cria uma rede Docker nova, sem nenhuma opção além
// do nome (ver internal/dockerclient.CreateNetwork) -- para quando se
// quer isolar um grupo de instâncias sem tocar na rede de mais nada.
func (h *Handler) handleCreateNetwork(w http.ResponseWriter, r *http.Request, _, _ string) {
	var req createNetworkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "corpo inválido: "+err.Error())
		return
	}
	if !networkNameRe.MatchString(req.Name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "nome inválido -- use só letras, números, \".\", \"_\" ou \"-\", até 64 caracteres")
		return
	}

	if _, err := h.docker.CreateNetwork(r.Context(), req.Name); err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro criando rede: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

// hasPlatformLabel confirma que um recurso (rede ou container) tem a
// label desta plataforma (ver dockerclient.PlatformLabel) -- é o que
// impede qualquer endpoint de rede de tocar em algo de outra app
// qualquer a partilhar o mesmo host Docker.
func hasPlatformLabel(labels map[string]string) bool {
	return labels[dockerclient.PlatformLabel] == dockerclient.PlatformLabelValue
}

// isPlatformContainer confirma, por label, que um container é desta
// plataforma -- usado antes de ligar/desligar um container a uma rede,
// para essa ação nunca alcançar um container de outra app qualquer.
func (h *Handler) isPlatformContainer(ctx context.Context, containerID string) (bool, error) {
	containers, err := h.docker.ListContainers(ctx, true, map[string][]string{"id": {containerID}})
	if err != nil {
		return false, err
	}
	for _, c := range containers {
		if c.ID == containerID {
			return hasPlatformLabel(c.Labels), nil
		}
	}
	return false, nil
}

// handleGetNetwork devolve uma rede e os containers atualmente ligados a
// ela -- é o que a página de gestão de rede usa como "detalhe". Devolve
// 404 igual a "não existe" se a rede não for desta plataforma -- não
// distinguir os dois casos evita confirmar a existência de uma rede de
// outra app qualquer no mesmo host.
func (h *Handler) handleGetNetwork(w http.ResponseWriter, r *http.Request, _, _ string) {
	name := r.PathValue("name")
	detail, err := h.docker.InspectNetwork(r.Context(), name)
	if err != nil || !hasPlatformLabel(detail.Labels) {
		writeError(w, http.StatusNotFound, "not_found", "rede desconhecida: "+name)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleDeleteNetwork apaga uma rede -- o Docker recusa (erro repassado
// tal como veio) se ainda houver algum container ligado a ela. Confirma
// primeiro que a rede é desta plataforma, mesmo raciocínio de
// handleGetNetwork.
func (h *Handler) handleDeleteNetwork(w http.ResponseWriter, r *http.Request, _, _ string) {
	name := r.PathValue("name")
	detail, err := h.docker.InspectNetwork(r.Context(), name)
	if err != nil || !hasPlatformLabel(detail.Labels) {
		writeError(w, http.StatusNotFound, "not_found", "rede desconhecida: "+name)
		return
	}

	if err := h.docker.RemoveNetwork(r.Context(), name); err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro apagando rede -- confirme que não tem nenhum container ligado: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type networkConnectionRequest struct {
	ContainerID string `json:"containerId"`
}

// handleConnectNetwork liga um container já em execução a uma rede, sem
// recriá-lo -- é isto que resolve, ao vivo, um container lançado sem a
// rede certa (ver resolveNetwork). Confirma que TANTO a rede COMO o
// container são desta plataforma -- sem isso, esta rota conseguiria
// puxar para dentro (ou empurrar para fora) um container de outra app
// qualquer a partilhar o mesmo host Docker.
func (h *Handler) handleConnectNetwork(w http.ResponseWriter, r *http.Request, _, _ string) {
	name := r.PathValue("name")
	var req networkConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ContainerID == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", `"containerId" é obrigatório`)
		return
	}

	detail, err := h.docker.InspectNetwork(r.Context(), name)
	if err != nil || !hasPlatformLabel(detail.Labels) {
		writeError(w, http.StatusNotFound, "not_found", "rede desconhecida: "+name)
		return
	}
	isPlatform, err := h.isPlatformContainer(r.Context(), req.ContainerID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro confirmando container: "+err.Error())
		return
	}
	if !isPlatform {
		writeError(w, http.StatusForbidden, "forbidden", "este container não pertence a esta plataforma")
		return
	}

	if err := h.docker.ConnectNetwork(r.Context(), name, req.ContainerID); err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro ligando container à rede: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDisconnectNetwork desliga um container de uma rede -- não o pára
// nem o remove, só corta essa ligação específica. Mesmas confirmações de
// handleConnectNetwork.
func (h *Handler) handleDisconnectNetwork(w http.ResponseWriter, r *http.Request, _, _ string) {
	name := r.PathValue("name")
	var req networkConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ContainerID == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", `"containerId" é obrigatório`)
		return
	}

	detail, err := h.docker.InspectNetwork(r.Context(), name)
	if err != nil || !hasPlatformLabel(detail.Labels) {
		writeError(w, http.StatusNotFound, "not_found", "rede desconhecida: "+name)
		return
	}
	isPlatform, err := h.isPlatformContainer(r.Context(), req.ContainerID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro confirmando container: "+err.Error())
		return
	}
	if !isPlatform {
		writeError(w, http.StatusForbidden, "forbidden", "este container não pertence a esta plataforma")
		return
	}

	if err := h.docker.DisconnectNetwork(r.Context(), name, req.ContainerID); err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro desligando container da rede: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ContainerSummary é uma linha de GET /v1/containers -- só os containers
// desta plataforma (ver dockerclient.PlatformLabel), nunca os de outra
// app qualquer a partilhar o mesmo host -- é o que alimenta o picker de
// "ligar este container a esta rede".
type ContainerSummary struct {
	ContainerID string   `json:"containerId"`
	Name        string   `json:"name"`
	Image       string   `json:"image"`
	State       string   `json:"state"`
	Networks    []string `json:"networks"`
}

func (h *Handler) handleListAllContainers(w http.ResponseWriter, r *http.Request, _, _ string) {
	containers, err := h.docker.ListContainers(r.Context(), true, map[string][]string{
		"label": {dockerclient.PlatformLabel + "=" + dockerclient.PlatformLabelValue},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_error", "erro listando containers: "+err.Error())
		return
	}

	out := make([]ContainerSummary, 0, len(containers))
	for _, c := range containers {
		out = append(out, ContainerSummary{
			ContainerID: c.ID,
			Name:        containerDisplayName(c),
			Image:       c.Image,
			State:       c.State,
			Networks:    c.NetworkNames(),
		})
	}
	writeJSON(w, http.StatusOK, out)
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

func newInstanceID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
