package platformapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"testing"

	"launcher/internal/apikeyclient"
	"launcher/internal/platform"
	"launcher/internal/secretbox"
	"launcher/internal/store/storetest"
)

type fakeAPIKeys map[string]apikeyclient.Result

func (f fakeAPIKeys) Introspect(_ context.Context, key string) (apikeyclient.Result, error) {
	return f[key], nil
}

func TestPersonalAPIKeysOnPlatformAPI(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(base64.StdEncoding.EncodeToString(key))
	svc := platform.New(storetest.New(t), netDocker{}, box, platform.Config{}, nil)
	keys := fakeAPIKeys{
		"ak_ok":        {Active: true, Owner: "u1", Scopes: []string{"platform"}, Role: "infra-admin"},
		"ak_sem_scope": {Active: true, Owner: "u1", Scopes: []string{"read"}, Role: "super-admin"},
		"ak_user":      {Active: true, Owner: "u2", Scopes: []string{"platform"}, Role: "user"},
		"ak_revogada":  {Active: false},
	}
	mux := http.NewServeMux()
	New(svc, adminTokens{}, keys, nil).Register(mux)

	cases := []struct {
		token, slug string
		want        int
	}{
		{"ak_ok", "p-ok", http.StatusCreated},
		{"ak_sem_scope", "p-scope", http.StatusForbidden},
		{"ak_user", "p-user", http.StatusForbidden},
		{"ak_revogada", "p-rev", http.StatusUnauthorized},
	}
	for _, c := range cases {
		token, want := c.token, c.want
		rec := call(t, mux, "POST", "/v1/projects", token, map[string]string{"slug": c.slug})
		if rec.Code != want {
			t.Errorf("%s: POST /v1/projects = %d, queria %d (%s)", token, rec.Code, want, rec.Body)
		}
	}
	// O que a key cria fica em nome do dono, como com um access token.
	p, err := svc.GetProject(context.Background(), "p-ok")
	if err != nil || p.CreatedBy != "u1" {
		t.Errorf("projeto criado pela key = %+v, %v", p, err)
	}

	// Sem cliente de API keys, um ak_ é simplesmente recusado.
	mux = http.NewServeMux()
	New(svc, adminTokens{}, nil, nil).Register(mux)
	if rec := call(t, mux, "GET", "/v1/projects", "ak_ok", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("sem cliente de API keys = %d", rec.Code)
	}
}
