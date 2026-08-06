package main

import (
	"context"
	"log/slog"
	"sync"

	"autoscaler/internal/discovery"
	"autoscaler/internal/dockerclient"
	"autoscaler/internal/loadbalancer"
	"autoscaler/internal/scaler"
)

// snapshotSink recebe, depois de cada reconcile, o estado mais recente
// observado -- usado pelo adaptador da API admin (ver adminadapter.go) para
// responder GET /v1/status sem ter de repetir uma chamada de stats ao
// Docker por réplica a cada pedido HTTP.
type snapshotSink interface {
	recordSnapshot(containers []dockerclient.Container, stats []discovery.ContainerStats, decision scaler.Decision)
}

// restartGroup implementa o "restart lógico" pedido via POST /v1/restart:
// repete exatamente o mesmo caminho já usado no shutdown (SIGTERM) --
// termina todas as réplicas geridas por este group -- e a seguir reconstrói
// a partir do MinReplicas configurado, usando o launch template como ele
// estiver em disco NESTE momento (por isso é a forma de aplicar uma
// mudança de launch template sem esperar o container do group inteiro
// reiniciar).
//
// restartMu serializa isto contra reconcile(): sem essa serialização, um
// tick concorrente poderia recriar uma réplica enquanto terminateAllReplicas
// ainda está a removê-las, ou vice-versa.
func restartGroup(ctx context.Context, cfg config, client *dockerclient.Client, logger *slog.Logger, balancer *loadbalancer.RoundRobin, evaluator *scaler.Evaluator, draining *drainSet, metrics *groupMetrics, restartMu *sync.Mutex, sink snapshotSink) {
	shutdownCtx, cancel := context.WithTimeout(ctx, cfg.shutdownTimeout)
	defer cancel()

	restartMu.Lock()
	logger.Info("restart pedido via API admin: terminando réplicas geridas por este group")
	terminateAllReplicas(shutdownCtx, cfg, client, logger)
	draining.clear()
	balancer.SetBackends(nil)
	restartMu.Unlock()

	// Reconstrói passando pelo mesmo caminho que qualquer outro tick usa
	// (reconcile() volta a pegar restartMu por conta própria) -- em vez de
	// duplicar a lógica de bootstrap, reaproveita o comportamento já testado
	// de "réplicas=0 -> scaler decide ScaleUp até MinReplicas".
	reconcile(ctx, cfg, client, logger, balancer, evaluator, draining, metrics, restartMu, sink)
	logger.Info("restart concluído")
}
