package platform

import (
	"context"
	"fmt"
	"maps"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// O launcher só vê um nó remoto pelo túnel do agente. Túnel em baixo não
// quer dizer réplicas perdidas: pode ser um corte breve, ou o launcher que
// acabou de arrancar e o agente ainda não voltou. Por isso:
//
//   - ligado: vê-se o que lá corre;
//   - sem sinal (menos de nodeLossGrace desde que caiu, ou desde que o
//     launcher arrancou): as réplicas que lá se viram da última vez
//     continuam a contar ("fantasmas"), com o health check feito pela
//     overlay -- que não depende do túnel. Se nunca se viram (launcher
//     acabado de arrancar), o reconciliador e o routing esperam;
//   - perdido: as réplicas dele deixam de contar e o reconciliador
//     substitui-as noutros nós. Se o nó voltar, as que lá ficaram são as
//     mais antigas e saem (ver reconcileService).

const (
	nodeLossGrace = 60 * time.Second
	// LabelPhantom marca, só em memória, uma réplica de um nó sem sinal
	// (phantomSilent: conta) ou perdido (phantomLost: já não conta -- as
	// substitutas são criadas -- mas, se ainda responder pela overlay, serve
	// até haver uma substituta pronta; ver buildRouting).
	LabelPhantom  = "base-stack.phantom"
	phantomSilent = "silent"
	phantomLost   = "lost"
	// lostKeep: quanto tempo depois de perdido um nó ainda se guardam as
	// réplicas dele para o routing.
	lostKeep = 10 * time.Minute
	// deadAfter: um nó sem sinal cujas réplicas já não respondem pela
	// overlay morreu mesmo -- não se espera pelo nodeLossGrace (esse prazo
	// é para um túnel que cai com a máquina viva).
	deadAfter = 15 * time.Second
)

// allUnready: há réplicas e nenhuma passa no health check (as dos
// workers, que não se verificam, ficam como estavam -- prontas).
func allUnready(rt *runtime, seen []dockerclient.Container) bool {
	if len(seen) == 0 {
		return false
	}
	for _, c := range seen {
		if rt.isReady(containerName(c)) {
			return false
		}
	}
	return true
}

type liveness int

const (
	nodeUp liveness = iota
	nodeSilent
	nodeLost
)

// nodeWatch é o que o launcher sabe dos nós remotos entre passagens.
type nodeWatch struct {
	started time.Time
	down    map[string]time.Time                // nó -> quando o agente caiu
	seen    map[string][]dockerclient.Container // nó -> réplicas vistas da última vez
	lost    map[string]bool                     // nó -> já dado como perdido
}

func newNodeWatch() *nodeWatch {
	return &nodeWatch{started: time.Now(), down: map[string]time.Time{}, seen: map[string][]dockerclient.Container{}, lost: map[string]bool{}}
}

func (a *agents) liveness(node string, now time.Time) (liveness, time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m[node] != nil {
		return nodeUp, 0
	}
	if a.watch.lost[node] {
		return nodeLost, now.Sub(a.watch.down[node])
	}
	since, ok := a.watch.down[node]
	if !ok {
		since = a.watch.started
	}
	silent := now.Sub(since)
	if silent < nodeLossGrace {
		return nodeSilent, silent
	}
	return nodeLost, silent
}

// appReplicas lista as réplicas de todos os nós: as que se veem e as
// fantasmas dos nós sem sinal. unknown: há um nó sem sinal de que nunca se
// viu nada -- melhor não decidir já.
func (s *Service) appReplicas(ctx context.Context, all bool) (out []dockerclient.Container, unknown bool, err error) {
	out, err = s.docker.ListContainers(ctx, all, map[string][]string{"label": {LabelRole + "=" + RoleApp}})
	if err != nil {
		return nil, false, err
	}
	nodes, err := s.db.ListNodes(ctx)
	if err != nil {
		return nil, false, err
	}
	byNode := map[string][]dockerclient.Container{}
	for _, c := range out {
		if c.State == "running" || c.State == "restarting" {
			byNode[nodeOf(c)] = append(byNode[nodeOf(c)], c)
		}
	}
	now := time.Now()
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	w := s.agents.watch
	for _, n := range nodes {
		if n.Kind != store.NodeAgent {
			continue
		}
		if s.agents.m[n.Name] != nil {
			w.seen[n.Name] = byNode[n.Name]
			continue
		}
		since, ok := w.down[n.Name]
		if !ok {
			since = w.started
		}
		seen, known := w.seen[n.Name]
		ghosts := func(kind string) {
			for _, c := range seen {
				ghost := c
				ghost.Labels = maps.Clone(c.Labels)
				ghost.Labels[LabelPhantom] = kind
				out = append(out, ghost)
			}
		}
		switch {
		case now.Sub(since) >= nodeLossGrace+lostKeep:
			// Já houve tempo de sobra para as substitutas: esquece-as.
			for _, c := range seen {
				s.rt.forget(containerName(c))
			}
			delete(w.seen, n.Name)
		case now.Sub(since) >= nodeLossGrace || w.lost[n.Name] ||
			now.Sub(since) >= deadAfter && known && allUnready(s.rt, seen):
			if !w.lost[n.Name] {
				w.lost[n.Name] = true
				s.logger.Warn("nó perdido -- as réplicas dele vão ser substituídas", "node", n.Name, "replicas", len(seen))
			}
			ghosts(phantomLost)
		case !known:
			unknown = true
		default:
			ghosts(phantomSilent)
		}
	}
	return out, unknown, nil
}

func isPhantom(c dockerclient.Container) bool { return c.Labels[LabelPhantom] != "" }

func isLostPhantom(c dockerclient.Container) bool { return c.Labels[LabelPhantom] == phantomLost }

// probePhantom: o health check de uma réplica de um nó sem sinal, só pela
// rede (o Docker dela não se alcança). Um worker não tem como ser
// verificado -- fica como estava.
func (s *Service) probePhantom(ctx context.Context, c dockerclient.Container, rev store.Revision) {
	if rev.Port == 0 {
		return
	}
	s.rt.observeSuspect(containerName(c), s.probe(ctx, replicaAddr(c), rev.Port, rev.HealthPath) == nil)
}

// nodeStatus é o estado de um nó para a UI.
func (s *Service) nodeStatus(n store.Node) string {
	if n.Kind != store.NodeAgent {
		return "online"
	}
	switch l, silent := s.agents.liveness(n.Name, time.Now()); l {
	case nodeUp:
		return "online"
	case nodeSilent:
		return fmt.Sprintf("sem sinal há %ds", int(silent.Seconds()))
	default:
		return "perdido"
	}
}
