// Package platform é o modelo de projetos da plataforma: conta -> projetos
// -> ambientes, e dentro de cada ambiente os serviços, addons, segredos,
// jobs e domínios. Cada ambiente é uma fronteira de isolamento real: rede
// Docker própria, segredos próprios, containers com labels próprias.
//
// Este pacote decide e executa (store + Docker); internal/platformapi só
// traduz HTTP para chamadas daqui.
package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/secretbox"
	"launcher/internal/store"
)

// Labels que identificam a que projeto/ambiente pertence cada recurso
// Docker. Os slugs são para humanos e observabilidade (Loki/Prometheus);
// os ids são o que o código usa para encontrar recursos, porque não mudam.
const (
	LabelProject       = "base-stack.project"
	LabelEnvironment   = "base-stack.env"
	LabelProjectID     = "base-stack.project-id"
	LabelEnvironmentID = "base-stack.environment-id"
	// LabelRole distingue o que corre num ambiente: app, addon ou job.
	LabelRole  = "base-stack.role"
	LabelAddon = "base-stack.addon"
	LabelJob   = "base-stack.job"

	RoleApp   = "app"
	RoleAddon = "addon"
	RoleJob   = "job"
)

var (
	// ErrInvalid é um pedido mal formado -- a mensagem diz porquê.
	ErrInvalid  = errors.New("pedido inválido")
	ErrNotFound = errors.New("não encontrado")
	ErrConflict = errors.New("já existe")
	// ErrSecretsDisabled: sem LAUNCHER_SECRETS_KEY não há onde guardar
	// segredos, e é melhor recusar do que guardar em claro.
	ErrSecretsDisabled = errors.New("segredos de ambiente desligados: LAUNCHER_SECRETS_KEY não está configurada")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// slugRe vale para projetos, ambientes, serviços e addons: entra em nomes
// de redes, containers, volumes e hostnames, por isso só [a-z0-9-].
var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,28}[a-z0-9]$`)

func validSlug(kind, slug string) error {
	if !slugRe.MatchString(slug) {
		return invalid("%s %q inválido -- 2 a 30 caracteres, letras minúsculas, números e \"-\", a começar por letra", kind, slug)
	}
	return nil
}

// reservedHostPrefix: os nomes com que o launcher chega à plataforma
// (basestack-database, basestack-auth...). Serviços e addons são nomes DNS
// nas redes dos ambientes, onde o launcher também está -- não podem
// começar assim.
const reservedHostPrefix = "basestack-"

// validHostSlug é validSlug para o que vira nome DNS num ambiente.
func validHostSlug(kind, slug string) error {
	if err := validSlug(kind, slug); err != nil {
		return err
	}
	if strings.HasPrefix(slug, reservedHostPrefix) {
		return invalid("%s %q inválido -- os nomes começados por %q são da plataforma", kind, slug, reservedHostPrefix)
	}
	return nil
}

// Docker é o subconjunto do cliente Docker que este pacote usa.
type Docker interface {
	CreateLabeledNetwork(ctx context.Context, name string, labels map[string]string) (string, error)
	RemoveNetwork(ctx context.Context, id string) error
	ConnectNetwork(ctx context.Context, networkID, containerID string) error
	DisconnectNetwork(ctx context.Context, networkID, containerID string) error
	InspectNetwork(ctx context.Context, id string) (*dockerclient.NetworkDetail, error)
	ListContainers(ctx context.Context, all bool, filters map[string][]string) ([]dockerclient.Container, error)
	StopContainer(ctx context.Context, id string, timeoutSeconds int) error
	RemoveContainer(ctx context.Context, id string, force bool) error
	CreateContainer(ctx context.Context, name string, req dockerclient.CreateContainerRequest) (string, error)
	StartContainer(ctx context.Context, id string) error
	InspectContainer(ctx context.Context, id string) (*dockerclient.ContainerInspect, error)
	WaitContainer(ctx context.Context, id string) (int, error)
	ContainerLogs(ctx context.Context, id string, tail int) (string, error)
	CopyToContainer(ctx context.Context, id, dir string, archive io.Reader) error
	CreateVolume(ctx context.Context, name string, labels map[string]string) error
	RemoveVolume(ctx context.Context, name string) error
	ListVolumesByLabel(ctx context.Context, label string) ([]dockerclient.Volume, error)
	InspectImage(ctx context.Context, ref string) (*dockerclient.ImageInspect, error)
	PullImage(ctx context.Context, ref string) error
	PullImageWithAuth(ctx context.Context, ref string, auth *dockerclient.RegistryAuth) error
	ContainerStats(ctx context.Context, id string) (*dockerclient.Stats, error)
	VolumesWithUsage(ctx context.Context) ([]dockerclient.Volume, error)
	CreateExec(ctx context.Context, containerID string, cfg dockerclient.ExecConfig) (string, error)
	StartExec(ctx context.Context, execID string, tty bool) (io.ReadWriteCloser, error)
	StartExecDetached(ctx context.Context, execID string) error
	ResizeExec(ctx context.Context, execID string, rows, cols int) error
	InspectExec(ctx context.Context, execID string) (*dockerclient.ExecInspect, error)
	Info(ctx context.Context) (*dockerclient.SystemInfo, error)
	CreateNetworkWith(ctx context.Context, name string, o dockerclient.NetworkOptions) (string, error)
	ConnectNetworkAliases(ctx context.Context, networkID, containerID string, aliases []string) error
	SwarmInit(ctx context.Context, advertise string) error
	SwarmWorkerToken(ctx context.Context) (string, error)
	SwarmJoin(ctx context.Context, advertise, manager, token string) error
	SwarmLeave(ctx context.Context) error
	RemoveSwarmNode(ctx context.Context, id string) error
	InspectSwarmNode(ctx context.Context, id string) error
}

type Config struct {
	// TraefikContainer é ligado a toda rede de ambiente, para conseguir
	// encaminhar tráfego para os serviços web lá dentro.
	TraefikContainer string
	// SelfContainer é o container deste launcher -- também ligado a toda
	// rede de ambiente, para fazer health checks às réplicas.
	SelfContainer string
	// PrometheusContainer é ligado a toda rede de ambiente para recolher as
	// métricas das apps -- as apps nunca saem da sua rede. Vazio desliga.
	PrometheusContainer string

	// Imagens dos addons -- Postgres 17, Redis e SeaweedFS (storage) por
	// defeito, trocáveis por env (ex: para fixar uma minor version).
	PostgresImage string
	RedisImage    string
	StorageImage  string
	// DebugImage é a do container do modo debug do terminal (vazio =
	// busybox).
	DebugImage string

	// Capacidade declarada do host -- a mesma de internal/httpapi
	// (CapacityConfig). Zero desliga o teto.
	HostMemoryMB          int64
	MemoryOvercommitRatio float64

	// Routing: o launcher escreve a configuração dinâmica do Traefik
	// (file provider) neste diretório. Vazio desliga o routing.
	TraefikDynamicDir string
	// PublicBaseDomain dá a cada serviço web o host
	// <serviço>-<ambiente>-<projeto>.<domínio>.
	PublicBaseDomain string
	// TLSCertResolver (ex: "le") liga HTTPS com certificado automático.
	TLSCertResolver string
	// TraefikCertificates entram no routing do Traefik (ver routing.go).
	TraefikCertificates []TLSCertificate

	// RegistryHost é o registry da plataforma (registry.<domínio>): as
	// imagens de lá buscam-se com RegistryPullPassword, a credencial
	// interna do launcher, e só as do próprio projeto.
	RegistryHost         string
	RegistryPullPassword string

	// AgentImage é a imagem do agente dos nós; a versão dela (label) diz
	// se um agente está desatualizado.
	AgentImage string

	// Cluster liga o swarm e as redes overlay (ver swarm.go); AdvertiseAddr
	// é o IP desta máquina na rede, por onde os nós chegam ao manager.
	Cluster       bool
	AdvertiseAddr string

	// PublicIPs são os IPs públicos deste servidor -- um domínio próprio só
	// fica ativo quando o DNS dele resolve para um destes.
	PublicIPs []string

	// Só para testes: encurtam as esperas do deploy.
	readyPoll        time.Duration
	drainDelay       time.Duration
	workerReadyAfter time.Duration
}

type Service struct {
	db *store.DB
	// docker é o do cluster (ver cluster.go): o local mais os nós remotos.
	docker Docker
	// local é só o desta máquina (swarm, e o nó "local" do escalonador).
	local  Docker
	box    *secretbox.Box // nil = segredos desligados
	cfg    Config
	logger *slog.Logger
	rt     *runtime
	agents *agents // ligações dos agentes dos nós remotos
	swarm  swarmState
	// probe faz o health check HTTP/TCP a uma réplica -- substituível nos
	// testes.
	probe func(ctx context.Context, host string, port int, path string) error
	// lookupHost resolve DNS -- substituível nos testes.
	lookupHost func(ctx context.Context, host string) ([]string, error)
	// reconcileMu: uma passagem do reconciliador e a remoção de um
	// ambiente ou addon nunca correm ao mesmo tempo -- senão o
	// reconciliador recria, a meio da limpeza, o que está a ser apagado.
	reconcileMu sync.Mutex
}

func New(db *store.DB, docker Docker, box *secretbox.Box, cfg Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	ag := &agents{m: map[string]*agentConn{}, watch: newNodeWatch()}
	return &Service{
		db: db, docker: newClusterDocker(docker, ag), local: docker, box: box, cfg: cfg, logger: logger, rt: newRuntime(), agents: ag,
		probe: defaultProbe, lookupHost: net.DefaultResolver.LookupHost,
	}
}

// isNotFound reconhece o 404 do Docker (container/imagem/volume que não existe).
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "status 404")
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("platform: sem entropia para gerar ids: %v", err))
	}
	return hex.EncodeToString(buf)
}

// mapStoreErr converte os erros "esperados" da store nos deste pacote,
// para a camada HTTP só precisar de conhecer um conjunto.
func mapStoreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	case errors.Is(err, store.ErrProjectNotFound), errors.Is(err, store.ErrEnvironmentNotFound), errors.Is(err, store.ErrSecretNotFound),
		errors.Is(err, store.ErrAddonNotFound), errors.Is(err, store.ErrJobNotFound),
		errors.Is(err, store.ErrServiceNotFound), errors.Is(err, store.ErrRevisionNotFound),
		errors.Is(err, store.ErrDeploymentNotFound), errors.Is(err, store.ErrRegistryNotFound),
		errors.Is(err, store.ErrDeployTokenNotFound),
		errors.Is(err, store.ErrTemplateNotFound), errors.Is(err, store.ErrTemplateVersionNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}
