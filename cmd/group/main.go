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
	"log"
	"net/http"
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

	go reconcileLoop(ctx, cfg, client, balancer, evaluator, draining)

	srv := &http.Server{
		Addr:    cfg.listenAddr,
		Handler: loadbalancer.NewProxy(balancer),
	}

	go func() {
		log.Printf("group: ouvindo em %s (%s)", cfg.listenAddr, cfg.summary())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("group: erro no servidor HTTP: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("group: sinal de encerramento recebido, drenando requisições em andamento (até %s)", cfg.shutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("group: shutdown do servidor HTTP não terminou a tempo: %v", err)
	}
	log.Printf("group: encerrado")
}

// reconcileLoop é o coração do grupo: a cada tick, lê o estado atual dos
// containers do serviço UMA vez e usa essa mesma leitura tanto pra decidir
// se escala quanto para atualizar a lista de backends do load balancer.
func reconcileLoop(ctx context.Context, cfg config, client *dockerclient.Client, balancer *loadbalancer.RoundRobin, evaluator *scaler.Evaluator, draining *drainSet) {
	ticker := time.NewTicker(cfg.reconcileTick)
	defer ticker.Stop()

	for {
		reconcile(ctx, cfg, client, balancer, evaluator, draining)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			log.Printf("group: reconcile loop encerrado (%v)", ctx.Err())
			return
		}
	}
}

func reconcile(ctx context.Context, cfg config, client *dockerclient.Client, balancer *loadbalancer.RoundRobin, evaluator *scaler.Evaluator, draining *drainSet) {
	filters := map[string][]string{
		"label": {fmt.Sprintf("%s=%s", cfg.serviceLabel, cfg.targetService)},
	}
	containers, err := client.ListContainers(ctx, false, filters)
	if err != nil {
		log.Printf("group: erro listando containers: %v", err)
		return
	}

	if len(containers) == 0 {
		log.Printf("group: nenhum container encontrado para %q, load balancer sem backends", cfg.targetService)
		balancer.SetBackends(nil)
		return
	}

	if metrics, err := discovery.AggregateMetrics(ctx, client, cfg.targetService, containers); err != nil {
		log.Printf("group: erro coletando métricas: %v", err)
	} else {
		decision := evaluator.Evaluate(metrics, time.Now())
		log.Printf("group: replicas=%d avg_cpu=%.2f%% -> %s (delta=%d): %s",
			metrics.Replicas, metrics.AvgCPUPercent, decision.Action, decision.Delta, decision.Reason)

		switch decision.Action {
		case scaler.ScaleUp:
			if err := executor.Apply(ctx, client, decision, containers); err != nil {
				log.Printf("group: erro aplicando decisão: %v", err)
			}
		case scaler.ScaleDown:
			startDrain(ctx, cfg, client, draining, containers)
		}
	}

	// Containers em drenagem já saíram (ou estão saindo) do serviço: não
	// devem mais receber requisições novas, mesmo que o Docker ainda não
	// tenha terminado de pará-los.
	active := draining.excludeDraining(containers)
	backends := discovery.Backends(ctx, client, active, cfg.backendPort, cfg.healthCheckPath, cfg.healthCheckTimeout)
	balancer.SetBackends(backends)
	log.Printf("group: %d/%d container(s) saudável(is)", len(backends), len(active))
}

// startDrain escolhe um alvo de scale down entre os containers que ainda
// não estão em drenagem, tira-o imediatamente da rotação do load balancer
// (via draining.add, refletido no próximo SetBackends deste mesmo
// reconcile) e só depois de cfg.drainTimeout efetivamente para e remove o
// container — dando tempo das requisições já em andamento nele terminarem.
func startDrain(ctx context.Context, cfg config, client *dockerclient.Client, draining *drainSet, containers []dockerclient.Container) {
	candidates := draining.excludeDraining(containers)
	target, err := executor.SelectScaleDownTarget(candidates)
	if err != nil {
		log.Printf("group: scale down sem candidatos livres (todos já em drenagem)")
		return
	}

	draining.add(target.ID)
	log.Printf("group: container %s escolhido para scale down, saindo do load balancer e será removido em %s",
		target.ID[:12], cfg.drainTimeout)

	go func(id string) {
		time.Sleep(cfg.drainTimeout)
		if err := executor.Remove(ctx, client, id); err != nil {
			log.Printf("group: erro removendo container %s: %v", id[:12], err)
		}
		draining.remove(id)
	}(target.ID)
}
