package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"launcher/internal/dockerclient"
	"launcher/internal/platform"
)

// Uma rede e um container de projeto, ao lado de uma rede e um container do
// fluxo antigo -- a API antiga só pode ver e mexer nos segundos.
func isolationFixture(t *testing.T) (*Handler, *fakeDocker) {
	t.Helper()
	managed := map[string]string{dockerclient.PlatformLabel: "true"}
	project := map[string]string{dockerclient.PlatformLabel: "true", platform.LabelProjectID: "p1"}
	docker := newFakeDocker(
		dockerclient.Container{ID: "legacy-app", Labels: managed},
		dockerclient.Container{ID: "project-app", Labels: project},
	)
	docker.networks = map[string]map[string]string{
		"auth-net":          managed,
		"observability-net": managed,
		"bs-loja-prod":      project,
	}
	return newTestHandler(docker, RoleInfraAdmin), docker
}

func TestLegacyAPIHidesProjectNetworksAndContainers(t *testing.T) {
	h, _ := isolationFixture(t)

	rec := do(t, h, "GET", "/v1/networks", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "bs-loja-prod") {
		t.Errorf("GET /v1/networks = %d %s", rec.Code, rec.Body)
	}
	// A observabilidade não é rede de projeto: tem de continuar visível e
	// utilizável pelo fluxo antigo.
	if !strings.Contains(rec.Body.String(), "observability-net") || !strings.Contains(rec.Body.String(), "auth-net") {
		t.Errorf("redes da plataforma desapareceram: %s", rec.Body)
	}

	rec = do(t, h, "GET", "/v1/containers", nil)
	if strings.Contains(rec.Body.String(), "project-app") || !strings.Contains(rec.Body.String(), "legacy-app") {
		t.Errorf("GET /v1/containers = %s", rec.Body)
	}
}

func TestLegacyAPICannotTouchProjectResources(t *testing.T) {
	h, docker := isolationFixture(t)

	cases := []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/v1/networks/bs-loja-prod", nil, http.StatusNotFound},
		{"DELETE", "/v1/networks/bs-loja-prod", nil, http.StatusNotFound},
		{"POST", "/v1/networks/bs-loja-prod/connect", map[string]string{"containerId": "legacy-app"}, http.StatusNotFound},
		{"POST", "/v1/networks/bs-loja-prod/disconnect", map[string]string{"containerId": "legacy-app"}, http.StatusNotFound},
		// Ligar um container de projeto a uma rede antiga também não.
		{"POST", "/v1/networks/auth-net/connect", map[string]string{"containerId": "project-app"}, http.StatusForbidden},
		{"POST", "/v1/containers/project-app/stop", nil, http.StatusNotFound},
		{"DELETE", "/v1/containers/project-app", nil, http.StatusNotFound},
	}
	for _, c := range cases {
		if rec := do(t, h, c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s %s = %d, queria %d (%s)", c.method, c.path, rec.Code, c.want, rec.Body)
		}
	}
	for _, call := range docker.calls {
		if strings.Contains(call, "bs-loja-prod") || strings.Contains(call, "project-app") {
			t.Errorf("chamada ao Docker sobre recurso de projeto: %s", call)
		}
	}

	// O fluxo antigo continua a funcionar nas suas redes.
	if rec := do(t, h, "POST", "/v1/networks/observability-net/connect", map[string]string{"containerId": "legacy-app"}); rec.Code != http.StatusNoContent {
		t.Errorf("ligar à observability-net = %d %s", rec.Code, rec.Body)
	}
}

func TestReplicaCannotLandOnProjectNetwork(t *testing.T) {
	h, docker := isolationFixture(t)
	h.tokens = fakeTokens{role: RoleService}

	rec := do(t, h, "POST", "/v1/replicas", map[string]any{"image": "app:1", "network": "bs-loja-prod"})
	if rec.Code != http.StatusUnprocessableEntity || len(docker.created) != 0 {
		t.Fatalf("réplica na rede de um projeto = %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "POST", "/v1/replicas", map[string]any{"image": "app:1", "network": "auth-net"})
	if rec.Code >= 300 {
		t.Fatalf("réplica numa rede antiga = %d %s", rec.Code, rec.Body)
	}
}
