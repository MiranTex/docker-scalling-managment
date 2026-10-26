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

// Uma imagem de outro projeto que já esteja no Docker local (o fake diz
// que todas estão) não pode ser usada: a verificação vem antes da cópia
// local, no pedido de deploy e em qualquer ensureImage.
func TestPlatformImageOfAnotherProjectRefusedEvenIfLocal(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	svc.cfg.RegistryHost = "registry.example.test"
	svc.cfg.RegistryPullPassword = "segredo"
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "registry.example.test/basestack/launcher:dev"}, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("deploy com imagem de outro projeto: err = %v, queria ErrInvalid", err)
	}
	p, _, _ := svc.Scope(ctx, "loja", "prod")
	if err := svc.ensureImage(ctx, "registry.example.test/outro/app:1", p.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("ensureImage com imagem local de outro projeto: err = %v", err)
	}
	if err := svc.ensureImage(ctx, "registry.example.test/loja/app:1", p.ID); err != nil {
		t.Errorf("ensureImage do próprio projeto: %v", err)
	}
}
