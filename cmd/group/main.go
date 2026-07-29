// cmd/group é o "autoscaling group" completo: um único processo responsável
// por UM serviço — decide quando escalar (scaler+executor) e ao mesmo tempo
// serve como load balancer pra ele (loadbalancer). cmd/server e
// cmd/loadbalancer continuam existindo como ferramentas separadas, úteis pra
// estudar cada peça isolada; este binário é a junção das duas.
//
// Toda a configuração vem de variáveis de ambiente (veja config.go) — é o
// que permite rodar várias instâncias, uma por serviço, cada uma num
// container com seu próprio conjunto de envs.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"autoscaler/internal/discovery"
	"autoscaler/internal/dockerclient"
	"autoscaler/internal/executor"
	"autoscaler/internal/loadbalancer"
	"autoscaler/internal/scaler"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	logger := setupLogger()
	cfg := loadConfig()

	// ctx é cancelado assim que um SIGINT/SIGTERM chega, o que faz o
	// reconcileLoop parar de agendar novos ciclos. As requisições HTTP em
	// andamento são drenadas separadamente, abaixo, via srv.Shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := dockerclient.New(cfg.dockerSocket)
	balancer := loadbalancer.NewRoundRobin()
	evaluator := scaler.NewEvaluator(cfg.policy)
	draining := newDrainSet()

	go reconcileLoop(ctx, cfg, client, logger, balancer, evaluator, draining)

	srv := &http.Server{
		Addr:    cfg.listenAddr,
		Handler: loadbalancer.NewProxy(balancer),
	}

	go func() {
		logger.Info("group iniciado", cfg.logAttrs()...)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("erro no servidor HTTP", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("sinal de encerramento recebido, drenando requisições em andamento", "shutdown_timeout", cfg.shutdownTimeout.String())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown do servidor HTTP não terminou a tempo", "err", err)
	}
	logger.Info("group encerrado")
}

// reconcileLoop é o coração do grupo: a cada tick, lê o estado atual dos
// containers do serviço UMA vez e usa essa mesma leitura tanto pra decidir
// se escala quanto para atualizar a lista de backends do load balancer.
func reconcileLoop(ctx context.Context, cfg config, client *dockerclient.Client, logger *slog.Logger, balancer *loadbalancer.RoundRobin, evaluator *scaler.Evaluator, draining *drainSet) {
	ticker := time.NewTicker(cfg.reconcileTick)
	defer ticker.Stop()

	for {
		reconcile(ctx, cfg, client, logger, balancer, evaluator, draining)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			logger.Info("reconcile loop encerrado", "err", ctx.Err())
			return
		}
	}
}

func reconcile(ctx context.Context, cfg config, client *dockerclient.Client, logger *slog.Logger, balancer *loadbalancer.RoundRobin, evaluator *scaler.Evaluator, draining *drainSet) {
	logger = logger.With("service", cfg.targetService)

	filters := map[string][]string{
		"label": {fmt.Sprintf("%s=%s", cfg.serviceLabel, cfg.targetService)},
	}
	containers, err := client.ListContainers(ctx, false, filters)
	if err != nil {
		logger.Error("erro listando containers", "err", err)
		return
	}

	if len(containers) == 0 {
		logger.Warn("nenhum container encontrado para o serviço, load balancer sem backends")
		balancer.SetBackends(nil)
		return
	}

	if metrics, err := discovery.AggregateMetrics(ctx, client, cfg.targetService, containers); err != nil {
		logger.Error("erro coletando métricas", "err", err)
	} else {
		decision := evaluator.Evaluate(metrics, time.Now())
		level := slog.LevelDebug
		if decision.Action != scaler.NoAction {
			level = slog.LevelInfo
		}
		logger.Log(ctx, level, "reconcile avaliado",
			"replicas", metrics.Replicas,
			"avg_cpu_percent", metrics.AvgCPUPercent,
			"action", decision.Action.String(),
			"delta", decision.Delta,
			"reason", decision.Reason,
		)

		switch decision.Action {
		case scaler.ScaleUp:
			if err := executor.Apply(ctx, client, decision, containers); err != nil {
				logger.Error("erro aplicando scale up", "err", err)
			}
		case scaler.ScaleDown:
			startDrain(ctx, cfg, client, logger, draining, containers)
		}
	}

	// Containers em drenagem já saíram (ou estão saindo) do serviço: não
	// devem mais receber requisições novas, mesmo que o Docker ainda não
	// tenha terminado de pará-los.
	active := draining.excludeDraining(containers)
	backends := discovery.Backends(ctx, client, active, cfg.backendPort, cfg.healthCheckPath, cfg.healthCheckTimeout)
	balancer.SetBackends(backends)
	logger.Debug("backends atualizados", "healthy", len(backends), "total", len(active))
}

// startDrain escolhe um alvo de scale down entre os containers que ainda
// não estão em drenagem, tira-o imediatamente da rotação do load balancer
// (via draining.add, refletido no próximo SetBackends deste mesmo
// reconcile) e só depois de cfg.drainTimeout efetivamente para e remove o
// container — dando tempo das requisições já em andamento nele terminarem.
func startDrain(ctx context.Context, cfg config, client *dockerclient.Client, logger *slog.Logger, draining *drainSet, containers []dockerclient.Container) {
	candidates := draining.excludeDraining(containers)
	target, err := executor.SelectScaleDownTarget(candidates)
	if err != nil {
		logger.Warn("scale down sem candidatos livres, todos já em drenagem")
		return
	}

	draining.add(target.ID)
	logger.Info("container escolhido para scale down, saindo do load balancer",
		"container_id", target.ID[:12], "drain_timeout", cfg.drainTimeout.String())

	go func(id string) {
		time.Sleep(cfg.drainTimeout)
		if err := executor.Remove(ctx, client, id); err != nil {
			logger.Error("erro removendo container", "container_id", id[:12], "err", err)
		} else {
			logger.Info("container removido", "container_id", id[:12])
		}
		draining.remove(id)
	}(target.ID)
}
