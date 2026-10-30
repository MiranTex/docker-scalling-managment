package platformapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"launcher/internal/dockerclient"
	"launcher/internal/jwtverify"
	"launcher/internal/platform"
	"launcher/internal/secretbox"
	"launcher/internal/store/storetest"
)

// netDocker só implementa o que criar ambientes precisa; o resto da
// interface fica nil (panic se um teste o usar sem querer).
type netDocker struct{ platform.Docker }

func (netDocker) CreateLabeledNetwork(context.Context, string, map[string]string) (string, error) {
	return "n", nil
}
func (netDocker) CreateNetworkWith(context.Context, string, dockerclient.NetworkOptions) (string, error) {
	return "n", nil
}
func (netDocker) ConnectNetwork(context.Context, string, string) error { return nil }
func (netDocker) ListContainers(context.Context, bool, map[string][]string) ([]dockerclient.Container, error) {
	return nil, nil
}
func (netDocker) VolumesWithUsage(context.Context) ([]dockerclient.Volume, error) { return nil, nil }

type adminTokens struct{}

func (adminTokens) Verify(_ context.Context, token string) (jwtverify.Claims, error) {
	if token == "admin-jwt" {
		return jwtverify.Claims{Subject: "admin", Role: "infra-admin"}, nil
	}
	return jwtverify.Claims{}, errors.New("assinatura inválida")
}

func call(t *testing.T, mux *http.ServeMux, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestDeployTokenScope(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(base64.StdEncoding.EncodeToString(key))
	svc := platform.New(storetest.New(t), netDocker{}, box, platform.Config{}, nil)
	mux := http.NewServeMux()
	New(svc, adminTokens{}, nil, nil, nil).Register(mux)

	for _, p := range []string{"loja", "blog"} {
		if _, err := svc.CreateProject(ctx, platform.CreateProjectInput{Slug: p}, "t"); err != nil {
			t.Fatal(err)
		}
		for _, e := range []string{"prod", "staging"} {
			if _, err := svc.CreateEnvironment(ctx, p, e, "t"); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.CreateService(ctx, p, e, platform.ServiceInput{Slug: "web", Kind: "worker"}, "t"); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Token só para loja/prod, criado por um admin pela API.
	rec := call(t, mux, "POST", "/v1/projects/loja/deploy-tokens", "admin-jwt", map[string]string{"name": "ci", "environment": "prod"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar token: %d %s", rec.Code, rec.Body)
	}
	var created platform.NewDeployToken
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	token := created.Token

	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/v1/projects/loja/environments/prod/services/web", http.StatusOK},
		{"GET", "/v1/projects/loja/environments/prod/services/web/deployments", http.StatusOK},
		{"GET", "/v1/projects/loja/environments/staging/services/web", http.StatusForbidden},
		{"GET", "/v1/projects/blog/environments/prod/services/web", http.StatusForbidden},
		{"GET", "/v1/projects/nao-existe/environments/prod/services/web", http.StatusForbidden},
		// Rotas só de admin nunca aceitam um token de deploy.
		{"GET", "/v1/projects/loja/environments/prod/secrets", http.StatusUnauthorized},
		{"PATCH", "/v1/projects/loja/environments/prod/services/web", http.StatusUnauthorized},
		{"POST", "/v1/projects/loja/deploy-tokens", http.StatusUnauthorized},
	}
	for _, c := range cases {
		if rec := call(t, mux, c.method, c.path, token, map[string]string{}); rec.Code != c.want {
			t.Errorf("%s %s = %d, queria %d (%s)", c.method, c.path, rec.Code, c.want, rec.Body)
		}
	}

	if rec := call(t, mux, "GET", "/v1/projects/loja/environments/prod/services/web", "bsd_inventado", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("token inventado = %d", rec.Code)
	}

	// Revogado deixa de funcionar.
	if rec := call(t, mux, "DELETE", "/v1/projects/loja/deploy-tokens/"+created.ID, "admin-jwt", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revogar: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, mux, "GET", "/v1/projects/loja/environments/prod/services/web", token, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("token revogado = %d", rec.Code)
	}

	// A lista nunca devolve o token nem o hash.
	rec = call(t, mux, "GET", "/v1/projects/loja/deploy-tokens", "admin-jwt", nil)
	if bytes.Contains(rec.Body.Bytes(), []byte(token)) || bytes.Contains(rec.Body.Bytes(), []byte("hash")) {
		t.Errorf("lista expõe segredo: %s", rec.Body)
	}
}
