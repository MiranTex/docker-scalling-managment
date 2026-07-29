package main

import (
	"net/http"
	"time"

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
// /healthz, /readyz e /metrics. Fica fora do listener principal (que só
// serve o proxy) pra esses endpoints não colidirem com o roteamento
// "qualquer caminho vai pro backend" do load balancer.
func newAdminServer(addr string, metrics *groupMetrics, balancer *loadbalancer.RoundRobin) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/healthz", healthzHandler())
	mux.Handle("/readyz", readyzHandler(balancer))
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))

	return &http.Server{Addr: addr, Handler: mux}
}
