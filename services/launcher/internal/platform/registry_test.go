package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRoutingCarriesExtraCertificates(t *testing.T) {
	ctx := context.Background()
	svc, _, _, dir := newDeployService(t)
	svc.cfg.TraefikCertificates = []TLSCertificate{{CertFile: "/etc/traefik/tls/registry.crt", KeyFile: "/etc/traefik/tls/registry.key"}}
	svc.syncRouting(ctx)
	b, err := os.ReadFile(filepath.Join(dir, routingFile))
	if err != nil {
		t.Fatal(err)
	}
	var cfg traefikConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP != nil || cfg.TLS == nil || len(cfg.TLS.Certificates) != 1 || cfg.TLS.Certificates[0].KeyFile != "/etc/traefik/tls/registry.key" {
		t.Errorf("routing = %s", b)
	}
}

func TestPlatformRegistryPullsOnlyOwnProject(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.RegistryHost = "registry.example.test"
	svc.cfg.RegistryPullPassword = "segredo"
	setupEnv(t, svc)
	p, _, _ := svc.Scope(ctx, "loja", "prod")

	if err := svc.pullImage(ctx, "registry.example.test/loja/app:1", p.ID); err != nil {
		t.Fatal(err)
	}
	if len(docker.pulls) != 1 || docker.pulls[0] != "registry.example.test/loja/app:1 "+RegistryInternalUser+"@registry.example.test" {
		t.Errorf("pulls = %v", docker.pulls)
	}
	if err := svc.pullImage(ctx, "registry.example.test/outro/app:1", p.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("imagem de outro projeto: err = %v, queria ErrInvalid", err)
	}
	if got := RegistryProject("registry.example.test/loja/app:1"); got != "loja" {
		t.Errorf("RegistryProject = %q", got)
	}
}
