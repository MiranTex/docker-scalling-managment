package main

import (
	"fmt"
	"log"
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

		policy: scaler.Policy{
			MinReplicas:         envInt("MIN_REPLICAS", 1),
			MaxReplicas:         envInt("MAX_REPLICAS", 3),
			CPUScaleUpPercent:   envFloat("CPU_SCALE_UP_PERCENT", 50),
			CPUScaleDownPercent: envFloat("CPU_SCALE_DOWN_PERCENT", 20),
		},
	}

	if cfg.targetService == "" {
		log.Fatal("group: variável de ambiente TARGET_SERVICE é obrigatória (qual serviço este grupo gerencia)")
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
		log.Fatalf("group: %s=%q inválido, esperava um inteiro: %v", key, v, err)
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
		log.Fatalf("group: %s=%q inválido, esperava um número: %v", key, v, err)
	}
	return n
}

func envSeconds(key string, defSeconds int) time.Duration {
	return time.Duration(envInt(key, defSeconds)) * time.Second
}

func envMillis(key string, defMillis int) time.Duration {
	return time.Duration(envInt(key, defMillis)) * time.Millisecond
}

// summary é só uma linha de log legível pra conferir a configuração efetiva
// ao subir — útil quando ela vem inteira de variáveis de ambiente e não dá
// pra "ver" no código.
func (c config) summary() string {
	healthCheck := "tcp"
	if c.healthCheckPath != "" {
		healthCheck = "http " + c.healthCheckPath
	}
	return fmt.Sprintf(
		"service=%s label=%s port=%d listen=%s replicas=[%d,%d] cpu=[%.0f%%,%.0f%%] tick=%s health=%s",
		c.targetService, c.serviceLabel, c.backendPort, c.listenAddr,
		c.policy.MinReplicas, c.policy.MaxReplicas,
		c.policy.CPUScaleDownPercent, c.policy.CPUScaleUpPercent, c.reconcileTick, healthCheck,
	)
}
