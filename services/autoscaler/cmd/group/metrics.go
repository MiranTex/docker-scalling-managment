package main

import (
	"net/http"
	"time"

	"autoscaler/internal/adminapi"
	"autoscaler/internal/discovery"
	"autoscaler/internal/loadbalancer"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// groupMetrics reúne as métricas Prometheus deste processo. Fica isolado
// num único registry (em vez do default global) pra não misturar com
// métricas de outra lib que por acaso registre no default — e pra dar pra
// instanciar mais de um em teste, se precisar.
type groupMetrics struct {
	registry *prometheus.Registry

	replicas        *prometheus.GaugeVec
	avgCPUPercent   *prometheus.GaugeVec
	healthyBackends *prometheus.GaugeVec
	draining        *prometheus.GaugeVec
	scaleActions    *prometheus.CounterVec
	proxyRequests   *prometheus.CounterVec
	proxyDuration   *prometheus.HistogramVec

	// Métricas por container (não por serviço): CPU/memória/rede/disco de
	// cada réplica individualmente, pra dar pra ver no Grafana qual
	// container específico está consumindo mais recurso dentro do grupo.
	containerCPUPercent    *prometheus.GaugeVec
	containerMemoryPercent *prometheus.GaugeVec
	containerMemoryBytes   *prometheus.GaugeVec
	containerNetworkRx     *prometheus.GaugeVec
	containerNetworkTx     *prometheus.GaugeVec
	containerDiskRead      *prometheus.GaugeVec
	containerDiskWrite     *prometheus.GaugeVec
}

func newGroupMetrics() *groupMetrics {
	registry := prometheus.NewRegistry()
	factory := promauto.With(registry)

	return &groupMetrics{
		registry: registry,

		replicas: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_replicas",
			Help: "Número atual de réplicas do serviço gerenciado por este grupo.",
		}, []string{"service"}),

		avgCPUPercent: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_avg_cpu_percent",
			Help: "Média de CPU (%) entre as réplicas do serviço na última avaliação.",
		}, []string{"service"}),

		healthyBackends: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_healthy_backends",
			Help: "Número de containers saudáveis disponíveis no load balancer.",
		}, []string{"service"}),

		draining: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_draining_containers",
			Help: "Número de containers escolhidos para scale down que ainda estão drenando.",
		}, []string{"service"}),

		scaleActions: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "autoscaler_scale_actions_total",
			Help: "Total de ações de scaling aplicadas, por tipo.",
		}, []string{"service", "action"}),

		proxyRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "autoscaler_proxy_requests_total",
			Help: "Total de requisições encaminhadas pelo load balancer, por status HTTP.",
		}, []string{"service", "status"}),

		proxyDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "autoscaler_proxy_request_duration_seconds",
			Help:    "Duração das requisições encaminhadas pelo load balancer.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service"}),

		containerCPUPercent: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_container_cpu_percent",
			Help: "Uso de CPU (%) de cada container individualmente.",
		}, []string{"service", "container_name"}),

		containerMemoryPercent: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_container_memory_percent",
			Help: "Uso de memória (%) de cada container, relativo ao limite configurado.",
		}, []string{"service", "container_name"}),

		containerMemoryBytes: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_container_memory_usage_bytes",
			Help: "Uso de memória (bytes) de cada container.",
		}, []string{"service", "container_name"}),

		// Rede e disco são contadores acumulados desde que o container
		// subiu (o Docker não devolve uma taxa pronta) -- por isso viram
		// gauge aqui: quem calcula bytes/segundo é a query no Grafana
		// (rate()/increase()), não este processo.
		containerNetworkRx: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_container_network_receive_bytes",
			Help: "Total de bytes recebidos pelo container desde que subiu (contador acumulado).",
		}, []string{"service", "container_name"}),

		containerNetworkTx: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_container_network_transmit_bytes",
			Help: "Total de bytes transmitidos pelo container desde que subiu (contador acumulado).",
		}, []string{"service", "container_name"}),

		containerDiskRead: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_container_disk_read_bytes",
			Help: "Total de bytes lidos de disco pelo container desde que subiu (contador acumulado).",
		}, []string{"service", "container_name"}),

		containerDiskWrite: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoscaler_container_disk_write_bytes",
			Help: "Total de bytes escritos em disco pelo container desde que subiu (contador acumulado).",
		}, []string{"service", "container_name"}),
	}
}

// updateContainerStats substitui os valores por container pelas leituras
// mais recentes. Faz Reset() em cada métrica antes de repopular: sem isso,
// um container removido num scale down deixaria uma série "fantasma"
// (última leitura conhecida, parada pra sempre) no Prometheus/Grafana.
func (m *groupMetrics) updateContainerStats(service string, stats []discovery.ContainerStats) {
	m.containerCPUPercent.Reset()
	m.containerMemoryPercent.Reset()
	m.containerMemoryBytes.Reset()
	m.containerNetworkRx.Reset()
	m.containerNetworkTx.Reset()
	m.containerDiskRead.Reset()
	m.containerDiskWrite.Reset()

	for _, s := range stats {
		m.containerCPUPercent.WithLabelValues(service, s.Name).Set(s.CPUPercent)
		m.containerMemoryPercent.WithLabelValues(service, s.Name).Set(s.MemoryPercent)
		m.containerMemoryBytes.WithLabelValues(service, s.Name).Set(float64(s.MemoryUsageBytes))
		m.containerNetworkRx.WithLabelValues(service, s.Name).Set(float64(s.NetworkRxBytes))
		m.containerNetworkTx.WithLabelValues(service, s.Name).Set(float64(s.NetworkTxBytes))
		m.containerDiskRead.WithLabelValues(service, s.Name).Set(float64(s.DiskReadBytes))
		m.containerDiskWrite.WithLabelValues(service, s.Name).Set(float64(s.DiskWriteBytes))
	}
}

// instrumentProxy envolve o handler do load balancer com contagem de
// requisições e histograma de duração. Fica em cmd/group (não em
// internal/loadbalancer) pra não empurrar a dependência do Prometheus pros
// pacotes internos, que hoje não têm dependências externas.
func (m *groupMetrics) instrumentProxy(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		m.proxyRequests.WithLabelValues(service, http.StatusText(rec.status)).Inc()
		m.proxyDuration.WithLabelValues(service).Observe(time.Since(start).Seconds())
	})
}

// statusRecorder captura o status code de verdade escrito pelo handler
// encapsulado — http.ResponseWriter não expõe isso por padrão.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// healthzHandler é o liveness check: responde 200 sempre que o processo
// está de pé e atendendo HTTP, independente do estado dos backends.
func healthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

// readyzHandler é o readiness check: só responde 200 se o load balancer
// tem ao menos um backend saudável no momento — sinaliza pro orquestrador
// não mandar tráfego real pra cá enquanto isso não for verdade.
func readyzHandler(balancer *loadbalancer.RoundRobin) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !balancer.HasBackends() {
			http.Error(w, "sem backends saudáveis", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

// newAdminServer monta o servidor HTTP separado (porta própria) que expõe
// /healthz, /readyz, /metrics e as rotas administrativas autenticadas
// (/v1/status, /v1/policy, /v1/restart -- ver internal/adminapi). Fica fora
// do listener principal (que só serve o proxy) pra esses endpoints não
// colidirem com o roteamento "qualquer caminho vai pro backend" do load
// balancer. As rotas admin exigem role infra-admin; healthz/readyz/metrics
// continuam sem autenticação, como já dependem Prometheus e o healthcheck
// do próprio container.
func newAdminServer(addr string, metrics *groupMetrics, balancer *loadbalancer.RoundRobin, admin *adminapi.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/healthz", healthzHandler())
	mux.Handle("/readyz", readyzHandler(balancer))
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))
	admin.Register(mux)

	return &http.Server{Addr: addr, Handler: mux}
}
