package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"launcher/internal/dockerclient"
	"launcher/internal/jwtverify"
	"launcher/internal/store"
)

// fakeDocker guarda containers em memória e regista a ordem das chamadas
// que alteram estado. Embute a interface só para satisfazer os métodos de
// rede, que estes testes nunca chamam (chamá-los daria panic, de propósito).
type fakeDocker struct {
	Docker

	mu         sync.Mutex
	containers map[string]dockerclient.Container
	calls      []string
	created    []dockerclient.CreateContainerRequest
	volumes    map[string]dockerclient.Volume
}

func (f *fakeDocker) FindVolume(_ context.Context, name string) (*dockerclient.Volume, error) {
	if v, ok := f.volumes[name]; ok {
		return &v, nil
	}
	return nil, nil
}

func newFakeDocker(cs ...dockerclient.Container) *fakeDocker {
	f := &fakeDocker{containers: map[string]dockerclient.Container{}}
	for _, c := range cs {
		f.containers[c.ID] = c
	}
	return f
}

func (f *fakeDocker) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeDocker) InspectImage(context.Context, string) (*dockerclient.ImageInspect, error) {
	return &dockerclient.ImageInspect{ID: "sha256:x"}, nil
}

func (f *fakeDocker) CreateContainer(_ context.Context, _ string, req dockerclient.CreateContainerRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, req)
	return "new-container", nil
}

func (f *fakeDocker) StartContainer(_ context.Context, id string) error {
	f.record("start " + id)
	return nil
}

func (f *fakeDocker) StopContainer(_ context.Context, id string, _ int) error {
	f.record("stop " + id)
	return nil
}

func (f *fakeDocker) RemoveContainer(_ context.Context, id string, _ bool) error {
	f.record("remove " + id)
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.containers, id)
	return nil
}

// ListContainers entende os dois filtros que este pacote usa: "id" e
// "label" (no formato "chave" ou "chave=valor").
func (f *fakeDocker) ListContainers(_ context.Context, _ bool, filters map[string][]string) ([]dockerclient.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []dockerclient.Container
	for _, c := range f.containers {
		if ids := filters["id"]; len(ids) > 0 && ids[0] != c.ID {
			continue
		}
		match := true
		for _, l := range filters["label"] {
			k, v, hasValue := strings.Cut(l, "=")
			got, ok := c.Labels[k]
			if !ok || (hasValue && got != v) {
				match = false
				break
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out, nil
}

type fakeStore struct{ Store }

func (fakeStore) List(context.Context) ([]store.Instance, error) { return nil, nil }

type fakeSecrets struct{}

func (fakeSecrets) ResolveEnv(_ context.Context, env []string) ([]string, error) { return env, nil }

type fakeTokens struct{ role string }

func (t fakeTokens) Verify(context.Context, string) (jwtverify.Claims, error) {
	return jwtverify.Claims{Subject: "tester", Role: t.role}, nil
}

func newTestHandler(docker *fakeDocker, role string) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(fakeTokens{role: role}, fakeStore{}, docker, fakeSecrets{}, nil, GroupConfig{}, "", "", CapacityConfig{}, SpecPolicy{}, logger)
}

func do(t *testing.T, h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	return rec
}

// Apagar um group é agora o único sítio onde as réplicas dele vão abaixo
// (o SIGTERM do group já não as remove) -- e só as DELE.
func TestDeleteGroupRemovesOnlyItsReplicas(t *testing.T) {
	docker := newFakeDocker(
		dockerclient.Container{ID: "group-web", Labels: map[string]string{
			labelGroupRole: labelGroupRoleValue, labelGroupTargetService: "web",
		}},
		dockerclient.Container{ID: "web-1", Labels: map[string]string{labelServiceKey: "web"}},
		dockerclient.Container{ID: "web-2", Labels: map[string]string{labelServiceKey: "web"}},
		dockerclient.Container{ID: "api-1", Labels: map[string]string{labelServiceKey: "api"}},
	)
	h := newTestHandler(docker, RoleInfraAdmin)

	rec := do(t, h, "DELETE", "/v1/groups/group-web", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}

	for _, id := range []string{"group-web", "web-1", "web-2"} {
		if _, ok := docker.containers[id]; ok {
			t.Errorf("%s devia ter sido removido", id)
		}
	}
	if _, ok := docker.containers["api-1"]; !ok {
		t.Error("api-1 pertence a outro group e não devia ter sido tocado")
	}

	// O group tem de parar ANTES de qualquer réplica ser removida, senão o
	// reconcile dele repõe o MinReplicas a meio da limpeza.
	if docker.calls[0] != "stop group-web" {
		t.Errorf("primeira chamada = %q, queria stop do group", docker.calls[0])
	}
	if last := docker.calls[len(docker.calls)-1]; last != "remove group-web" {
		t.Errorf("última chamada = %q, queria remove do group", last)
	}
}

func TestDeleteGroupWithoutTargetServiceLabelFails(t *testing.T) {
	docker := newFakeDocker(
		dockerclient.Container{ID: "group-x", Labels: map[string]string{labelGroupRole: labelGroupRoleValue}},
		dockerclient.Container{ID: "other", Labels: map[string]string{labelServiceKey: "whatever"}},
	)
	h := newTestHandler(docker, RoleInfraAdmin)

	rec := do(t, h, "DELETE", "/v1/groups/group-x", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, queria 409", rec.Code)
	}
	if len(docker.calls) != 0 {
		t.Errorf("chamadas = %v, não devia ter parado nem removido nada", docker.calls)
	}
	if _, ok := docker.containers["other"]; !ok {
		t.Error("sem label de target service não pode remover nada à sorte")
	}
}

func TestCreateReplicaSetsRestartPolicy(t *testing.T) {
	docker := newFakeDocker()
	h := newTestHandler(docker, RoleService)

	rec := do(t, h, "POST", "/v1/replicas", map[string]any{"image": "app:1"})
	if rec.Code >= 300 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if len(docker.created) != 1 {
		t.Fatalf("containers criados = %d, queria 1", len(docker.created))
	}
	hc := docker.created[0].HostConfig
	if hc == nil || hc.RestartPolicy == nil || hc.RestartPolicy.Name != "unless-stopped" {
		t.Errorf("RestartPolicy = %+v, queria unless-stopped", hc)
	}
}
