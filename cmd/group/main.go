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
	"fmt"
	"log"
	"net/http"
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

	ctx := context.Background()
	client := dockerclient.New(cfg.dockerSocket)
	balancer := loadbalancer.NewRoundRobin()

	go reconcileLoop(ctx, cfg, client, balancer)

	log.Printf("group: ouvindo em %s (%s)", cfg.listenAddr, cfg.summary())
	if err := http.ListenAndServe(cfg.listenAddr, loadbalancer.NewProxy(balancer)); err != nil {
		log.Fatal(err)
	}
}

// reconcileLoop é o coração do grupo: a cada tick, lê o estado atual dos
// containers do serviço UMA vez e usa essa mesma leitura tanto pra decidir
// se escala quanto para atualizar a lista de backends do load balancer.
func reconcileLoop(ctx context.Context, cfg config, client *dockerclient.Client, balancer *loadbalancer.RoundRobin) {
	ticker := time.NewTicker(cfg.reconcileTick)
	defer ticker.Stop()

	for {
		reconcile(ctx, cfg, client, balancer)
		<-ticker.C
	}
}

func reconcile(ctx context.Context, cfg config, client *dockerclient.Client, balancer *loadbalancer.RoundRobin) {
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
		decision := scaler.Evaluate(cfg.policy, metrics)
		log.Printf("group: replicas=%d avg_cpu=%.2f%% -> %s (delta=%d): %s",
			metrics.Replicas, metrics.AvgCPUPercent, decision.Action, decision.Delta, decision.Reason)

		if decision.Action != scaler.NoAction {
			if err := executor.Apply(ctx, client, decision, containers); err != nil {
				log.Printf("group: erro aplicando decisão: %v", err)
			}
			// A lista de containers usada abaixo pro load balancer ainda é a
			// de antes da mudança; o próximo tick já reflete o container
			// criado/removido. Um pequeno atraso de um ciclo é aceitável.
		}
	}

	backends := discovery.Backends(ctx, client, containers, cfg.backendPort, cfg.healthCheckPath, cfg.healthCheckTimeout)
	balancer.SetBackends(backends)
	log.Printf("group: %d/%d container(s) saudável(is)", len(backends), len(containers))
}
