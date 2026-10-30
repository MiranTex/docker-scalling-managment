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

// dockerOn é o Docker de um nó: o local, ou o de um nó remoto pelo túnel
// do seu agente.
func (s *Service) dockerOn(node string) (Docker, error) {
	if node == LocalNode {
		return s.local, nil
	}
	if c := s.agents.get(node); c != nil {
		return c.docker, nil
	}
	return nil, fmt.Errorf("o nó %s está offline (o agente não está ligado)", node)
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
	info, err := s.local.Info(ctx)
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
	// Network: "cluster" (está no swarm, recebe apps), "local", ou o
	// motivo por que um nó remoto ainda não está na rede.
	Network string `json:"network"`
	// Online: o local dá sinal pelo launcher; um remoto, pelo agente ligado.
	Online bool `json:"online"`
	// Draining/Drain: a ser esvaziado agora; o estado do último esvaziar.
	Draining bool   `json:"draining"`
	Drain    string `json:"drain,omitempty"`
	// Status: "online", "sem sinal há Ns" ou "perdido" (ver liveness.go).
	Status    string  `json:"status"`
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
		online := time.Since(n.LastSeenAt) < nodeOfflineAfter
		if n.Kind == store.NodeAgent {
			online = s.agents.get(n.Name) != nil
		}
		out[i] = NodeView{Node: n, Online: online, Status: s.nodeStatus(n), Capacity: s.nodeCapacityMB(n), Network: s.nodeNetwork(n)}
		out[i].Draining, out[i].Drain = s.rt.nodeDrainState(n.Name)
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

// nodeNetwork diz se um nó tem a rede dos ambientes (e pode receber apps).
func (s *Service) nodeNetwork(n store.Node) string {
	switch {
	case n.Kind != store.NodeAgent:
		return "local"
	case !s.clusterOn():
		return "sem modo cluster na plataforma"
	}
	c := s.agents.get(n.Name)
	if c == nil {
		return "offline"
	}
	if ok, why := c.networked(); ok {
		return "cluster"
	} else if why != "" {
		return why
	}
	return "a entrar no swarm"
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
	Node   store.Node
	Online bool
	// Network: "local", "cluster" ou o motivo por que não tem a rede.
	Network   string
	Allocated Usage
	Capacity  float64
	// Siblings: réplicas da mesma revisão que já correm neste nó.
	Siblings int
}

// schedule escolhe o nó para uma réplica: online, a aceitar réplicas, com
// a arquitetura da imagem e memória livre; entre esses, o com menos
// réplicas do mesmo serviço (espalhar: a morte de uma máquina não leva
// todas) e, a seguir, o com mais memória livre. Sem nenhum, o erro diz
// porque cada um ficou de fora.
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
		case c.Network != "local" && c.Network != "cluster":
			// Sem a rede do ambiente, uma app neste nó não chegava aos
			// addons nem o Traefik a ela.
			why = append(why, c.Node.Name+": sem rede para apps ("+c.Network+")")
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
	sort.SliceStable(ok, func(i, j int) bool {
		if ok[i].Siblings != ok[j].Siblings {
			return ok[i].Siblings < ok[j].Siblings
		}
		return free(ok[i]) > free(ok[j])
	})
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
	siblings := map[string]int{}
	if list, err := s.docker.ListContainers(ctx, false, map[string][]string{"label": {LabelRevision + "=" + rev.ID}}); err == nil {
		for _, c := range list {
			if _, draining := s.rt.drainDeadline(containerName(c)); !draining {
				siblings[nodeOf(c)]++
			}
		}
	}
	cands := make([]candidate, len(nodes))
	for i, n := range nodes {
		cands[i] = candidate{Node: n.Node, Online: n.Online, Network: n.Network, Allocated: n.Allocated, Capacity: n.Capacity, Siblings: siblings[n.Name]}
	}
	return schedule(cands, placement{VCPU: rev.VCPU, MemoryMB: rev.MemoryMB, Platforms: s.imagePlatforms(ctx, rev.Image)})
}

// imagePlatforms: a plataforma da imagem como está no Docker local (onde
// o deploy a buscou). Uma imagem de várias arquiteturas aparece com a
// desta máquina; com nós remotos, isto passa a ler o índice no registry.
func (s *Service) imagePlatforms(ctx context.Context, image string) []string {
	info, err := s.local.InspectImage(ctx, image)
	if err != nil || info.Os == "" || info.Architecture == "" {
		return nil
	}
	return []string{info.Os + "/" + normalArch(info.Architecture)}
}
