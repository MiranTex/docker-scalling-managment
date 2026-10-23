package httpapi

import (
	"context"
	"net/http"
	"testing"

	"launcher/internal/dockerclient"
)

func TestValidateSpec(t *testing.T) {
	docker := newFakeDocker()
	docker.volumes = map[string]dockerclient.Volume{
		"demo-database-data": {Name: "demo-database-data", Labels: map[string]string{composeProjectLabel: "demo"}},
		"app-uploads":        {Name: "app-uploads"},
	}
	h := newTestHandler(docker, RoleInfraAdmin)
	h.policy = SpecPolicy{AllowedBindPaths: []string{"/srv/apps/"}}

	cases := []struct {
		name    string
		binds   []string
		network string
		ok      bool
	}{
		{"sem binds", nil, "auth-net", true},
		{"volume anónimo", []string{"/data"}, "", true},
		{"volume nomeado novo", []string{"auth-signing-key:/var/lib/auth"}, "", true},
		{"volume nomeado sem compose", []string{"app-uploads:/uploads:ro"}, "", true},
		{"volume de stack compose", []string{"demo-database-data:/x"}, "", false},
		{"host path permitido", []string{"/srv/apps/loja:/var/www/html"}, "", true},
		{"host path fora da allowlist", []string{"/home/dev/app:/app"}, "", false},
		{"prefixo sem fronteira", []string{"/srv/apps2/x:/app"}, "", false},
		{"fuga com ..", []string{"/srv/apps/../../etc:/x"}, "", false},
		{"raiz do host", []string{"/:/host"}, "", false},
		{"socket do docker", []string{"/var/run/docker.sock:/var/run/docker.sock"}, "", false},
		{"socket fora de /var/run", []string{"/srv/apps/docker.sock:/s"}, "", false},
		{"dentro da allowlist mas proibido", []string{"/proc:/p"}, "", false},
		{"caminho relativo", []string{"./src:/app"}, "", false},
		{"rede do host", nil, "host", false},
		{"rede de outro container", nil, "container:abc", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := h.validateSpec(context.Background(), c.binds, c.network)
			if (err == nil) != c.ok {
				t.Errorf("validateSpec(%v, %q) = %v, queria ok=%v", c.binds, c.network, err, c.ok)
			}
		})
	}
}

// Um token de group (role "service") não pode montar o socket só porque
// o pedido vem de "dentro" da plataforma.
func TestCreateReplicaRejectsDockerSocket(t *testing.T) {
	docker := newFakeDocker()
	h := newTestHandler(docker, RoleService)

	rec := do(t, h, "POST", "/v1/replicas", map[string]any{
		"image": "app:1",
		"binds": []string{"/var/run/docker.sock:/var/run/docker.sock"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, queria 422; body = %s", rec.Code, rec.Body)
	}
	if len(docker.created) != 0 {
		t.Error("não devia ter criado container nenhum")
	}
}

func TestCreateReplicaSetsNoNewPrivileges(t *testing.T) {
	docker := newFakeDocker()
	h := newTestHandler(docker, RoleService)

	if rec := do(t, h, "POST", "/v1/replicas", map[string]any{"image": "app:1"}); rec.Code >= 300 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	opts := docker.created[0].HostConfig.SecurityOpt
	if len(opts) != 1 || opts[0] != "no-new-privileges:true" {
		t.Errorf("SecurityOpt = %v", opts)
	}
}
