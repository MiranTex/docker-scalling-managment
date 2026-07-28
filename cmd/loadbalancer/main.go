package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"autoscaler/internal/discovery"
	"autoscaler/internal/dockerclient"
	"autoscaler/internal/loadbalancer"

	"github.com/joho/godotenv"
)

// serviceLabel identifica quais containers pertencem ao serviço balanceado —
// a mesma label que o autoscaler usa pra saber quais containers gerencia.
const serviceLabel = "autoscaler.service"

// targetService é o valor da label que queremos balancear e backendPort é a
// porta que a aplicação escuta dentro do container. Fixos por enquanto;
// virarão flags/config quando o LB precisar lidar com múltiplos serviços.
const (
	targetService      = "demo"
	backendPort        = 80
	discoveryTick      = 3 * time.Second
	healthCheckTimeout = 500 * time.Millisecond
	listenAddr         = ":8090"
)

func main() {
	_ = godotenv.Load()
	ctx := context.Background()
	client := dockerclient.New(os.Getenv("HOME") + os.Getenv("DOCKER_SOCKET"))
	balancer := loadbalancer.NewRoundRobin()

	go discoveryLoop(ctx, client, balancer)

	log.Printf("loadbalancer: ouvindo em %s, encaminhando para serviço %q (porta %d)", listenAddr, targetService, backendPort)
	if err := http.ListenAndServe(listenAddr, loadbalancer.NewProxy(balancer)); err != nil {
		log.Fatal(err)
	}
}

// discoveryLoop atualiza a lista de backends do balancer periodicamente,
// consultando o Docker por containers rodando com a label do serviço alvo.
func discoveryLoop(ctx context.Context, client *dockerclient.Client, balancer *loadbalancer.RoundRobin) {
	ticker := time.NewTicker(discoveryTick)
	defer ticker.Stop()

	for {
		backends, err := discoverBackends(ctx, client)
		if err != nil {
			log.Printf("loadbalancer: erro descobrindo backends: %v", err)
		} else {
			balancer.SetBackends(backends)
			log.Printf("loadbalancer: %d backend(s) ativos", len(backends))
		}
		<-ticker.C
	}
}

// discoverBackends lista os containers running do serviço alvo e devolve só
// os que passaram no health check TCP na porta configurada.
func discoverBackends(ctx context.Context, client *dockerclient.Client) ([]loadbalancer.Backend, error) {
	filters := map[string][]string{
		"label": {fmt.Sprintf("%s=%s", serviceLabel, targetService)},
	}
	containers, err := client.ListContainers(ctx, false, filters)
	if err != nil {
		return nil, fmt.Errorf("listando containers: %w", err)
	}

	return discovery.Backends(ctx, client, containers, backendPort, healthCheckTimeout), nil
}
