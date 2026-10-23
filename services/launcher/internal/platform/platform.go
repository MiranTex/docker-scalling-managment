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
	"log/slog"
	"regexp"
	"strings"

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
	CreateVolume(ctx context.Context, name string, labels map[string]string) error
	RemoveVolume(ctx context.Context, name string) error
	ListVolumesByLabel(ctx context.Context, label string) ([]dockerclient.Volume, error)
	InspectImage(ctx context.Context, ref string) (*dockerclient.ImageInspect, error)
	PullImage(ctx context.Context, ref string) error
}

type Config struct {
	// TraefikContainer é ligado a toda rede de ambiente, para conseguir
	// encaminhar tráfego para os serviços web lá dentro.
	TraefikContainer string
	// SelfContainer é o container deste launcher -- também ligado a toda
	// rede de ambiente, para fazer health checks às réplicas.
	SelfContainer string

	// Imagens dos addons -- Postgres 17 e Redis por defeito, trocáveis por
	// env (ex: para fixar uma minor version).
	PostgresImage string
	RedisImage    string

	// Capacidade declarada do host -- a mesma de internal/httpapi
	// (CapacityConfig). Zero desliga o teto.
	HostMemoryMB          int64
	MemoryOvercommitRatio float64
}

type Service struct {
	db     *store.DB
	docker Docker
	box    *secretbox.Box // nil = segredos desligados
	cfg    Config
	logger *slog.Logger
}

func New(db *store.DB, docker Docker, box *secretbox.Box, cfg Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, docker: docker, box: box, cfg: cfg, logger: logger}
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
		errors.Is(err, store.ErrAddonNotFound), errors.Is(err, store.ErrJobNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}
