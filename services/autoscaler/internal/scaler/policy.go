// Package scaler contém a lógica de decisão de autoscaling. É deliberadamente
// isolado do dockerclient: recebe métricas já agregadas e devolve uma decisão,
// sem saber nada sobre containers, sockets ou HTTP. Isso deixa a regra em si
// fácil de testar e de raciocinar sobre.
package scaler

import (
	"fmt"
	"sync"
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
	// Immediate é true quando a ação não deve esperar SustainedTicks no
	// Evaluator com estado. Hoje só a correção de "abaixo do mínimo" usa
	// isso: violar o piso de réplicas (inclusive chegar a zero) não é um
	// sinal que possa estar oscilando como CPU perto de um limiar --
	// esperar vários ticks "pra confirmar" só atrasaria desnecessariamente
	// a correção de um estado que já sabemos que é inválido.
	Immediate bool
}

// Evaluate aplica a policy às métricas atuais do serviço e decide se deve
// escalar. Por enquanto escala sempre de 1 em 1 réplica por avaliação — é a
// forma mais simples de evitar oscilações bruscas; políticas mais espertas
// (escalar proporcional à carga, cooldown entre decisões) podem vir depois.
func Evaluate(policy Policy, metrics ServiceMetrics) Decision {
	// MinReplicas como alvo ativo, não só piso de scale-down: se as
	// réplicas atuais já estão abaixo do mínimo (inclusive zero, no
	// bootstrap), sobe imediatamente, antes de olhar CPU. Isso cobre tanto
	// "acabei de subir e ainda não atingi o mínimo" quanto "algo removeu
	// réplicas demais" -- em ambos os casos é uma violação de invariante,
	// não uma decisão sensível à carga.
	if metrics.Replicas < policy.MinReplicas {
		return Decision{
			Action:    ScaleUp,
			Delta:     1,
			Reason:    fmt.Sprintf("replicas %d abaixo do mínimo de %d réplicas", metrics.Replicas, policy.MinReplicas),
			Immediate: true,
		}
	}

	if metrics.AvgCPUPercent >= policy.CPUScaleUpPercent {
		if metrics.Replicas >= policy.MaxReplicas {
			return Decision{Action: NoAction, Reason: fmt.Sprintf(
				"cpu %.1f%% >= limite de scale up (%.1f%%), mas já está no máximo de %d réplicas",
				metrics.AvgCPUPercent, policy.CPUScaleUpPercent, policy.MaxReplicas)}
		}
		return Decision{Action: ScaleUp, Delta: 1, Reason: fmt.Sprintf(
			"cpu %.1f%% >= limite de scale up (%.1f%%)", metrics.AvgCPUPercent, policy.CPUScaleUpPercent)}
	}

	if metrics.AvgCPUPercent <= policy.CPUScaleDownPercent {
		if metrics.Replicas <= policy.MinReplicas {
			return Decision{Action: NoAction, Reason: fmt.Sprintf(
				"cpu %.1f%% <= limite de scale down (%.1f%%), mas já está no mínimo de %d réplicas",
				metrics.AvgCPUPercent, policy.CPUScaleDownPercent, policy.MinReplicas)}
		}
		return Decision{Action: ScaleDown, Delta: -1, Reason: fmt.Sprintf(
			"cpu %.1f%% <= limite de scale down (%.1f%%)", metrics.AvgCPUPercent, policy.CPUScaleDownPercent)}
	}

	return Decision{Action: NoAction, Reason: fmt.Sprintf(
		"cpu %.1f%% dentro dos limites (%.1f%% - %.1f%%)",
		metrics.AvgCPUPercent, policy.CPUScaleDownPercent, policy.CPUScaleUpPercent)}
}

// Evaluator envolve Evaluate com o estado necessário para aplicar
// ticks sustentados e cooldown entre ações — técnicas que, ao contrário da
// histerese (limiares separados), dependem do histórico de avaliações e por
// isso não cabem numa função pura.
type Evaluator struct {
	// mu protege policy e o estado de ticks/cooldown abaixo: Evaluate roda
	// no reconcile loop, enquanto Policy/SetPolicy agora também podem ser
	// chamados a partir da API admin (goroutine HTTP separada) para permitir
	// edição a quente da policy sem reiniciar o processo.
	mu sync.RWMutex

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

// Policy devolve a policy em vigor neste momento.
func (e *Evaluator) Policy() Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policy
}

// SetPolicy substitui a policy em vigor, aplicada já na próxima chamada a
// Evaluate. Deliberadamente não reseta consecutiveUp/consecutiveDown/
// lastAction: uma sequência de ticks já contada ou um cooldown já em
// andamento continuam válidos sob os novos limiares -- mudar um threshold a
// meio de uma janela não deveria descartar o progresso já observado.
func (e *Evaluator) SetPolicy(p Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policy = p
}

// Evaluate aplica a policy às métricas atuais, mas só devolve uma ação de
// scale up/down depois que ela se repetir por SustainedTicks avaliações
// consecutivas e o Cooldown desde a última ação já tiver passado. Nos
// demais casos devolve NoAction com o motivo do bloqueio.
func (e *Evaluator) Evaluate(metrics ServiceMetrics, now time.Time) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()

	raw := Evaluate(e.policy, metrics)

	if raw.Action == NoAction {
		e.consecutiveUp = 0
		e.consecutiveDown = 0
		return raw
	}

	// Immediate (hoje só a correção de abaixo-do-mínimo) pula tanto ticks
	// sustentados quanto cooldown: é reação a um piso de disponibilidade
	// violado -- inclusive zero réplicas -- não a um sinal de carga que
	// possa estar oscilando. Diferente de CPU perto de um limiar, não há
	// ambiguidade sobre a ação ser necessária, e atrasá-la só prolonga um
	// período com capacidade insuficiente. Também não mexe em lastAction:
	// uma correção de bootstrap não deve fazer uma decisão CPU-driven
	// legítima logo em seguida esperar cooldown por causa disso.
	if raw.Immediate {
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
		return Decision{Action: NoAction, Reason: fmt.Sprintf(
			"%s pendente: %d/%d ticks consecutivos (%s)", raw.Action, *consecutive, sustained, raw.Reason)}
	}

	if e.policy.Cooldown > 0 && !e.lastAction.IsZero() {
		if elapsed := now.Sub(e.lastAction); elapsed < e.policy.Cooldown {
			return Decision{Action: NoAction, Reason: fmt.Sprintf(
				"%s bloqueado por cooldown: faltam %s (%s)",
				raw.Action, (e.policy.Cooldown - elapsed).Round(time.Second), raw.Reason)}
		}
	}

	e.lastAction = now
	e.consecutiveUp = 0
	e.consecutiveDown = 0
	return raw
}
