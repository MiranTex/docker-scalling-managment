package main

import (
	"context"
	"fmt"
	"os"

	"autoscaler/internal/discovery"
	"autoscaler/internal/dockerclient"
	"autoscaler/internal/executor"
	"autoscaler/internal/scaler"

	"github.com/joho/godotenv"
)

// serviceLabel é a label que o autoscaler usa para saber a quais containers
// ele tem permissão de gerenciar (criar/remover). Só entram nessa lista
// containers marcados explicitamente com essa label — o resto do Docker no
// host fica intocado.
const serviceLabel = "autoscaler.service"

// samplePolicy é fixa por enquanto só pra ver a decisão acontecer; carregar
// policies por serviço (de config) é um passo futuro.
var samplePolicy = scaler.Policy{
	MinReplicas:         1,
	MaxReplicas:         3,
	CPUScaleUpPercent:   50,
	CPUScaleDownPercent: 1,
}

func main() {

	_ = godotenv.Load()

	ctx := context.Background()
	client := dockerclient.New(os.Getenv("DOCKER_SOCKET"))

	containers, err := client.ListContainers(ctx, false, nil)
	if err != nil {
		panic(err)
	}

	byService := discovery.GroupByLabel(containers, serviceLabel)

	for service, members := range byService {
		metrics, _, err := discovery.AggregateMetrics(ctx, client, service, members)
		if err != nil {
			fmt.Printf("%s: erro ao coletar métricas: %v\n", service, err)
			continue
		}

		decision := scaler.Evaluate(samplePolicy, metrics)
		fmt.Printf("%-20s replicas=%d avg_cpu=%.2f%%  ->  %s (delta=%d): %s\n",
			service, metrics.Replicas, metrics.AvgCPUPercent, decision.Action, decision.Delta, decision.Reason)

		if decision.Action == scaler.NoAction {
			continue
		}
		if err := executor.Apply(ctx, client, decision, members); err != nil {
			fmt.Printf("%s: erro ao executar decisão: %v\n", service, err)
			continue
		}
		fmt.Printf("%s: %s aplicado com sucesso\n", service, decision.Action)
	}
}
