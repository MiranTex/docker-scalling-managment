package platform

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// Nós: as máquinas onde correm os containers dos projetos. Para já só há o
// "local" -- a máquina do launcher, registada sozinha com o que o Docker
// dela diz --, mas cada réplica já é colocada num nó pelo escalonador e
// leva a label do nó; os nós remotos entram com o agente.

const (
	// LocalNode é a máquina do launcher. Containers sem LabelNode (de antes
	// de haver nós) são dela.
	LocalNode = "local"
	LabelNode = "base-stack.node"

	// nodeOfflineAfter: um nó que não dá sinal há mais do que isto não
	// recebe réplicas novas.
	nodeOfflineAfter = 2 * time.Minute
	nodeHeartbeat    = 30 * time.Second
)

// ErrNoNode: nenhum nó pode receber uma réplica (offline, em manutenção,
// sem a arquitetura da imagem ou sem memória) -- a mensagem diz porquê.
var ErrNoNode = errors.New("nenhum nó pode receber a réplica")

// nodeOf diz em que nó corre um container.
func nodeOf(c dockerclient.Container) string {
	if n := c.Labels[LabelNode]; n != "" {
		return n
	}
	return LocalNode
}

// dockerOn é o Docker de um nó. Só o local tem um, por agora.
func (s *Service) dockerOn(node string) (Docker, error) {
	if node == LocalNode {
		return s.docker, nil
	}
	return nil, fmt.Errorf("o nó %s ainda não aceita containers (falta o agente)", node)
}

// normalArch traduz a arquitetura do kernel (docker info) para a das
// imagens.
func normalArch(a string) string {
	switch a {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return a
}

// registerLocalNode grava (ou refresca) o nó local com o que o Docker diz.
// Corre no arranque e a cada nodeHeartbeat.
func (s *Service) registerLocalNode(ctx context.Context) {
	info, err := s.docker.Info(ctx)
	if err != nil {
		s.logger.Error("lendo a máquina local para o registo do nó", "err", err)
		return
	}
	n := store.Node{
		Name: LocalNode, OS: info.OSType, Arch: normalArch(info.Architecture), CPUs: info.NCPU,
		MemoryMB: info.MemTotal >> 20, DockerVersion: info.ServerVersion,
	}
	if err := s.db.PutNodeFacts(ctx, n); err != nil {
		s.logger.Error("registando o nó local", "err", err)
	}
}

// NodeView é um nó para a UI: o que reporta, se está online e o que tem
// alocado (somado das labels de recursos, como as quotas).
type NodeView struct {
	store.Node
	Online    bool    `json:"online"`
	Allocated Usage   `json:"allocated"`
	Replicas  int     `json:"replicas"`
	Addons    int     `json:"addons"`
	Capacity  float64 `json:"capacityMemoryMb"` // memória que o escalonador usa (com overcommit)
}

func (s *Service) ListNodes(ctx context.Context) ([]NodeView, error) {
	nodes, err := s.db.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	containers, err := s.docker.ListContainers(ctx, false, map[string][]string{"label": {dockerclient.PlatformLabel + "=" + dockerclient.PlatformLabelValue}})
	if err != nil {
		return nil, fmt.Errorf("listando containers para os nós: %w", err)
	}
	byNode := map[string]*NodeView{}
	out := make([]NodeView, len(nodes))
	for i, n := range nodes {
		out[i] = NodeView{Node: n, Online: time.Since(n.LastSeenAt) < nodeOfflineAfter, Capacity: s.nodeCapacityMB(n)}
		byNode[n.Name] = &out[i]
	}
	for _, c := range containers {
		v := byNode[nodeOf(c)]
		if v == nil {
			continue
		}
		// Tudo o que a plataforma criou conta para a capacidade (também as
		// instâncias do fluxo antigo, como na quota do host).
		addUsage(&v.Allocated, c)
		switch c.Labels[LabelRole] {
		case RoleApp:
			v.Replicas++
		case RoleAddon:
			v.Addons++
		}
	}
	return out, nil
}

func addUsage(u *Usage, c dockerclient.Container) {
	mem, err := strconv.ParseInt(c.Labels[dockerclient.LabelMemoryMB], 10, 64)
	if err != nil {
		return
	}
	vcpu, _ := strconv.ParseFloat(c.Labels[dockerclient.LabelVCPU], 64)
	u.VCPU += vcpu
	u.MemoryMB += mem
	u.Containers++
}

// SetNodeSchedulable liga/desliga a colocação de réplicas novas num nó.
func (s *Service) SetNodeSchedulable(ctx context.Context, name string, schedulable bool) (NodeView, error) {
	if err := s.db.SetNodeSchedulable(ctx, name, schedulable); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			return NodeView{}, fmt.Errorf("%w: nó %s", ErrNotFound, name)
		}
		return NodeView{}, err
	}
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return NodeView{}, err
	}
	for _, n := range nodes {
		if n.Name == name {
			return n, nil
		}
	}
	return NodeView{}, fmt.Errorf("%w: nó %s", ErrNotFound, name)
}

// nodeCapacityMB é a memória que o escalonador pode alocar num nó: a que
// ele tem vezes o overcommit. No local, LAUNCHER_HOST_MEMORY_MB (se
// definido) manda, como sempre mandou.
func (s *Service) nodeCapacityMB(n store.Node) float64 {
	ratio := s.cfg.MemoryOvercommitRatio
	if ratio <= 0 {
		ratio = 1
	}
	mem := n.MemoryMB
	if n.Name == LocalNode && s.cfg.HostMemoryMB > 0 {
		mem = s.cfg.HostMemoryMB
	}
	return float64(mem) * ratio
}

// placement é o que uma réplica nova precisa de um nó.
type placement struct {
	VCPU     float64
	MemoryMB int64
	// Platforms em que a imagem existe (ex: "linux/arm64"); vazio = não se
	// sabe, serve qualquer nó.
	Platforms []string
}

type candidate struct {
	Node      store.Node
	Online    bool
	Allocated Usage
	Capacity  float64
}

// schedule escolhe o nó para uma réplica: online, a aceitar réplicas, com
// a arquitetura da imagem e memória livre; entre esses, o com mais
// memória livre. Sem nenhum, o erro diz porque cada um ficou de fora.
func schedule(cands []candidate, p placement) (string, error) {
	var ok []candidate
	var why []string
	for _, c := range cands {
		platform := c.Node.OS + "/" + c.Node.Arch
		switch {
		case !c.Online:
			why = append(why, c.Node.Name+": offline")
		case !c.Node.Schedulable:
			why = append(why, c.Node.Name+": em manutenção")
		case len(p.Platforms) > 0 && !hasPlatform(p.Platforms, platform):
			why = append(why, fmt.Sprintf("%s: a imagem não tem %s (tem %s)", c.Node.Name, platform, strings.Join(p.Platforms, ", ")))
		case float64(c.Allocated.MemoryMB+p.MemoryMB) > c.Capacity:
			why = append(why, fmt.Sprintf("%s: %d MB alocados + %d MB pedidos > %.0f MB", c.Node.Name, c.Allocated.MemoryMB, p.MemoryMB, c.Capacity))
		default:
			ok = append(ok, c)
		}
	}
	if len(ok) == 0 {
		if len(why) == 0 {
			why = append(why, "não há nós registados")
		}
		return "", fmt.Errorf("%w: %s", ErrNoNode, strings.Join(why, "; "))
	}
	free := func(c candidate) float64 { return c.Capacity - float64(c.Allocated.MemoryMB) }
	sort.SliceStable(ok, func(i, j int) bool { return free(ok[i]) > free(ok[j]) })
	return ok[0].Node.Name, nil
}

func hasPlatform(list []string, want string) bool {
	for _, p := range list {
		// "linux/arm64/v8" serve um nó linux/arm64.
		if p == want || strings.HasPrefix(p, want+"/") {
			return true
		}
	}
	return false
}

// placeReplica escolhe o nó de uma réplica nova da revisão.
func (s *Service) placeReplica(ctx context.Context, rev store.Revision) (string, error) {
	nodes, err := s.ListNodes(ctx)
	if err == nil && len(nodes) == 0 {
		// O registo do arranque falhou ou ainda não correu.
		s.registerLocalNode(ctx)
		nodes, err = s.ListNodes(ctx)
	}
	if err != nil {
		return "", err
	}
	cands := make([]candidate, len(nodes))
	for i, n := range nodes {
		cands[i] = candidate{Node: n.Node, Online: n.Online, Allocated: n.Allocated, Capacity: n.Capacity}
	}
	return schedule(cands, placement{VCPU: rev.VCPU, MemoryMB: rev.MemoryMB, Platforms: s.imagePlatforms(ctx, rev.Image)})
}

// imagePlatforms: a plataforma da imagem como está no Docker local (onde
// o deploy a buscou). Uma imagem de várias arquiteturas aparece com a
// desta máquina; com nós remotos, isto passa a ler o índice no registry.
func (s *Service) imagePlatforms(ctx context.Context, image string) []string {
	info, err := s.docker.InspectImage(ctx, image)
	if err != nil || info.Os == "" || info.Architecture == "" {
		return nil
	}
	return []string{info.Os + "/" + normalArch(info.Architecture)}
}
