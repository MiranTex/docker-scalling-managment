package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"autoscaler/internal/adminapi"
	"autoscaler/internal/discovery"
	"autoscaler/internal/dockerclient"
	"autoscaler/internal/executor"
	"autoscaler/internal/launcherclient"
	"autoscaler/internal/loadbalancer"
	"autoscaler/internal/scaler"
)

// groupAdapter implementa adminapi.GroupControl fechando sobre exatamente
// os mesmos objetos que reconcileLoop já é dono de -- sem criar uma segunda
// fonte de verdade para policy/réplicas. Também implementa snapshotSink
// (ver restart.go): a cada reconcile, guarda o resultado mais recente para
// GET /v1/status responder sem repetir chamadas de stats ao Docker.
type groupAdapter struct {
	cfg       config
	client    *dockerclient.Client
	logger    *slog.Logger
	balancer  *loadbalancer.RoundRobin
	evaluator *scaler.Evaluator
	draining  *drainSet
	metrics   *groupMetrics
	restartMu *sync.Mutex
	launcher  *launcherclient.Client
	startedAt time.Time

	snapMu         sync.RWMutex
	lastContainers []dockerclient.Container
	lastStats      []discovery.ContainerStats
	lastDecision   scaler.Decision
	lastActionAt   time.Time
}

func newGroupAdapter(cfg config, client *dockerclient.Client, logger *slog.Logger, balancer *loadbalancer.RoundRobin, evaluator *scaler.Evaluator, draining *drainSet, metrics *groupMetrics, restartMu *sync.Mutex, launcher *launcherclient.Client) *groupAdapter {
	return &groupAdapter{
		cfg:       cfg,
		client:    client,
		logger:    logger,
		balancer:  balancer,
		evaluator: evaluator,
		draining:  draining,
		metrics:   metrics,
		restartMu: restartMu,
		launcher:  launcher,
		startedAt: time.Now(),
	}
}

func (a *groupAdapter) recordSnapshot(containers []dockerclient.Container, stats []discovery.ContainerStats, decision scaler.Decision) {
	a.snapMu.Lock()
	defer a.snapMu.Unlock()
	a.lastContainers = containers
	a.lastStats = stats
	a.lastDecision = decision
	if decision.Action != scaler.NoAction {
		a.lastActionAt = time.Now()
	}
}

func (a *groupAdapter) Policy() scaler.Policy {
	return a.evaluator.Policy()
}

func (a *groupAdapter) SetPolicy(p scaler.Policy) (scaler.Policy, error) {
	a.evaluator.SetPolicy(p)
	return a.evaluator.Policy(), nil
}

func (a *groupAdapter) Restart(ctx context.Context) error {
	restartGroup(ctx, a.cfg, a.client, a.logger, a.balancer, a.evaluator, a.draining, a.metrics, a.restartMu, a, a.launcher)
	return nil
}

// AddReplica cria uma réplica extra imediatamente, fora do ciclo normal do
// scaler -- mesmo caminho (local ou via launcher) que applyScaleUp usaria
// num scale up automático, ver cmd/group/main.go. restartMu serializa
// isto contra reconcile()/Restart(), pela mesma razão de sempre: evitar
// que um tick concorrente crie/remova réplicas ao mesmo tempo que uma
// ação manual.
func (a *groupAdapter) AddReplica(ctx context.Context) (string, error) {
	a.restartMu.Lock()
	defer a.restartMu.Unlock()

	id, err := applyScaleUp(ctx, a.client, a.launcher, a.cfg.launchTemplate)
	if err != nil {
		return "", err
	}
	a.metrics.scaleActions.WithLabelValues(a.cfg.targetService, "manual_scale_up").Inc()
	a.logger.Info("réplica adicionada manualmente via API admin", "container_id", id[:12])
	return id, nil
}

// RemoveReplica para e remove uma réplica específica -- confirma primeiro
// que o container pertence de facto a este group (mesma label de
// serviço), para um id errado/de outro serviço nunca ser aceite.
func (a *groupAdapter) RemoveReplica(ctx context.Context, containerID string) error {
	a.restartMu.Lock()
	defer a.restartMu.Unlock()

	filters := map[string][]string{
		"label": {fmt.Sprintf("%s=%s", a.cfg.serviceLabel, a.cfg.targetService)},
	}
	containers, err := a.client.ListContainers(ctx, true, filters)
	if err != nil {
		return fmt.Errorf("listando réplicas: %w", err)
	}
	found := false
	for _, c := range containers {
		if c.ID == containerID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("container %s não é uma réplica gerida por este group", containerID)
	}

	if err := executor.Remove(ctx, a.client, containerID); err != nil {
		return err
	}
	a.metrics.scaleActions.WithLabelValues(a.cfg.targetService, "manual_scale_down").Inc()
	a.logger.Info("réplica removida manualmente via API admin", "container_id", containerID[:12])
	return nil
}

func (a *groupAdapter) Status(ctx context.Context) (adminapi.Status, error) {
	a.snapMu.RLock()
	containers := a.lastContainers
	stats := a.lastStats
	decision := a.lastDecision
	lastActionAt := a.lastActionAt
	a.snapMu.RUnlock()

	cpuByID := make(map[string]float64, len(stats))
	for _, s := range stats {
		cpuByID[s.ContainerID] = s.CPUPercent
	}

	replicas := make([]adminapi.Replica, 0, len(containers))
	for _, c := range containers {
		replicas = append(replicas, adminapi.Replica{
			ContainerID: c.ID,
			Name:        containerDisplayName(c),
			State:       c.State,
			Status:      c.Status,
			CPUPercent:  cpuByID[c.ID],
		})
	}

	status := adminapi.Status{
		TargetService:  a.cfg.targetService,
		ReplicaCount:   len(containers),
		Replicas:       replicas,
		Policy:         adminapi.PolicyToDTO(a.evaluator.Policy()),
		LaunchTemplate: launchTemplateInfo(a.cfg.launchTemplatePath),
		UptimeSeconds:  time.Since(a.startedAt).Seconds(),
	}
	if decision.Action != scaler.NoAction && !lastActionAt.IsZero() {
		status.LastAction = &adminapi.ScaleAction{
			Action: decision.Action.String(),
			At:     lastActionAt,
			Reason: decision.Reason,
		}
	}
	return status, nil
}

func containerDisplayName(c dockerclient.Container) string {
	if len(c.Names) > 0 {
		name := c.Names[0]
		if len(name) > 0 && name[0] == '/' {
			return name[1:]
		}
		return name
	}
	if len(c.ID) >= 12 {
		return c.ID[:12]
	}
	return c.ID
}

// launchTemplateInfo lê o hash/mtime do ficheiro de launch template em
// disco, sem expor o conteúdo (imagem/env/binds) por esta API -- só o
// suficiente para a UI saber se o ficheiro mudou desde o último restart.
// Erros de leitura (ficheiro temporariamente inacessível) resultam num
// LaunchTemplateInfo só com o path, não numa falha do endpoint inteiro.
func launchTemplateInfo(path string) adminapi.LaunchTemplateInfo {
	info := adminapi.LaunchTemplateInfo{Path: path}

	stat, err := os.Stat(path)
	if err != nil {
		return info
	}
	info.ModifiedAt = stat.ModTime()

	raw, err := os.ReadFile(path)
	if err != nil {
		return info
	}
	sum := sha256.Sum256(raw)
	info.SHA256 = hex.EncodeToString(sum[:])
	return info
}
