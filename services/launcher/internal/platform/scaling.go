package platform

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

const (
	// autoscaleEvery: amostrar CPU custa ~1s por container, não vale a
	// pena a cada tick do reconciliador.
	autoscaleEvery = 30 * time.Second
	// scaleCooldown evita oscilar: depois de mudar, espera antes de voltar
	// a decidir (o tráfego precisa de tempo para se redistribuir).
	scaleCooldown = 90 * time.Second
)

// targetReplicas é quantas réplicas o serviço deve ter agora.
func (s *Service) targetReplicas(ctx context.Context, svc store.Service) int {
	if svc.ScalingMode == store.ScalingFixed {
		return svc.Replicas
	}
	s.rt.mu.Lock()
	d, ok := s.rt.desired[svc.ID]
	s.rt.mu.Unlock()
	if !ok {
		d = svc.MinReplicas
	}
	return clamp(d, svc.MinReplicas, svc.MaxReplicas)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// autoscale decide +1/-1 réplica pela CPU média das réplicas prontas,
// medida em relação ao que cada uma tem alocado (50% de um core numa
// réplica de 0.5 vCPU = 100%).
func (s *Service) autoscale(ctx context.Context, svc store.Service, rev store.Revision, ready []string) {
	if svc.ScalingMode != store.ScalingAuto || len(ready) == 0 {
		return
	}
	now := time.Now()
	s.rt.mu.Lock()
	if now.Sub(s.rt.lastEval[svc.ID]) < autoscaleEvery || now.Sub(s.rt.lastScale[svc.ID]) < scaleCooldown {
		s.rt.mu.Unlock()
		return
	}
	s.rt.lastEval[svc.ID] = now
	current, ok := s.rt.desired[svc.ID]
	s.rt.mu.Unlock()
	if !ok {
		current = clamp(len(ready), svc.MinReplicas, svc.MaxReplicas)
	}

	avg := s.averageCPU(ctx, ready, rev.VCPU)
	next := current
	switch {
	case avg > svc.CPUScaleUpPercent && current < svc.MaxReplicas:
		next = current + 1
	case avg < svc.CPUScaleDownPercent && current > svc.MinReplicas:
		next = current - 1
	}

	s.rt.mu.Lock()
	s.rt.desired[svc.ID] = next
	if next != current {
		s.rt.lastScale[svc.ID] = now
	}
	s.rt.mu.Unlock()
	if next != current {
		s.logger.Info("autoscaling", "service", svc.Slug, "cpu_percent", avg, "from", current, "to", next)
	}
}

func (s *Service) averageCPU(ctx context.Context, names []string, vcpu float64) float64 {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total float64
		n     int
	)
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			st, err := s.docker.ContainerStats(ctx, name)
			if err != nil {
				return
			}
			pct := st.CPUPercent()
			if vcpu > 0 {
				pct /= vcpu
			}
			mu.Lock()
			total += pct
			n++
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	if n == 0 {
		return 0
	}
	return total / float64(n)
}

// reconcileService converge um serviço para o desejado. Não corre durante
// um deploy desse serviço (o deploy é dono das réplicas até acabar).
func (s *Service) reconcileService(ctx context.Context, p store.Project, e store.Environment, svc store.Service, containers []dockerclient.Container) {
	if svc.DesiredState == store.DesiredStopped || svc.CurrentRevisionID == nil {
		for _, c := range containers {
			s.stopAndRemove(ctx, containerName(c))
		}
		return
	}
	rev, err := s.db.GetRevision(ctx, *svc.CurrentRevisionID)
	if err != nil {
		s.logger.Error("reconciliador: revisão atual", "service", svc.Slug, "err", err)
		return
	}

	var current []dockerclient.Container
	for _, c := range containers {
		name := containerName(c)
		if deadline, draining := s.rt.drainDeadline(name); draining {
			if time.Now().After(deadline) {
				s.stopAndRemove(ctx, name)
			}
			continue
		}
		switch {
		case c.Labels[LabelRevision] != rev.ID:
			// Sobra de um deploy interrompido ou de uma revisão antiga.
			s.logger.Info("reconciliador: réplica de outra revisão", "container", name)
			s.drainReplica(name)
		case c.State != "running" && c.State != "restarting":
			// Parada por fora (docker stop) ou morta: substitui-se.
			s.stopAndRemove(ctx, name)
		default:
			current = append(current, c)
		}
	}

	var ready []string
	for _, c := range current {
		name := containerName(c)
		err := s.probeReplica(ctx, name, rev, false)
		s.rt.observe(name, err == nil)
		if s.rt.isReady(name) {
			ready = append(ready, name)
		}
	}

	s.autoscale(ctx, svc, rev, ready)
	want := s.targetReplicas(ctx, svc)

	s.rt.setProblem(svc.ID, "")
	switch {
	case len(current) < want:
		envVars, err := s.containerEnv(ctx, e, rev)
		if err != nil {
			s.logger.Error("reconciliador: env do serviço", "service", svc.Slug, "err", err)
			s.rt.setProblem(svc.ID, "não foi possível montar o env: "+err.Error())
			return
		}
		for i := len(current); i < want; i++ {
			name, err := s.createReplica(ctx, p, e, svc, rev, envVars)
			if err != nil {
				s.logger.Error("reconciliador: criando réplica", "service", svc.Slug, "err", err)
				s.rt.setProblem(svc.ID, fmt.Sprintf("só %d de %d réplicas: %v", i, want, err))
				return
			}
			s.logger.Info("reconciliador: réplica criada", "service", svc.Slug, "container", name)
		}
	case len(current) > want:
		// Sai primeiro quem não está pronto, depois as mais recentes.
		sort.SliceStable(current, func(i, j int) bool {
			ri, rj := s.rt.isReady(containerName(current[i])), s.rt.isReady(containerName(current[j]))
			return !ri && rj
		})
		for _, c := range current[:len(current)-want] {
			s.drainReplica(containerName(c))
		}
	}
}
