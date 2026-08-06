package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"autoscaler/internal/audit"
	"autoscaler/internal/jwtverify"
	"autoscaler/internal/scaler"
)

// fakeVerifier aceita um conjunto fixo de tokens de teste -> claims, sem
// tocar em JWKS/rede nenhuma -- só o suficiente para exercitar requireRole.
type fakeVerifier struct {
	tokens map[string]jwtverify.Claims
}

func (f *fakeVerifier) Verify(ctx context.Context, tokenString string) (jwtverify.Claims, error) {
	claims, ok := f.tokens[tokenString]
	if !ok {
		return jwtverify.Claims{}, jwtverify.ErrInvalidToken
	}
	return claims, nil
}

// fakeGroupControl é um GroupControl em memória, com mutex próprio, para
// testar tanto a lógica de validação/handlers quanto concorrência de
// pedidos HTTP simultâneos sem depender de Docker nenhum.
type fakeGroupControl struct {
	mu           sync.Mutex
	policy       scaler.Policy
	setPolicyErr error
	restartErr   error
	restarts     int
	status       Status
	statusErr    error
}

func newFakeGroupControl() *fakeGroupControl {
	return &fakeGroupControl{
		policy: scaler.Policy{MinReplicas: 1, MaxReplicas: 3, CPUScaleUpPercent: 50, CPUScaleDownPercent: 20, SustainedTicks: 2, Cooldown: 0},
	}
}

func (f *fakeGroupControl) Status(ctx context.Context) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return Status{}, f.statusErr
	}
	return f.status, nil
}

func (f *fakeGroupControl) Policy() scaler.Policy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.policy
}

func (f *fakeGroupControl) SetPolicy(p scaler.Policy) (scaler.Policy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setPolicyErr != nil {
		return scaler.Policy{}, f.setPolicyErr
	}
	f.policy = p
	return f.policy, nil
}

func (f *fakeGroupControl) Restart(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restartErr != nil {
		return f.restartErr
	}
	f.restarts++
	return nil
}

func newTestHandler(group GroupControl) (*Handler, *fakeVerifier) {
	verifier := &fakeVerifier{tokens: map[string]jwtverify.Claims{
		"infra-admin-token": {Subject: "operator@example.com", Role: RoleInfraAdmin},
		"user-token":        {Subject: "user@example.com", Role: "user"},
	}}
	auditLogger := audit.NewLogger(slog.Default())
	return NewHandler(verifier, group, auditLogger), verifier
}

func doRequest(t *testing.T, mux *http.ServeMux, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != "" {
		reqBody = bytes.NewReader([]byte(body))
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRequireRoleRejectsMissingToken(t *testing.T) {
	h, _ := newTestHandler(newFakeGroupControl())
	mux := http.NewServeMux()
	h.Register(mux)

	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/status"},
		{"GET", "/v1/policy"},
		{"PUT", "/v1/policy"},
		{"POST", "/v1/restart"},
	} {
		rec := doRequest(t, mux, route.method, route.path, "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s sem token: status = %d, want 401", route.method, route.path, rec.Code)
		}
	}
}

func TestRequireRoleRejectsWrongRole(t *testing.T) {
	h, _ := newTestHandler(newFakeGroupControl())
	mux := http.NewServeMux()
	h.Register(mux)

	rec := doRequest(t, mux, "GET", "/v1/status", "user-token", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 para role \"user\"", rec.Code)
	}
}

func TestRequireRoleAcceptsInfraAdmin(t *testing.T) {
	h, _ := newTestHandler(newFakeGroupControl())
	mux := http.NewServeMux()
	h.Register(mux)

	rec := doRequest(t, mux, "GET", "/v1/status", "infra-admin-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandlePutPolicyValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"min maior que max", `{"min_replicas":5,"max_replicas":3}`},
		{"scale up menor ou igual a scale down", `{"cpu_scale_up_percent":10,"cpu_scale_down_percent":20}`},
		{"scale down negativo", `{"cpu_scale_down_percent":-5}`},
		{"max replicas zero", `{"max_replicas":0}`},
		{"sustained ticks negativo", `{"sustained_ticks":-1}`},
		{"cooldown negativo", `{"cooldown_seconds":-1}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeGroupControl()
			before := fake.policy
			h, _ := newTestHandler(fake)
			mux := http.NewServeMux()
			h.Register(mux)

			rec := doRequest(t, mux, "PUT", "/v1/policy", "infra-admin-token", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if fake.policy != before {
				t.Fatalf("policy foi alterada apesar da validação falhar: %+v", fake.policy)
			}
		})
	}
}

func TestHandlePutPolicyPartialUpdate(t *testing.T) {
	fake := newFakeGroupControl()
	h, _ := newTestHandler(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := doRequest(t, mux, "PUT", "/v1/policy", "infra-admin-token", `{"max_replicas":10}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var dto PolicyDTO
	if err := json.NewDecoder(rec.Body).Decode(&dto); err != nil {
		t.Fatalf("resposta não é JSON válido: %v", err)
	}
	if dto.MaxReplicas == nil || *dto.MaxReplicas != 10 {
		t.Fatalf("max_replicas = %v, want 10", dto.MaxReplicas)
	}
	if dto.MinReplicas == nil || *dto.MinReplicas != 1 {
		t.Fatalf("min_replicas = %v, want 1 (não deveria ter mudado)", dto.MinReplicas)
	}
	if fake.Policy().MaxReplicas != 10 {
		t.Fatalf("fakeGroupControl.policy.MaxReplicas = %d, want 10", fake.Policy().MaxReplicas)
	}
}

func TestHandlePutPolicyConcurrentRequests(t *testing.T) {
	const n = 20

	fake := newFakeGroupControl()
	fake.policy.MaxReplicas = n // espaço suficiente pra qualquer min_replicas concorrente abaixo ser válido
	h, _ := newTestHandler(fake)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(minReplicas int) {
			defer wg.Done()
			body := strings.NewReader(`{"min_replicas":` + strconv.Itoa(minReplicas) + `}`)
			req, _ := http.NewRequest("PUT", srv.URL+"/v1/policy", body)
			req.Header.Set("Authorization", "Bearer infra-admin-token")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("pedido concorrente falhou: %v", err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()

	final := fake.Policy()
	if final.MinReplicas < 1 || final.MinReplicas > n {
		t.Fatalf("estado final inconsistente após pedidos concorrentes: min_replicas=%d, esperado um dos valores tentados (1..%d)", final.MinReplicas, n)
	}
}

func TestHandleRestartSuccess(t *testing.T) {
	fake := newFakeGroupControl()
	h, _ := newTestHandler(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := doRequest(t, mux, "POST", "/v1/restart", "infra-admin-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if fake.restarts != 1 {
		t.Fatalf("restarts = %d, want 1", fake.restarts)
	}
}

func TestHandleRestartReturnsGroupControlError(t *testing.T) {
	fake := newFakeGroupControl()
	fake.restartErr = errors.New("docker inalcançável")
	h, _ := newTestHandler(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := doRequest(t, mux, "POST", "/v1/restart", "infra-admin-token", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandleStatusShapeAndFields(t *testing.T) {
	fake := newFakeGroupControl()
	fake.status = Status{
		TargetService: "auth",
		ReplicaCount:  2,
		Replicas: []Replica{
			{ContainerID: "abc123", Name: "authd-1", State: "running", Status: "Up 5 minutes", CPUPercent: 12.5},
		},
		Policy:         PolicyToDTO(fake.policy),
		LaunchTemplate: LaunchTemplateInfo{Path: "/etc/autoscaler/launch-template.auth.json"},
	}
	h, _ := newTestHandler(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := doRequest(t, mux, "GET", "/v1/status", "infra-admin-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got struct {
		TargetService string `json:"target_service"`
		ReplicaCount  int    `json:"replica_count"`
		Replicas      []struct {
			ContainerID string  `json:"container_id"`
			Name        string  `json:"name"`
			State       string  `json:"state"`
			Status      string  `json:"status"`
			CPUPercent  float64 `json:"cpu_percent"`
		} `json:"replicas"`
		Policy struct {
			MinReplicas *int `json:"min_replicas"`
			MaxReplicas *int `json:"max_replicas"`
		} `json:"policy"`
		LaunchTemplate struct {
			Path string `json:"path"`
		} `json:"launch_template"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("resposta não bate com o envelope esperado: %v", err)
	}
	if got.TargetService != "auth" || got.ReplicaCount != 2 || len(got.Replicas) != 1 {
		t.Fatalf("envelope inesperado: %+v", got)
	}
	if got.Policy.MinReplicas == nil || got.Policy.MaxReplicas == nil {
		t.Fatalf("policy sem min/max_replicas: %+v", got.Policy)
	}
	if got.LaunchTemplate.Path == "" {
		t.Fatalf("launch_template.path vazio")
	}
}
