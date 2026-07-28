// Package discovery contém a lógica compartilhada entre o autoscaler e o
// load balancer para transformar a lista bruta de containers do Docker em
// coisas que cada um entende: métricas agregadas por serviço (pro scaler) e
// uma lista de backends saudáveis (pro load balancer). Existir como pacote
// à parte evita que essa lógica fique duplicada entre os binários que a usam.
package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"autoscaler/internal/dockerclient"
	"autoscaler/internal/loadbalancer"
	"autoscaler/internal/scaler"
)

// GroupByLabel separa os containers em grupos usando o valor da label
// informada como chave. Containers sem essa label ficam de fora.
func GroupByLabel(containers []dockerclient.Container, labelKey string) map[string][]dockerclient.Container {
	groups := make(map[string][]dockerclient.Container)
	for _, ctr := range containers {
		value, ok := ctr.Labels[labelKey]
		if !ok {
			continue
		}
		groups[value] = append(groups[value], ctr)
	}
	return groups
}

// AggregateMetrics coleta o stats de cada container do grupo e calcula a
// média de CPU do serviço como um todo. Erro se members estiver vazio: não
// há "média" de um grupo sem membros, e quem chama deve tratar esse caso
// antes de decidir se escala.
func AggregateMetrics(ctx context.Context, client *dockerclient.Client, service string, members []dockerclient.Container) (scaler.ServiceMetrics, error) {
	if len(members) == 0 {
		return scaler.ServiceMetrics{}, fmt.Errorf("discovery: nenhum container no grupo %q", service)
	}

	var totalCPU float64
	for _, ctr := range members {
		stats, err := client.ContainerStats(ctx, ctr.ID)
		if err != nil {
			return scaler.ServiceMetrics{}, fmt.Errorf("stats do container %s: %w", ctr.ID[:12], err)
		}
		totalCPU += stats.CPUPercent()
	}

	return scaler.ServiceMetrics{
		Name:          service,
		Replicas:      len(members),
		AvgCPUPercent: totalCPU / float64(len(members)),
	}, nil
}

// Backends resolve o IP de cada container do grupo e testa se ele está
// aceitando conexão na porta informada, devolvendo só os que passaram no
// health check. Os checks rodam em paralelo porque cada um pode levar até
// healthCheckTimeout, e não queremos que N containers lentos somem seus
// tempos de espera em série.
func Backends(ctx context.Context, client *dockerclient.Client, members []dockerclient.Container, port int, healthCheckTimeout time.Duration) []loadbalancer.Backend {
	results := make([]loadbalancer.Backend, len(members))
	found := make([]bool, len(members))

	var wg sync.WaitGroup
	for i, ctr := range members {
		wg.Add(1)
		go func(i int, ctr dockerclient.Container) {
			defer wg.Done()

			inspect, err := client.InspectContainer(ctx, ctr.ID)
			if err != nil {
				return
			}
			ip := inspect.IPAddress()
			if ip == "" {
				return
			}

			addr := fmt.Sprintf("%s:%d", ip, port)
			if !loadbalancer.TCPHealthy(addr, healthCheckTimeout) {
				return
			}

			results[i] = loadbalancer.Backend{ContainerID: ctr.ID, Addr: addr}
			found[i] = true
		}(i, ctr)
	}
	wg.Wait()

	backends := make([]loadbalancer.Backend, 0, len(members))
	for i, ok := range found {
		if ok {
			backends = append(backends, results[i])
		}
	}
	return backends
}
