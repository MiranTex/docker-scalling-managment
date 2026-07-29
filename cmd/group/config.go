package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"autoscaler/internal/scaler"
)

// config reúne tudo que hoje é fixo em código e precisa variar por serviço
// quando rodamos várias instâncias do grupo (uma por serviço) em containers
// diferentes, cada uma configurada só por variáveis de ambiente.
type config struct {
	dockerSocket string
	serviceLabel string
	// targetService é o valor da label que identifica o serviço que ESTA
	// instância do grupo gerencia. Não tem default: cada instância cuida de
	// um serviço só, então rodar sem isso definido é um erro de configuração,
	// não um caso razoável de usar um valor padrão.
	targetService string
	backendPort   int
	listenAddr    string

	reconcileTick time.Duration
	// healthCheckPath vazio faz um simples dial TCP; definido (ex: "/health")
	// faz um GET HTTP nesse caminho e exige status < 400.
	healthCheckPath    string
	healthCheckTimeout time.Duration

	// drainTimeout é quanto tempo um container escolhido para scale down
	// fica fora dos backends do load balancer antes de ser efetivamente
	// parado — dá tempo das requisições já em andamento nele terminarem.
	drainTimeout time.Duration

	// shutdownTimeout é quanto tempo o próprio processo group espera, ao
	// receber SIGTERM/SIGINT, pelas requisições HTTP em andamento
	// terminarem antes de encerrar à força.
	shutdownTimeout time.Duration

	policy scaler.Policy
}

// loadConfig lê a configuração das variáveis de ambiente, aplicando defaults
// sensatos pra tudo que não seja obrigatório. Chame godotenv.Load() antes,
// se quiser carregar um .env local.
func loadConfig() config {
	cfg := config{
		dockerSocket:  envString("DOCKER_SOCKET", "/var/run/docker.sock"),
		serviceLabel:  envString("SERVICE_LABEL", "autoscaler.service"),
		targetService: os.Getenv("TARGET_SERVICE"),
		backendPort:   envInt("BACKEND_PORT", 80),
		listenAddr:    envString("LISTEN_ADDR", ":8090"),

		reconcileTick:      envSeconds("RECONCILE_TICK_SECONDS", 3),
		healthCheckPath:    os.Getenv("HEALTH_CHECK_PATH"),
		healthCheckTimeout: envMillis("HEALTH_CHECK_TIMEOUT_MS", 500),
		drainTimeout:       envSeconds("DRAIN_TIMEOUT_SECONDS", 10),
		shutdownTimeout:    envSeconds("SHUTDOWN_TIMEOUT_SECONDS", 15),

		policy: scaler.Policy{
			MinReplicas:         envInt("MIN_REPLICAS", 1),
			MaxReplicas:         envInt("MAX_REPLICAS", 3),
			CPUScaleUpPercent:   envFloat("CPU_SCALE_UP_PERCENT", 50),
			CPUScaleDownPercent: envFloat("CPU_SCALE_DOWN_PERCENT", 20),
			SustainedTicks:      envInt("SUSTAINED_TICKS", 2),
			Cooldown:            envSeconds("COOLDOWN_SECONDS", 30),
		},
	}

	if cfg.targetService == "" {
		slog.Error("configuração inválida: TARGET_SERVICE é obrigatória (qual serviço este grupo gerencia)")
		os.Exit(1)
	}
	return cfg
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Error("configuração inválida", "var", key, "value", v, "want", "inteiro", "err", err)
		os.Exit(1)
	}
	return n
}

func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		slog.Error("configuração inválida", "var", key, "value", v, "want", "número", "err", err)
		os.Exit(1)
	}
	return n
}

func envSeconds(key string, defSeconds int) time.Duration {
	return time.Duration(envInt(key, defSeconds)) * time.Second
}

func envMillis(key string, defMillis int) time.Duration {
	return time.Duration(envInt(key, defMillis)) * time.Millisecond
}

// logAttrs devolve a configuração efetiva como pares chave/valor prontos
// pra passar a um slog.Logger — útil pra conferir no log de startup como o
// processo ficou configurado, já que ela vem inteira de variáveis de
// ambiente e não dá pra "ver" no código.
func (c config) logAttrs() []any {
	healthCheck := "tcp"
	if c.healthCheckPath != "" {
		healthCheck = "http:" + c.healthCheckPath
	}
	return []any{
		"service", c.targetService,
		"label", c.serviceLabel,
		"backend_port", c.backendPort,
		"listen_addr", c.listenAddr,
		"min_replicas", c.policy.MinReplicas,
		"max_replicas", c.policy.MaxReplicas,
		"cpu_scale_down_percent", c.policy.CPUScaleDownPercent,
		"cpu_scale_up_percent", c.policy.CPUScaleUpPercent,
		"reconcile_tick", c.reconcileTick.String(),
		"sustained_ticks", c.policy.SustainedTicks,
		"cooldown", c.policy.Cooldown.String(),
		"drain_timeout", c.drainTimeout.String(),
		"shutdown_timeout", c.shutdownTimeout.String(),
		"health_check", healthCheck,
	}
}
