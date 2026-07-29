package scaler

import (
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	policy := Policy{
		MinReplicas:         1,
		MaxReplicas:         5,
		CPUScaleUpPercent:   70,
		CPUScaleDownPercent: 20,
	}

	cases := []struct {
		name     string
		metrics  ServiceMetrics
		wantAct  Action
		wantDiff int
	}{
		{"cpu alta escala pra cima", ServiceMetrics{Replicas: 2, AvgCPUPercent: 80}, ScaleUp, 1},
		{"cpu baixa escala pra baixo", ServiceMetrics{Replicas: 2, AvgCPUPercent: 5}, ScaleDown, -1},
		{"cpu no meio não faz nada", ServiceMetrics{Replicas: 2, AvgCPUPercent: 50}, NoAction, 0},
		{"cpu alta mas já no máximo", ServiceMetrics{Replicas: 5, AvgCPUPercent: 90}, NoAction, 0},
		{"cpu baixa mas já no mínimo", ServiceMetrics{Replicas: 1, AvgCPUPercent: 0}, NoAction, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(policy, tc.metrics)
			if got.Action != tc.wantAct {
				t.Errorf("action = %v, want %v (reason: %s)", got.Action, tc.wantAct, got.Reason)
			}
			if got.Delta != tc.wantDiff {
				t.Errorf("delta = %d, want %d", got.Delta, tc.wantDiff)
			}
		})
	}
}

func TestEvaluatorSustainedTicks(t *testing.T) {
	policy := Policy{
		MinReplicas: 1, MaxReplicas: 5,
		CPUScaleUpPercent: 70, CPUScaleDownPercent: 20,
		SustainedTicks: 3,
	}
	e := NewEvaluator(policy)
	now := time.Unix(0, 0)
	high := ServiceMetrics{Replicas: 2, AvgCPUPercent: 90}

	if d := e.Evaluate(high, now); d.Action != NoAction {
		t.Fatalf("tick 1: action = %v, want NoAction (reason: %s)", d.Action, d.Reason)
	}
	if d := e.Evaluate(high, now); d.Action != NoAction {
		t.Fatalf("tick 2: action = %v, want NoAction (reason: %s)", d.Action, d.Reason)
	}
	d := e.Evaluate(high, now)
	if d.Action != ScaleUp || d.Delta != 1 {
		t.Fatalf("tick 3: action = %v delta = %d, want ScaleUp/1 (reason: %s)", d.Action, d.Delta, d.Reason)
	}
}

func TestEvaluatorResetsOnDip(t *testing.T) {
	policy := Policy{
		MinReplicas: 1, MaxReplicas: 5,
		CPUScaleUpPercent: 70, CPUScaleDownPercent: 20,
		SustainedTicks: 2,
	}
	e := NewEvaluator(policy)
	now := time.Unix(0, 0)
	high := ServiceMetrics{Replicas: 2, AvgCPUPercent: 90}
	mid := ServiceMetrics{Replicas: 2, AvgCPUPercent: 50}

	e.Evaluate(high, now)      // 1 tick alto
	e.Evaluate(mid, now)       // oscilação: zera o contador
	d := e.Evaluate(high, now) // volta a 1 tick alto, não deveria escalar ainda
	if d.Action != NoAction {
		t.Fatalf("action = %v, want NoAction após oscilação (reason: %s)", d.Action, d.Reason)
	}
}

func TestEvaluatorCooldown(t *testing.T) {
	policy := Policy{
		MinReplicas: 1, MaxReplicas: 5,
		CPUScaleUpPercent: 70, CPUScaleDownPercent: 20,
		SustainedTicks: 1,
		Cooldown:       30 * time.Second,
	}
	e := NewEvaluator(policy)
	t0 := time.Unix(0, 0)
	high := ServiceMetrics{Replicas: 2, AvgCPUPercent: 90}

	d := e.Evaluate(high, t0)
	if d.Action != ScaleUp {
		t.Fatalf("primeira ação = %v, want ScaleUp (reason: %s)", d.Action, d.Reason)
	}

	d = e.Evaluate(high, t0.Add(10*time.Second))
	if d.Action != NoAction {
		t.Fatalf("dentro do cooldown: action = %v, want NoAction (reason: %s)", d.Action, d.Reason)
	}

	d = e.Evaluate(high, t0.Add(31*time.Second))
	if d.Action != ScaleUp {
		t.Fatalf("após cooldown: action = %v, want ScaleUp (reason: %s)", d.Action, d.Reason)
	}
}
