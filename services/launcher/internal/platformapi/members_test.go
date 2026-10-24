package platformapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"launcher/internal/authusers"
	"launcher/internal/jwtverify"
	"launcher/internal/platform"
	"launcher/internal/secretbox"
	"launcher/internal/store/storetest"
)

// accounts mapeia o "access token" de cada conta de teste nas suas claims.
var accounts = map[string]jwtverify.Claims{
	"t-platform": {Subject: "u-platform", Role: "infra-admin"},
	"t-padmin":   {Subject: "u-padmin", Role: "user"},
	"t-dev":      {Subject: "u-dev", Role: "user"},
	"t-viewer":   {Subject: "u-viewer", Role: "user"},
	"t-outsider": {Subject: "u-outsider", Role: "user"},
}

type accountTokens struct{}

func (accountTokens) Verify(_ context.Context, token string) (jwtverify.Claims, error) {
	if c, ok := accounts[token]; ok {
		return c, nil
	}
	return jwtverify.Claims{}, errors.New("token inválido")
}

type fakeUsers map[string]string // email -> id

func (f fakeUsers) Me(context.Context, string) (string, string, string, error) {
	return "", "", "", errors.New("não usado nestes testes")
}

func (f fakeUsers) Resolve(_ context.Context, bearer, email string) (string, string, error) {
	if bearer == "" {
		return "", "", errors.New("sem bearer")
	}
	if id, ok := f[email]; ok {
		return id, email, nil
	}
	return "", "", authusers.ErrNoAccount
}

func TestProjectRoleMatrix(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(base64.StdEncoding.EncodeToString(key))
	svc := platform.New(storetest.New(t), netDocker{}, box, platform.Config{}, nil)
	users := fakeUsers{"padmin@x.pt": "u-padmin", "dev@x.pt": "u-dev", "viewer@x.pt": "u-viewer"}
	mux := http.NewServeMux()
	New(svc, accountTokens{}, nil, users, nil).Register(mux)

	must := func(token, method, path string, body any, want int) {
		t.Helper()
		if rec := call(t, mux, method, path, token, body); rec.Code != want {
			t.Errorf("%s %s %s = %d, queria %d (%s)", token, method, path, rec.Code, want, rec.Body)
		}
	}

	// Montagem: o admin da plataforma cria o projeto e o primeiro admin.
	must("t-platform", "POST", "/v1/projects", map[string]string{"slug": "loja"}, 201)
	must("t-padmin", "POST", "/v1/projects", map[string]string{"slug": "outro"}, 403)
	must("t-platform", "POST", "/v1/projects/loja/members", map[string]string{"email": "padmin@x.pt", "role": "admin"}, 201)
	// O admin do projeto trata do resto da equipa e dos ambientes.
	must("t-padmin", "POST", "/v1/projects/loja/members", map[string]string{"email": "dev@x.pt", "role": "developer"}, 201)
	must("t-padmin", "POST", "/v1/projects/loja/members", map[string]string{"email": "viewer@x.pt", "role": "viewer"}, 201)
	must("t-padmin", "POST", "/v1/projects/loja/members", map[string]string{"email": "nao-existe@x.pt", "role": "viewer"}, 404)
	must("t-padmin", "POST", "/v1/projects/loja/environments", map[string]string{"slug": "prod"}, 201)

	// Quem não é membro não vê nada -- nem sabe que o projeto existe.
	must("t-outsider", "GET", "/v1/projects/loja", nil, 404)
	must("t-outsider", "GET", "/v1/projects/loja/environments/prod/services", nil, 404)
	rec := call(t, mux, "GET", "/v1/projects", "t-outsider", nil)
	if rec.Body.String() != "[]\n" {
		t.Errorf("lista do outsider = %s", rec.Body)
	}
	rec = call(t, mux, "GET", "/v1/projects", "t-viewer", nil)
	var list []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["slug"] != "loja" {
		t.Errorf("lista do viewer = %s", rec.Body)
	}

	// viewer: só ler; nem comandos (correm com os segredos).
	must("t-viewer", "GET", "/v1/projects/loja/environments/prod/services", nil, 200)
	must("t-viewer", "GET", "/v1/projects/loja/members", nil, 200)
	must("t-viewer", "POST", "/v1/projects/loja/environments/prod/services", map[string]string{"slug": "web", "kind": "worker"}, 403)
	must("t-viewer", "PUT", "/v1/projects/loja/environments/prod/secrets/X", map[string]string{"value": "1"}, 403)

	// developer: trabalhar nos serviços, mas não na equipa nem nos ambientes.
	must("t-dev", "POST", "/v1/projects/loja/environments/prod/services", map[string]string{"slug": "web", "kind": "worker"}, 201)
	must("t-dev", "PUT", "/v1/projects/loja/environments/prod/secrets/X", map[string]string{"value": "1"}, 204)
	must("t-viewer", "POST", "/v1/projects/loja/environments/prod/services/web/run", map[string]any{"cmd": []string{"ls"}}, 403)
	must("t-dev", "POST", "/v1/projects/loja/deploy-tokens", map[string]string{"name": "ci"}, 201)
	must("t-dev", "POST", "/v1/projects/loja/environments", map[string]string{"slug": "staging"}, 403)
	must("t-dev", "POST", "/v1/projects/loja/members", map[string]string{"email": "viewer@x.pt", "role": "admin"}, 403)
	must("t-dev", "DELETE", "/v1/projects/loja/environments/prod", nil, 403)

	// admin do projeto: equipa e ambientes, mas não quotas nem apagar o
	// projeto, nem as páginas globais.
	must("t-padmin", "PATCH", "/v1/projects/loja/members/u-viewer", map[string]string{"role": "developer"}, 200)
	must("t-padmin", "PATCH", "/v1/projects/loja", map[string]any{"quotaMemoryMb": 99999}, 403)
	must("t-padmin", "DELETE", "/v1/projects/loja", nil, 403)
	must("t-padmin", "GET", "/v1/volumes", nil, 403)
	must("t-padmin", "DELETE", "/v1/projects/loja/members/u-viewer", nil, 204)
	must("t-viewer", "GET", "/v1/projects/loja", nil, 404) // já não é membro

	// O papel de quem pede vem no detalhe do projeto, para a UI.
	for token, want := range map[string]string{"t-platform": "platform-admin", "t-padmin": "admin", "t-dev": "developer"} {
		var d struct{ MyRole string }
		_ = json.Unmarshal(call(t, mux, "GET", "/v1/projects/loja", token, nil).Body.Bytes(), &d)
		if d.MyRole != want {
			t.Errorf("myRole de %s = %q, queria %q", token, d.MyRole, want)
		}
	}
	must("t-platform", "GET", "/v1/volumes", nil, 200)
}
