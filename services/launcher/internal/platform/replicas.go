package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

const (
	LabelService     = "base-stack.service"
	LabelServiceSlug = "base-stack.service-slug"
	LabelRevision    = "base-stack.revision"

	// workerReadyAfter: um worker não tem health check -- está "pronto"
	// quando está a correr há este tempo sem ter reiniciado.
	workerReadyAfter = 5 * time.Second
	// drainDelay é quanto uma réplica fica fora do routing antes de
	// receber SIGTERM, para os pedidos em curso terminarem.
	drainDelay = 10 * time.Second
	// stopGraceSeconds é o tempo entre SIGTERM e SIGKILL.
	stopGraceSeconds = 30
)

// replicasOf lista os containers de um serviço (de qualquer revisão,
// incluindo parados).
func (s *Service) replicasOf(ctx context.Context, serviceID string) ([]dockerclient.Container, error) {
	return s.docker.ListContainers(ctx, true, map[string][]string{"label": {LabelService + "=" + serviceID}})
}

// replicaAddr é por onde a plataforma (health checks, Traefik) chega a uma
// réplica: o IP dela na rede do ambiente. O DNS de uma overlay entre nós
// não é de confiança para containers soltos (um container que volta a
// entrar na rede pode deixar de resolver os que já lá estavam noutros
// nós); o IP não muda enquanto a réplica existir. Sem IP, o nome.
func replicaAddr(c dockerclient.Container) string {
	for _, raw := range c.NetworkSettings.Networks {
		var ep struct {
			IPAddress string `json:"IPAddress"`
		}
		if json.Unmarshal(raw, &ep) == nil && ep.IPAddress != "" {
			return ep.IPAddress
		}
	}
	return containerName(c)
}

// detached: a réplica corre mas não está em rede nenhuma (ex: o nó saiu
// e voltou a entrar no swarm) -- nunca vai servir, substitui-se. Sem
// informação de redes (nil) não se conclui nada.
func detached(c dockerclient.Container) bool {
	return c.NetworkSettings.Networks != nil && replicaAddr(c) == containerName(c)
}

func containerName(c dockerclient.Container) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID
}

// containerEnv monta o env de um container de serviço: PORT, as URLs dos
// addons do ambiente, e por cima o env da revisão com os ${secret:...}
// resolvidos (o da revisão ganha se repetir uma chave).
func (s *Service) containerEnv(ctx context.Context, e store.Environment, rev store.Revision) ([]string, error) {
	values, err := s.secretValues(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	var base []string
	if rev.Port > 0 {
		base = append(base, "PORT="+strconv.Itoa(rev.Port))
	}
	addons, err := s.db.ListAddons(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range addons {
		if v, ok := values[a.EnvVar]; ok {
			base = append(base, a.EnvVar+"="+v)
		}
	}
	resolved, err := resolveWith(values, rev.Env)
	if err != nil {
		return nil, err
	}
	return mergeEnv(base, resolved), nil
}

// mergeEnv junta listas KEY=VALUE; uma chave repetida fica com o valor da
// última lista, na posição da primeira ocorrência.
func mergeEnv(lists ...[]string) []string {
	index := map[string]int{}
	var out []string
	for _, l := range lists {
		for _, kv := range l {
			key, _, _ := strings.Cut(kv, "=")
			if i, ok := index[key]; ok {
				out[i] = kv
				continue
			}
			index[key] = len(out)
			out = append(out, kv)
		}
	}
	return out
}

func replicaLabels(p store.Project, e store.Environment, svc store.Service, rev store.Revision) map[string]string {
	labels := environmentLabels(p, e)
	labels[LabelRole] = RoleApp
	labels[LabelService] = svc.ID
	labels[LabelServiceSlug] = svc.Slug
	labels[LabelRevision] = rev.ID
	labels[dockerclient.LabelInstanceType] = "service"
	labels[dockerclient.LabelVCPU] = strconv.FormatFloat(rev.VCPU, 'f', -1, 64)
	labels[dockerclient.LabelMemoryMB] = strconv.FormatInt(rev.MemoryMB, 10)
	// Convenção do services/monitoring: o Prometheus descobre os containers
	// com estas labels e chega-lhes pelo nome, porque está ligado à rede do
	// ambiente (ver attachPlatform).
	if rev.MetricsPort > 0 {
		labels["prometheus.scrape"] = "true"
		labels["prometheus.port"] = strconv.Itoa(rev.MetricsPort)
		path := rev.MetricsPath
		if path == "" {
			path = "/metrics"
		}
		labels["prometheus.path"] = path
	}
	return labels
}

// createReplica cria e arranca uma réplica da revisão. Não espera que fique
// pronta -- isso é o health check (probeReplica).
func (s *Service) createReplica(ctx context.Context, p store.Project, e store.Environment, svc store.Service, rev store.Revision, envVars []string) (string, error) {
	if err := s.checkQuota(ctx, p, rev.VCPU, rev.MemoryMB); err != nil {
		return "", err
	}
	node, err := s.placeReplica(ctx, rev)
	if err != nil {
		return "", err
	}
	docker, err := s.dockerOn(node)
	if err != nil {
		return "", err
	}
	// O deploy buscou a imagem para esta máquina; outro nó tem de a ter.
	if node != LocalNode {
		if _, err := docker.InspectImage(ctx, rev.Image); err != nil {
			if err := s.pullWith(ctx, docker, rev.Image, p.ID); err != nil {
				return "", fmt.Errorf("no nó %s: %w", node, err)
			}
		}
	}
	labels := replicaLabels(p, e, svc, rev)
	labels[LabelNode] = node
	memory := rev.MemoryMB * 1024 * 1024
	name := "bs-" + p.Slug + "-" + e.Slug + "-" + svc.Slug + "-" + newID()[:6]
	id, err := docker.CreateContainer(ctx, name, dockerclient.CreateContainerRequest{
		Image:  rev.Image,
		Cmd:    rev.Cmd,
		Env:    envVars,
		Labels: labels,
		HostConfig: &dockerclient.CreateHostConfig{
			NetworkMode:   e.Network,
			RestartPolicy: dockerclient.UnlessStopped(),
			SecurityOpt:   dockerclient.AppSecurityOpt(),
			Resources: dockerclient.Resources{
				NanoCPUs:   int64(rev.VCPU * 1e9),
				Memory:     memory,
				MemorySwap: memory,
			},
		},
		// O slug como alias: os outros serviços do ambiente chegam a este
		// por http://<slug>:<porta>, com round-robin de DNS entre réplicas.
		NetworkingConfig: dockerclient.WithAliases(e.Network, svc.Slug),
	})
	if err != nil {
		return "", fmt.Errorf("criando réplica: %w", err)
	}
	if err := docker.StartContainer(ctx, id); err != nil {
		_ = docker.RemoveContainer(ctx, id, true)
		return "", fmt.Errorf("arrancando réplica: %w", err)
	}
	return name, nil
}

// errReplicaDied: a réplica morreu ou está a reiniciar em ciclo -- não vale
// a pena esperar pelo health check.
type errReplicaDied struct {
	container string
	msg       string
}

func (e errReplicaDied) Error() string   { return e.msg }
func (e errReplicaDied) replica() string { return e.container }

// errReplicaUnhealthy: a réplica corre mas não passou no health check a
// tempo.
type errReplicaUnhealthy struct {
	container string
	msg       string
}

func (e errReplicaUnhealthy) Error() string   { return e.msg }
func (e errReplicaUnhealthy) replica() string { return e.container }

// exitHint explica os exit codes que mais confundem. 0 é o caso de uma
// imagem base (ubuntu, alpine...): o processo dela é uma shell que, sem
// terminal, termina logo -- com sucesso.
func exitHint(code int) string {
	switch code {
	case 0:
		return " -- o processo principal terminou sem erro: a imagem não tem um processo que fique a correr (ex: uma imagem base como ubuntu). Define um comando que não termine"
	case 126:
		return " -- o comando existe mas não tem permissão de execução"
	case 127:
		return " -- o comando não existe na imagem"
	case 137:
		return " -- morto à força (SIGKILL), normalmente por falta de memória: aumenta a memória por réplica"
	}
	return ""
}

// probeReplica verifica se uma réplica está pronta: worker = a correr há
// workerReadyAfter; web = health check HTTP (ou só TCP, sem caminho
// configurado) à porta, pelo nome do container na rede do ambiente.
//
// failFast (no deploy) trata qualquer reinício como falha definitiva: uma
// réplica nova que já reiniciou está a falhar no arranque. Fora do deploy
// um reinício isolado não diz nada -- o Docker repôs a réplica e o health
// check decide.
func (s *Service) probeReplica(ctx context.Context, name string, rev store.Revision, failFast bool) error {
	info, err := s.docker.InspectContainer(ctx, name)
	if err != nil {
		return err
	}
	if !info.State.Running {
		return errReplicaDied{name, fmt.Sprintf("a réplica %s terminou (exit code %d)%s", name, info.State.ExitCode, exitHint(info.State.ExitCode))}
	}
	if failFast && info.RestartCount > 0 {
		return errReplicaDied{name, fmt.Sprintf("a réplica %s já reiniciou %d vez(es) -- o processo principal está a terminar logo depois de arrancar (ver o log abaixo)", name, info.RestartCount)}
	}
	if rev.Port == 0 {
		if wait := s.workerReadyAfter(); time.Since(info.State.StartedAt) < wait {
			return fmt.Errorf("a correr há menos de %s", wait)
		}
		return nil
	}
	// Pelo IP na rede do ambiente (ver replicaAddr).
	host := name
	for _, ep := range info.NetworkSettings.Networks {
		if ep.IPAddress != "" {
			host = ep.IPAddress
			break
		}
	}
	return s.probe(ctx, host, rev.Port, rev.HealthPath)
}

// defaultProbe é o health check real. Separado para os testes o poderem
// substituir (não há rede de ambiente num teste unitário).
func defaultProbe(ctx context.Context, host string, port int, path string) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if path == "" {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addr+path, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("health check %s respondeu %d", path, resp.StatusCode)
	}
	return nil
}

// stopReplica tira a réplica do routing de imediato e agenda a paragem
// para depois do drainDelay (feita pelo reconciliador, ou por quem chama
// waitDrainAndStop).
func (s *Service) drainReplica(name string) {
	s.rt.drain(name, time.Now().Add(s.drainDelay()))
}

func (s *Service) drainDelay() time.Duration {
	if s.cfg.drainDelay > 0 {
		return s.cfg.drainDelay
	}
	return drainDelay
}

func (s *Service) stopAndRemove(ctx context.Context, name string) {
	_ = s.docker.StopContainer(ctx, name, stopGraceSeconds)
	if err := s.docker.RemoveContainer(ctx, name, true); err != nil && !isNotFound(err) {
		s.logger.Error("removendo réplica", "container", name, "err", err)
	}
	s.rt.forget(name)
}

func (s *Service) workerReadyAfter() time.Duration {
	if s.cfg.workerReadyAfter > 0 {
		return s.cfg.workerReadyAfter
	}
	return workerReadyAfter
}

// Replica é uma réplica viva de um serviço, para a UI.
type Replica struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	Status         string `json:"status"`
	RevisionID     string `json:"revisionId"`
	RevisionNumber int    `json:"revisionNumber"`
	Ready          bool   `json:"ready"`
	Draining       bool   `json:"draining"`
	Node           string `json:"node"`
}

func (s *Service) ListReplicas(ctx context.Context, projectSlug, envSlug, svcSlug string) ([]Replica, error) {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return nil, err
	}
	containers, err := s.replicasOf(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	numbers := map[string]int{}
	out := make([]Replica, 0, len(containers))
	for _, c := range containers {
		revID := c.Labels[LabelRevision]
		if _, ok := numbers[revID]; !ok {
			if rev, err := s.db.GetRevision(ctx, revID); err == nil {
				numbers[revID] = rev.Number
			}
		}
		name := containerName(c)
		_, draining := s.rt.drainDeadline(name)
		out = append(out, Replica{
			Name: name, State: c.State, Status: c.Status, RevisionID: revID, RevisionNumber: numbers[revID],
			Ready: s.rt.isReady(name), Draining: draining, Node: nodeOf(c),
		})
	}
	return out, nil
}
