// Package scaler contém a lógica de decisão de autoscaling. É deliberadamente
// isolado do dockerclient: recebe métricas já agregadas e devolve uma decisão,
// sem saber nada sobre containers, sockets ou HTTP. Isso deixa a regra em si
// fácil de testar e de raciocinar sobre.
package scaler

import (
	"fmt"
	"time"
)

// Policy define os limites que controlam o autoscaling de um serviço.
type Policy struct {
	MinReplicas int
	MaxReplicas int

	// CPUScaleUpPercent: média de CPU do serviço acima disso -> scale up.
	CPUScaleUpPercent float64
	// CPUScaleDownPercent: média de CPU do serviço abaixo disso -> scale down.
	// A diferença entre os dois limiares já forma uma zona neutra (histerese):
	// CPU entre os dois valores não aciona nenhuma ação.
	CPUScaleDownPercent float64

	// SustainedTicks: quantas avaliações CONSECUTIVAS a métrica precisa
	// ficar fora do limiar antes de uma ação ser efetivamente disparada.
	// Evita reagir a um pico ou vale isolado de CPU. 0 ou 1 = reage já na
	// primeira avaliação (comportamento antigo).
	SustainedTicks int

	// Cooldown: tempo mínimo entre duas ações de scaling. Depois de escalar,
	// novas ações ficam bloqueadas até o cooldown passar, mesmo que a
	// métrica continue fora do limiar. Evita ficar escalando repetidamente
	// enquanto o efeito da última ação ainda não se refletiu na métrica.
	// Zero desativa o cooldown.
	Cooldown time.Duration
}

// ServiceMetrics é o estado observado de um serviço no momento da avaliação:
// quantas réplicas existem agora e a média de CPU entre elas.
type ServiceMetrics struct {
	Name          string
	Replicas      int
	AvgCPUPercent float64
}

// Action é o tipo de ação que uma Decision recomenda.
type Action int

const (
	NoAction Action = iota
	ScaleUp
	ScaleDown
)

func (a Action) String() string {
	switch a {
	case ScaleUp:
		return "scale_up"
	case ScaleDown:
		return "scale_down"
	default:
		return "no_action"
	}
}

// Decision é o resultado de avaliar uma Policy contra ServiceMetrics.
// Delta é quantas réplicas adicionar (positivo) ou remover (negativo).
type Decision struct {
	Action Action
	Delta  int
	Reason string
}

// Evaluate aplica a policy às métricas atuais do serviço e decide se deve
// escalar. Por enquanto escala sempre de 1 em 1 réplica por avaliação — é a
// forma mais simples de evitar oscilações bruscas; políticas mais espertas
// (escalar proporcional à carga, cooldown entre decisões) podem vir depois.
func Evaluate(policy Policy, metrics ServiceMetrics) Decision {
	if metrics.AvgCPUPercent >= policy.CPUScaleUpPercent {
		if metrics.Replicas >= policy.MaxReplicas {
			return Decision{NoAction, 0, fmt.Sprintf(
				"cpu %.1f%% >= limite de scale up (%.1f%%), mas já está no máximo de %d réplicas",
				metrics.AvgCPUPercent, policy.CPUScaleUpPercent, policy.MaxReplicas)}
		}
		return Decision{ScaleUp, 1, fmt.Sprintf(
			"cpu %.1f%% >= limite de scale up (%.1f%%)", metrics.AvgCPUPercent, policy.CPUScaleUpPercent)}
	}

	if metrics.AvgCPUPercent <= policy.CPUScaleDownPercent {
		if metrics.Replicas <= policy.MinReplicas {
			return Decision{NoAction, 0, fmt.Sprintf(
				"cpu %.1f%% <= limite de scale down (%.1f%%), mas já está no mínimo de %d réplicas",
				metrics.AvgCPUPercent, policy.CPUScaleDownPercent, policy.MinReplicas)}
		}
		return Decision{ScaleDown, -1, fmt.Sprintf(
			"cpu %.1f%% <= limite de scale down (%.1f%%)", metrics.AvgCPUPercent, policy.CPUScaleDownPercent)}
	}

	return Decision{NoAction, 0, fmt.Sprintf(
		"cpu %.1f%% dentro dos limites (%.1f%% - %.1f%%)",
		metrics.AvgCPUPercent, policy.CPUScaleDownPercent, policy.CPUScaleUpPercent)}
}

// Evaluator envolve Evaluate com o estado necessário para aplicar
// ticks sustentados e cooldown entre ações — técnicas que, ao contrário da
// histerese (limiares separados), dependem do histórico de avaliações e por
// isso não cabem numa função pura.
type Evaluator struct {
	policy Policy

	consecutiveUp   int
	consecutiveDown int
	lastAction      time.Time
}

// NewEvaluator cria um Evaluator para a policy dada. Uma instância deve ser
// reutilizada entre ticks de um mesmo serviço: o estado de ticks
// consecutivos e cooldown só faz sentido acumulado ao longo do tempo.
func NewEvaluator(policy Policy) *Evaluator {
	return &Evaluator{policy: policy}
}

// Evaluate aplica a policy às métricas atuais, mas só devolve uma ação de
// scale up/down depois que ela se repetir por SustainedTicks avaliações
// consecutivas e o Cooldown desde a última ação já tiver passado. Nos
// demais casos devolve NoAction com o motivo do bloqueio.
func (e *Evaluator) Evaluate(metrics ServiceMetrics, now time.Time) Decision {
	raw := Evaluate(e.policy, metrics)

	if raw.Action == NoAction {
		e.consecutiveUp = 0
		e.consecutiveDown = 0
		return raw
	}

	consecutive := &e.consecutiveUp
	other := &e.consecutiveDown
	if raw.Action == ScaleDown {
		consecutive, other = other, consecutive
	}
	*consecutive++
	*other = 0

	sustained := e.policy.SustainedTicks
	if sustained < 1 {
		sustained = 1
	}
	if *consecutive < sustained {
		return Decision{NoAction, 0, fmt.Sprintf(
			"%s pendente: %d/%d ticks consecutivos (%s)", raw.Action, *consecutive, sustained, raw.Reason)}
	}

	if e.policy.Cooldown > 0 && !e.lastAction.IsZero() {
		if elapsed := now.Sub(e.lastAction); elapsed < e.policy.Cooldown {
			return Decision{NoAction, 0, fmt.Sprintf(
				"%s bloqueado por cooldown: faltam %s (%s)",
				raw.Action, (e.policy.Cooldown - elapsed).Round(time.Second), raw.Reason)}
		}
	}

	e.lastAction = now
	e.consecutiveUp = 0
	e.consecutiveDown = 0
	return raw
}
