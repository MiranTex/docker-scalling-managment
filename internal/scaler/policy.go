// Package scaler contém a lógica de decisão de autoscaling. É deliberadamente
// isolado do dockerclient: recebe métricas já agregadas e devolve uma decisão,
// sem saber nada sobre containers, sockets ou HTTP. Isso deixa a regra em si
// fácil de testar e de raciocinar sobre.
package scaler

import "fmt"

// Policy define os limites que controlam o autoscaling de um serviço.
type Policy struct {
	MinReplicas int
	MaxReplicas int

	// CPUScaleUpPercent: média de CPU do serviço acima disso -> scale up.
	CPUScaleUpPercent float64
	// CPUScaleDownPercent: média de CPU do serviço abaixo disso -> scale down.
	CPUScaleDownPercent float64
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
