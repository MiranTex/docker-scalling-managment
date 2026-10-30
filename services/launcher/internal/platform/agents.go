package platform

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// Nós remotos: cada um corre um agente (services/agent) que abre uma
// ligação ao launcher e a mantém. Por cima dela vai um yamux: o launcher
// abre um canal por pedido e o agente liga cada canal ao socket do Docker
// da sua máquina. O nó não abre portas nem expõe o Docker.
//
// Entrar: um admin cria um token de entrada (bsj_, uso único, 1 hora);
// "basestack node join" troca-o por uma credencial do nó (bsn_), que o
// agente usa daí em diante. Só se guardam hashes.

const (
	JoinTokenPrefix = "bsj_"
	NodeTokenPrefix = "bsn_"
	joinTokenTTL    = time.Hour
	agentKeepAlive  = 5 * time.Second
)

var ErrUnauthorizedNode = errors.New("credencial do nó inválida ou revogada")

type agentConn struct {
	session *yamux.Session
	docker  Docker // o do nó, pelo túnel
	version string

	mu sync.Mutex
	// swarmNodeID: o nó já está no swarm da plataforma (pode receber
	// apps); swarmErr: porque não entrou.
	swarmNodeID string
	swarmErr    string
}

func (c *agentConn) close() {
	if c.session != nil {
		_ = c.session.Close()
	}
}

func (c *agentConn) networked() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.swarmNodeID != "", c.swarmErr
}

type agents struct {
	mu    sync.Mutex
	m     map[string]*agentConn
	watch *nodeWatch // o que se sabe dos nós entre ligações (ver liveness.go)
}

func (a *agents) get(node string) *agentConn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m[node]
}

func (a *agents) names() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.m))
	for n := range a.m {
		out = append(out, n)
	}
	return out
}

// put troca a ligação de um nó (uma nova substitui a antiga, que se fecha).
func (a *agents) put(node string, c *agentConn) {
	a.mu.Lock()
	old := a.m[node]
	a.m[node] = c
	delete(a.watch.down, node)
	delete(a.watch.lost, node)
	a.mu.Unlock()
	if old != nil {
		old.close()
	}
}

func (a *agents) drop(node string, c *agentConn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c == nil || a.m[node] == c {
		if cur := a.m[node]; cur != nil {
			cur.close()
		}
		delete(a.m, node)
		a.watch.down[node] = time.Now()
	}
}

func randomToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("platform: sem entropia: %v", err))
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// JoinToken é um token de entrada acabado de criar (só se vê uma vez).
type JoinToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (s *Service) CreateJoinToken(ctx context.Context, by string) (JoinToken, error) {
	t := JoinToken{Token: randomToken(JoinTokenPrefix), ExpiresAt: time.Now().Add(joinTokenTTL).UTC()}
	if err := s.db.CreateNodeJoinToken(ctx, hashToken(t.Token), by, t.ExpiresAt); err != nil {
		return JoinToken{}, err
	}
	return t, nil
}

// JoinNode regista um nó remoto com um token de entrada e devolve a
// credencial do nó (só se vê aqui).
func (s *Service) JoinNode(ctx context.Context, joinToken, name, address string) (string, error) {
	if err := validSlug("nome do nó", name); err != nil {
		return "", err
	}
	if name == LocalNode {
		return "", invalid("o nome %q é o da máquina da plataforma -- escolhe outro (--name)", name)
	}
	nodeToken := randomToken(NodeTokenPrefix)
	err := s.db.JoinNode(ctx, hashToken(joinToken), store.Node{Name: name, Address: address}, hashToken(nodeToken))
	switch {
	case errors.Is(err, store.ErrJoinTokenInvalid):
		return "", fmt.Errorf("%w: o token de entrada não existe, expirou ou já foi usado -- cria outro no portal (Nós > Adicionar nó)", ErrUnauthorizedNode)
	case err != nil:
		return "", mapStoreErr(err)
	}
	s.logger.Info("nó registado", "node", name, "address", address)
	return nodeToken, nil
}

// AuthenticateNode encontra o nó de uma credencial de agente.
func (s *Service) AuthenticateNode(ctx context.Context, nodeToken string) (store.Node, error) {
	if !strings.HasPrefix(nodeToken, NodeTokenPrefix) {
		return store.Node{}, ErrUnauthorizedNode
	}
	n, err := s.db.NodeByToken(ctx, hashToken(nodeToken))
	if errors.Is(err, store.ErrNodeNotFound) {
		return store.Node{}, ErrUnauthorizedNode
	}
	return n, err
}

// ServeAgent usa a ligação do agente de node até ela cair: regista-o como
// ligado, atualiza o que a máquina reporta e o sinal de vida. Bloqueia.
func (s *Service) ServeAgent(ctx context.Context, node string, conn net.Conn, version, address string) error {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = discard{} // os erros chegam-nos pelas chamadas
	// Uma máquina que morre de repente não fecha a ligação: o keepalive é
	// que dá pela falta (por defeito só ao fim de ~30s, com as chamadas a
	// esse nó penduradas até lá).
	cfg.KeepAliveInterval = agentKeepAlive
	cfg.ConnectionWriteTimeout = agentKeepAlive
	session, err := yamux.Client(conn, cfg)
	if err != nil {
		return err
	}
	c := &agentConn{session: session, version: version}
	c.docker = dockerclient.NewWithDialer(func(context.Context) (net.Conn, error) { return session.Open() })
	s.agents.put(node, c)
	defer s.Kick() // o reconciliador vê já que o nó ficou sem sinal
	defer s.agents.drop(node, c)
	s.logger.Info("agente ligado", "node", node, "address", address, "version", version)
	defer s.logger.Info("agente desligado", "node", node)

	report := func() {
		info, err := c.docker.Info(ctx)
		if err != nil {
			s.logger.Error("lendo a máquina do nó", "node", node, "err", err)
			return
		}
		_ = s.db.UpdateNodeFacts(ctx, store.Node{
			Name: node, Address: address, OS: info.OSType, Arch: normalArch(info.Architecture), CPUs: info.NCPU,
			MemoryMB: info.MemTotal >> 20, DockerVersion: info.ServerVersion, AgentVersion: version,
		})
	}
	report()
	if s.clusterOn() {
		id, err := s.joinSwarm(ctx, node, c.docker, address)
		c.mu.Lock()
		c.swarmNodeID = id
		if err != nil {
			c.swarmErr = err.Error()
			s.logger.Error("o nó não entrou no swarm", "node", node, "err", err)
		}
		c.mu.Unlock()
	}
	// O reconciliador vê já o nó de volta (ex: réplicas que o Docker dele
	// repôs sem rede, ou que sobram porque já foram substituídas).
	s.Kick()
	tick := time.NewTicker(nodeHeartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-session.CloseChan():
			return nil
		case <-tick.C:
			if _, err := session.Ping(); err != nil {
				return err
			}
			if err := s.db.TouchNode(ctx, node); err != nil {
				s.logger.Error("sinal de vida do nó", "node", node, "err", err)
			}
		}
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// RemoveNode tira um nó remoto da plataforma: corta a ligação e apaga a
// credencial (o agente deixa de conseguir entrar).
func (s *Service) RemoveNode(ctx context.Context, name string) error {
	if name == LocalNode {
		return invalid("o nó local é a máquina da plataforma -- não se remove")
	}
	if err := s.db.DeleteNode(ctx, name); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			return fmt.Errorf("%w: nó %s", ErrNotFound, name)
		}
		return err
	}
	s.leaveSwarm(ctx, s.agents.get(name))
	s.agents.drop(name, nil)
	s.logger.Info("nó removido", "node", name)
	return nil
}

// CheckNode corre um container pequeno no nó, pelo caminho por onde iriam
// as réplicas, e devolve o output -- prova que o nó está utilizável.
func (s *Service) CheckNode(ctx context.Context, name string) (string, error) {
	if _, err := s.db.GetNode(ctx, name); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			return "", fmt.Errorf("%w: nó %s", ErrNotFound, name)
		}
		return "", err
	}
	d, err := s.dockerOn(name)
	if err != nil {
		return "", err
	}
	image := s.cfg.DebugImage
	if image == "" {
		image = defaultDebugImage
	}
	if _, err := d.InspectImage(ctx, image); err != nil {
		if err := d.PullImage(ctx, image); err != nil {
			return "", fmt.Errorf("buscando %s no nó %s: %w", image, name, err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	id, err := d.CreateContainer(ctx, "basestack-check-"+newID()[:6], dockerclient.CreateContainerRequest{
		Image:  image,
		Cmd:    []string{"sh", "-c", `echo "olá do nó $(hostname): $(uname -srm)"`},
		Labels: map[string]string{dockerclient.PlatformLabel: dockerclient.PlatformLabelValue, LabelNode: name, LabelRole: RoleJob},
		HostConfig: &dockerclient.CreateHostConfig{
			SecurityOpt: dockerclient.AppSecurityOpt(),
			Resources:   dockerclient.Resources{NanoCPUs: 250_000_000, Memory: 64 << 20, MemorySwap: 64 << 20},
		},
	})
	if err != nil {
		return "", fmt.Errorf("criando o container de teste: %w", err)
	}
	defer func() { _ = d.RemoveContainer(context.WithoutCancel(ctx), id, true) }()
	if err := d.StartContainer(ctx, id); err != nil {
		return "", fmt.Errorf("arrancando o container de teste: %w", err)
	}
	code, err := d.WaitContainer(ctx, id)
	if err != nil {
		return "", err
	}
	out, _ := d.ContainerLogs(ctx, id, 20)
	if code != 0 {
		return out, fmt.Errorf("o container de teste terminou com %d", code)
	}
	return strings.TrimSpace(out), nil
}
