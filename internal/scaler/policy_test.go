package scaler

import "testing"

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
