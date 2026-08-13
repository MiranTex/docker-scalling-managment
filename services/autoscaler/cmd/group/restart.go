package main

import (
	"context"
	"log/slog"
	"sync"

	"autoscaler/internal/discovery"
	"autoscaler/internal/dockerclient"
	"autoscaler/internal/launcherclient"
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
// a partir do MinReplicas configurado.
//
// Nota importante sobre o que isto NÃO faz: cfg.launchTemplate foi
// parseado uma vez no arranque do processo (ver loadConfig) e continua o
// mesmo aqui -- este restart não relê o ficheiro do disco. Mudar a
// IMAGEM/binds/rede do template continua a exigir recriar o container do
// group inteiro (docker compose up --force-recreate ou equivalente), não
// só este restart lógico. O que este restart resolve de fresco é o VALOR
// de cada referência ${secret:NOME} no "env" (ver applyScaleUp/
// services/launcher/internal/secretsclient) -- mas isso já acontece em
// TODO scale up, não só num restart; nesse sentido, um restart não é mais
// necessário do que qualquer outro scale up para um segredo já
// referenciado ficar atualizado, só é útil se quiseres forçar a
// recriação de réplicas já existentes agora, em vez de esperar o próximo
// scale down/up natural.
//
// restartMu serializa isto contra reconcile(): sem essa serialização, um
// tick concorrente poderia recriar uma réplica enquanto terminateAllReplicas
// ainda está a removê-las, ou vice-versa.
func restartGroup(ctx context.Context, cfg config, client *dockerclient.Client, logger *slog.Logger, balancer *loadbalancer.RoundRobin, evaluator *scaler.Evaluator, draining *drainSet, metrics *groupMetrics, restartMu *sync.Mutex, sink snapshotSink, launcher *launcherclient.Client) {
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
	reconcile(ctx, cfg, client, logger, balancer, evaluator, draining, metrics, restartMu, sink, launcher)
	logger.Info("restart concluído")
}
