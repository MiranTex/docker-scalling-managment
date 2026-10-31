package platform

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"launcher/internal/dockerclient"
)

// Métricas das máquinas (CPU, memória, carga, disco). Em cada nó corre um
// container mínimo (basestack-hostmetrics, busybox sem rede) onde o
// launcher lê, a cada hostMetricsEvery, /proc/stat, /proc/meminfo e
// /proc/loadavg -- que mostram a máquina inteira mesmo dentro de um
// container -- e o df de um volume vazio: os volumes vivem no disco dos
// dados do Docker, que é o que enche. Funciona igual no nó local e nos
// remotos (pelo túnel do agente), sem mexer no agente.

const (
	hostMetricsContainer = "basestack-hostmetrics"
	hostMetricsVolume    = "basestack-hostmetrics"
	// RoleHostMetrics marca o container de recolha de cada nó.
	RoleHostMetrics  = "hostmetrics"
	hostMetricsEvery = 30 * time.Second
	// 24 horas de amostras por nó.
	hostMetricsKeep = int(24 * time.Hour / hostMetricsEvery)
	// Sem amostra há mais do que isto, o nó não conta como medido.
	hostMetricsStale = 3 * hostMetricsEvery
)

const hostMetricsCmd = `head -1 /proc/stat
grep -E '^(MemTotal|MemAvailable):' /proc/meminfo
cat /proc/loadavg
df -kP /host/disk | tail -1`

// HostSample é uma leitura de uma máquina.
type HostSample struct {
	At time.Time `json:"at"`
	// CPUPercent: da máquina inteira (100 = todos os cores ocupados).
	CPUPercent         float64 `json:"cpuPercent"`
	Load1              float64 `json:"load1"`
	Load5              float64 `json:"load5"`
	Load15             float64 `json:"load15"`
	MemoryTotalBytes   uint64  `json:"memoryTotalBytes"`
	MemoryAvailBytes   uint64  `json:"memoryAvailableBytes"`
	DiskTotalBytes     uint64  `json:"diskTotalBytes"`
	DiskAvailableBytes uint64  `json:"diskAvailableBytes"`
}

// cpuTimes: o total e o tempo parado do /proc/stat (o CPU % é a diferença
// entre duas leituras).
type cpuTimes struct{ total, idle uint64 }

type hostMetrics struct {
	mu     sync.Mutex
	cpu    map[string]cpuTimes
	series map[string][]HostSample
}

func newHostMetrics() *hostMetrics {
	return &hostMetrics{cpu: map[string]cpuTimes{}, series: map[string][]HostSample{}}
}

func (h *hostMetrics) add(node string, sample HostSample, cpu cpuTimes) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prev, ok := h.cpu[node]
	h.cpu[node] = cpu
	if !ok || cpu.total <= prev.total {
		// A primeira leitura (ou um reboot do nó) só serve de base ao CPU %.
		return
	}
	sample.CPUPercent = 100 * (1 - float64(cpu.idle-prev.idle)/float64(cpu.total-prev.total))
	s := append(h.series[node], sample)
	if len(s) > hostMetricsKeep {
		s = s[len(s)-hostMetricsKeep:]
	}
	h.series[node] = s
}

// latest: a última leitura de um nó, se for recente.
func (h *hostMetrics) latest(node string) *HostSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.series[node]
	if len(s) == 0 || time.Since(s[len(s)-1].At) > hostMetricsStale {
		return nil
	}
	last := s[len(s)-1]
	return &last
}

func (h *hostMetrics) since(node string, from time.Time) []HostSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []HostSample{}
	for _, s := range h.series[node] {
		if !s.At.Before(from) {
			out = append(out, s)
		}
	}
	return out
}

// parseHostMetrics lê o output de hostMetricsCmd.
func parseHostMetrics(out string) (HostSample, cpuTimes, error) {
	var s HostSample
	var cpu cpuTimes
	sc := bufio.NewScanner(strings.NewReader(out))
	lines := 0
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		lines++
		switch {
		case f[0] == "cpu" && len(f) >= 8:
			// user nice system idle iowait irq softirq steal
			for i, v := range f[1:9] {
				n, _ := strconv.ParseUint(v, 10, 64)
				cpu.total += n
				if i == 3 || i == 4 {
					cpu.idle += n
				}
			}
		case f[0] == "MemTotal:" && len(f) >= 2:
			kb, _ := strconv.ParseUint(f[1], 10, 64)
			s.MemoryTotalBytes = kb << 10
		case f[0] == "MemAvailable:" && len(f) >= 2:
			kb, _ := strconv.ParseUint(f[1], 10, 64)
			s.MemoryAvailBytes = kb << 10
		case len(f) >= 5 && strings.Contains(f[3], "/"):
			// loadavg: "0.52 0.58 0.59 1/345 1234"
			s.Load1, _ = strconv.ParseFloat(f[0], 64)
			s.Load5, _ = strconv.ParseFloat(f[1], 64)
			s.Load15, _ = strconv.ParseFloat(f[2], 64)
		case len(f) >= 6 && strings.HasSuffix(f[4], "%"):
			// df -kP: sistema de ficheiros, blocos de 1K, usados, livres, uso%, ponto
			total, _ := strconv.ParseUint(f[1], 10, 64)
			avail, _ := strconv.ParseUint(f[3], 10, 64)
			s.DiskTotalBytes, s.DiskAvailableBytes = total<<10, avail<<10
		}
	}
	if cpu.total == 0 || s.MemoryTotalBytes == 0 || s.DiskTotalBytes == 0 {
		return s, cpu, fmt.Errorf("leitura incompleta (%d linhas): %q", lines, out)
	}
	return s, cpu, nil
}

// runHostMetrics recolhe de todos os nós online até ctx acabar.
func (s *Service) runHostMetrics(ctx context.Context) {
	tick := time.NewTicker(hostMetricsEvery)
	defer tick.Stop()
	for {
		s.collectHostMetrics(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) collectHostMetrics(ctx context.Context) {
	nodes, err := s.db.ListNodes(ctx)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, n := range nodes {
		d, err := s.dockerOn(n.Name)
		if err != nil {
			continue // offline
		}
		wg.Add(1)
		go func(node string, d Docker) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			out, err := s.hostMetricsExec(cctx, node, d)
			if err != nil {
				s.logger.Warn("métricas do nó", "node", node, "err", err)
				return
			}
			sample, cpu, err := parseHostMetrics(out)
			if err != nil {
				s.logger.Warn("métricas do nó", "node", node, "err", err)
				return
			}
			sample.At = time.Now().UTC()
			s.hm.add(node, sample, cpu)
		}(n.Name, d)
	}
	wg.Wait()
}

// hostMetricsExec corre a leitura no container de métricas do nó,
// criando-o se faltar.
func (s *Service) hostMetricsExec(ctx context.Context, node string, d Docker) (string, error) {
	out, err := s.execOutput(ctx, d, hostMetricsContainer, []string{"sh", "-c", hostMetricsCmd})
	if err == nil {
		return out, nil
	}
	if err := s.ensureHostMetricsContainer(ctx, node, d); err != nil {
		return "", err
	}
	return s.execOutput(ctx, d, hostMetricsContainer, []string{"sh", "-c", hostMetricsCmd})
}

func (s *Service) ensureHostMetricsContainer(ctx context.Context, node string, d Docker) error {
	if info, err := d.InspectContainer(ctx, hostMetricsContainer); err == nil {
		if info.State.Running {
			return nil
		}
		return d.StartContainer(ctx, hostMetricsContainer)
	}
	image := s.cfg.DebugImage
	if image == "" {
		image = defaultDebugImage
	}
	if _, err := d.InspectImage(ctx, image); err != nil {
		if err := d.PullImage(ctx, image); err != nil {
			return fmt.Errorf("buscando %s: %w", image, err)
		}
	}
	labels := map[string]string{dockerclient.PlatformLabel: dockerclient.PlatformLabelValue, LabelNode: node, LabelRole: RoleHostMetrics}
	if err := d.CreateVolume(ctx, hostMetricsVolume, labels); err != nil {
		return err
	}
	pids := int64(32)
	id, err := d.CreateContainer(ctx, hostMetricsContainer, dockerclient.CreateContainerRequest{
		Image:  image,
		Cmd:    []string{"sh", "-c", "while :; do sleep 3600; done"},
		Labels: labels,
		HostConfig: dockerclient.HardenApp(&dockerclient.CreateHostConfig{
			NetworkMode:   "none",
			RestartPolicy: dockerclient.UnlessStopped(),
			// Só para o df: o volume está no disco dos dados do Docker.
			Binds:     []string{hostMetricsVolume + ":/host/disk:ro"},
			Resources: dockerclient.Resources{NanoCPUs: 100_000_000, Memory: 32 << 20, MemorySwap: 32 << 20, PidsLimit: &pids},
		}),
	})
	if err != nil {
		return fmt.Errorf("criando o container de métricas: %w", err)
	}
	return d.StartContainer(ctx, id)
}

// execOutput corre um comando num container (sem TTY) e devolve o stdout
// e o stderr juntos.
func (s *Service) execOutput(ctx context.Context, d Docker, container string, cmd []string) (string, error) {
	id, err := d.CreateExec(ctx, container, dockerclient.ExecConfig{Cmd: cmd})
	if err != nil {
		return "", err
	}
	conn, err := d.StartExec(ctx, id, false)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	raw, err := io.ReadAll(io.LimitReader(conn, 1<<20))
	if err != nil {
		return "", err
	}
	return dockerclient.Demux(raw), nil
}

// HostMetrics é o histórico de um nó (as últimas horas).
func (s *Service) HostMetrics(ctx context.Context, node string, hours int) ([]HostSample, error) {
	if _, err := s.db.GetNode(ctx, node); err != nil {
		return nil, mapStoreErr(err)
	}
	if hours <= 0 || hours > 24 {
		hours = 24
	}
	return s.hm.since(node, time.Now().Add(-time.Duration(hours)*time.Hour)), nil
}

// ContainerUsage é o consumo de um container agora.
type ContainerUsage struct {
	Name        string  `json:"name"`
	Project     string  `json:"project,omitempty"`
	Environment string  `json:"environment,omitempty"`
	Service     string  `json:"service,omitempty"`
	Role        string  `json:"role,omitempty"`
	CPUPercent  float64 `json:"cpuPercent"`
	MemoryBytes uint64  `json:"memoryBytes"`
	MemoryLimit uint64  `json:"memoryLimitBytes"`
}

// TopContainers lê o consumo de cada container a correr num nó (o Docker
// leva ~1s por container; vão em paralelo).
func (s *Service) TopContainers(ctx context.Context, node string) ([]ContainerUsage, error) {
	d, err := s.dockerOn(node)
	if err != nil {
		return nil, err
	}
	containers, err := d.ListContainers(ctx, false, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ContainerUsage, len(containers))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, c := range containers {
		out[i] = ContainerUsage{
			Name: containerName(c), Project: c.Labels[LabelProject], Environment: c.Labels[LabelEnvironment],
			Service: c.Labels[LabelServiceSlug], Role: c.Labels[LabelRole],
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			st, err := d.ContainerStats(sctx, id)
			if err != nil {
				return
			}
			out[i].CPUPercent = st.CPUPercent()
			out[i].MemoryBytes = st.MemoryBytes()
			out[i].MemoryLimit = st.MemoryStats.Limit
		}(i, c.ID)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].MemoryBytes > out[j].MemoryBytes })
	return out, nil
}

// WriteHostMetrics escreve a última leitura de cada nó no formato de texto
// do Prometheus (o histórico longo fica lá).
func (s *Service) WriteHostMetrics(ctx context.Context, w io.Writer) error {
	nodes, err := s.db.ListNodes(ctx)
	if err != nil {
		return err
	}
	type metric struct {
		name, help string
		value      func(*HostSample) float64
	}
	metrics := []metric{
		{"bs_node_cpu_usage_percent", "CPU em uso na máquina (100 = todos os cores).", func(h *HostSample) float64 { return h.CPUPercent }},
		{"bs_node_load1", "Carga média de 1 minuto.", func(h *HostSample) float64 { return h.Load1 }},
		{"bs_node_load5", "Carga média de 5 minutos.", func(h *HostSample) float64 { return h.Load5 }},
		{"bs_node_load15", "Carga média de 15 minutos.", func(h *HostSample) float64 { return h.Load15 }},
		{"bs_node_memory_total_bytes", "Memória da máquina.", func(h *HostSample) float64 { return float64(h.MemoryTotalBytes) }},
		{"bs_node_memory_available_bytes", "Memória disponível.", func(h *HostSample) float64 { return float64(h.MemoryAvailBytes) }},
		{"bs_node_disk_total_bytes", "Disco dos dados do Docker.", func(h *HostSample) float64 { return float64(h.DiskTotalBytes) }},
		{"bs_node_disk_available_bytes", "Disco livre dos dados do Docker.", func(h *HostSample) float64 { return float64(h.DiskAvailableBytes) }},
	}
	samples := map[string]*HostSample{}
	for _, n := range nodes {
		if h := s.hm.latest(n.Name); h != nil {
			samples[n.Name] = h
		}
	}
	for _, m := range metrics {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", m.name, m.help, m.name)
		for _, n := range nodes {
			if h := samples[n.Name]; h != nil {
				fmt.Fprintf(w, "%s{node=%q} %g\n", m.name, n.Name, m.value(h))
			}
		}
	}
	fmt.Fprint(w, "# HELP bs_node_up 1 se o nó foi medido há pouco.\n# TYPE bs_node_up gauge\n")
	for _, n := range nodes {
		up := 0
		if samples[n.Name] != nil {
			up = 1
		}
		fmt.Fprintf(w, "bs_node_up{node=%q} %d\n", n.Name, up)
	}
	return nil
}
