package dockerclient

import (
	"context"
	"strings"
)

// Stats é o subconjunto do payload de /containers/{id}/stats que precisamos
// para calcular uso de CPU, memória, rede e disco. O Docker devolve
// contadores brutos e acumulados, não percentuais/taxas prontos — por isso
// os campos "pre" (leitura anterior), usados como base pro delta de CPU.
type Stats struct {
	CPUStats    cpuStats               `json:"cpu_stats"`
	PreCPUStats cpuStats               `json:"precpu_stats"`
	MemoryStats memoryStats            `json:"memory_stats"`
	Networks    map[string]networkStat `json:"networks"`
	BlkioStats  blkioStats             `json:"blkio_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemCPUUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs     uint32 `json:"online_cpus"`
}

type memoryStats struct {
	Usage uint64 `json:"usage"`
	Limit uint64 `json:"limit"`
}

// networkStat é o contador acumulado (desde que o container subiu) de uma
// interface de rede — normalmente só existe "eth0", mas um container pode
// ter mais de uma rede Docker anexada.
type networkStat struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

type blkioStats struct {
	// IoServiceBytesRecursive é uma entrada por (dispositivo, operação):
	// duas linhas por disco, uma "read" e uma "write", acumuladas desde que
	// o container subiu.
	IoServiceBytesRecursive []blkioEntry `json:"io_service_bytes_recursive"`
}

type blkioEntry struct {
	Op    string `json:"op"`
	Value uint64 `json:"value"`
}

// ContainerStats pega uma única leitura instantânea de uso de recursos do
// container (stream=false), em vez do stream contínuo que a API expõe por
// padrão.
func (c *Client) ContainerStats(ctx context.Context, id string) (*Stats, error) {
	var stats Stats
	if err := c.do(ctx, "GET", "/containers/"+id+"/stats?stream=false", nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// CPUPercent calcula o uso de CPU do container desde a leitura anterior
// (precpu_stats), na mesma fórmula que o `docker stats` usa: delta de uso do
// container sobre delta de uso do sistema, escalado pelo número de CPUs.
//
// Retorna 0 se ainda não houver uma leitura anterior válida (systemDelta == 0).
func (s *Stats) CPUPercent() float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemCPUUsage) - float64(s.PreCPUStats.SystemCPUUsage)

	if systemDelta <= 0 || cpuDelta <= 0 {
		return 0
	}

	numCPUs := float64(s.CPUStats.OnlineCPUs)
	if numCPUs == 0 {
		numCPUs = 1
	}

	return (cpuDelta / systemDelta) * numCPUs * 100.0
}

// MemoryPercent calcula o uso de memória do container como percentual do
// limite configurado (ou do total do host, se o container não tiver limite).
func (s *Stats) MemoryPercent() float64 {
	if s.MemoryStats.Limit == 0 {
		return 0
	}
	return float64(s.MemoryStats.Usage) / float64(s.MemoryStats.Limit) * 100.0
}

// NetworkBytes soma rx/tx de todas as interfaces de rede do container.
// São contadores acumulados desde que o container subiu (não uma taxa) —
// por isso o valor só faz sentido como série temporal: quem calcula
// bytes/segundo é a query (ex: rate() no PromQL), não este método.
func (s *Stats) NetworkBytes() (rx, tx uint64) {
	for _, n := range s.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}
	return rx, tx
}

// DiskBytes soma leitura/escrita de todos os dispositivos de bloco do
// container, também como contador acumulado (mesma lógica de NetworkBytes).
func (s *Stats) DiskBytes() (read, write uint64) {
	for _, e := range s.BlkioStats.IoServiceBytesRecursive {
		switch {
		case strings.EqualFold(e.Op, "read"):
			read += e.Value
		case strings.EqualFold(e.Op, "write"):
			write += e.Value
		}
	}
	return read, write
}
