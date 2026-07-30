// Package discovery contém a lógica compartilhada entre o autoscaler e o
// load balancer para transformar a lista bruta de containers do Docker em
// coisas que cada um entende: métricas agregadas por serviço (pro scaler) e
// uma lista de backends saudáveis (pro load balancer). Existir como pacote
// à parte evita que essa lógica fique duplicada entre os binários que a usam.
package discovery

import (
	"context"
	"fmt"
	"strings"
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

// ContainerStats é o detalhe de recursos de UM container do grupo, coletado
// na mesma leitura de stats já usada por AggregateMetrics pra calcular a
// média de CPU do serviço — não faz nenhuma chamada extra ao Docker, só
// aproveita campos do mesmo payload que já vinha sendo lido.
//
// NetworkRxBytes/TxBytes e DiskReadBytes/WriteBytes são contadores
// acumulados desde que o container subiu (não uma taxa); expostos como
// gauge no Prometheus, é a query (rate()/increase()) quem calcula
// bytes/segundo, não este pacote.
type ContainerStats struct {
	ContainerID      string
	Name             string
	CPUPercent       float64
	MemoryPercent    float64
	MemoryUsageBytes uint64
	NetworkRxBytes   uint64
	NetworkTxBytes   uint64
	DiskReadBytes    uint64
	DiskWriteBytes   uint64
}

// containerName devolve o primeiro nome do container sem a barra inicial
// que a API do Docker sempre inclui (ex: "/idle1" -> "idle1"), ou o ID
// curto se por algum motivo não vier nome nenhum.
func containerName(ctr dockerclient.Container) string {
	if len(ctr.Names) > 0 {
		return strings.TrimPrefix(ctr.Names[0], "/")
	}
	return ctr.ID[:12]
}

// AggregateMetrics coleta o stats de cada container do grupo, calcula a
// média de CPU do serviço como um todo (pro scaler decidir) e devolve
// também o detalhe por container (pra observabilidade). Erro se members
// estiver vazio: não há "média" de um grupo sem membros, e quem chama deve
// tratar esse caso antes de decidir se escala.
func AggregateMetrics(ctx context.Context, client *dockerclient.Client, service string, members []dockerclient.Container) (scaler.ServiceMetrics, []ContainerStats, error) {
	if len(members) == 0 {
		return scaler.ServiceMetrics{}, nil, fmt.Errorf("discovery: nenhum container no grupo %q", service)
	}

	var totalCPU float64
	details := make([]ContainerStats, 0, len(members))
	for _, ctr := range members {
		stats, err := client.ContainerStats(ctx, ctr.ID)
		if err != nil {
			return scaler.ServiceMetrics{}, nil, fmt.Errorf("stats do container %s: %w", ctr.ID[:12], err)
		}
		totalCPU += stats.CPUPercent()

		rx, tx := stats.NetworkBytes()
		diskRead, diskWrite := stats.DiskBytes()
		details = append(details, ContainerStats{
			ContainerID:      ctr.ID,
			Name:             containerName(ctr),
			CPUPercent:       stats.CPUPercent(),
			MemoryPercent:    stats.MemoryPercent(),
			MemoryUsageBytes: stats.MemoryStats.Usage,
			NetworkRxBytes:   rx,
			NetworkTxBytes:   tx,
			DiskReadBytes:    diskRead,
			DiskWriteBytes:   diskWrite,
		})
	}

	return scaler.ServiceMetrics{
		Name:          service,
		Replicas:      len(members),
		AvgCPUPercent: totalCPU / float64(len(members)),
	}, details, nil
}

// Backends resolve o IP de cada container do grupo e testa sua saúde na
// porta informada, devolvendo só os que passaram no health check. Os checks
// rodam em paralelo porque cada um pode levar até healthCheckTimeout, e não
// queremos que N containers lentos somem seus tempos de espera em série.
//
// healthCheckPath define o tipo de check: vazio faz só um dial TCP (serve
// pra qualquer protocolo, mas não valida o conteúdo da resposta); definido
// (ex: "/health") faz um GET HTTP de verdade nesse caminho, exigindo status
// < 400 — mais preciso pra serviços HTTP.
func Backends(ctx context.Context, client *dockerclient.Client, members []dockerclient.Container, port int, healthCheckPath string, healthCheckTimeout time.Duration) []loadbalancer.Backend {
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
			healthy := loadbalancer.TCPHealthy(addr, healthCheckTimeout)
			if healthy && healthCheckPath != "" {
				healthy = loadbalancer.HTTPHealthy(ctx, addr, healthCheckPath, healthCheckTimeout)
			}
			if !healthy {
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
