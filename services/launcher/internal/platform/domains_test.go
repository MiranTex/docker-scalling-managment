package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"launcher/internal/store"
)

func TestCustomDomainOnlyRoutedAfterDNS(t *testing.T) {
	ctx := context.Background()
	svc, _, _, dir := newDeployService(t)
	svc.cfg.PublicIPs = []string{"203.0.113.10"}
	dns := map[string][]string{}
	svc.lookupHost = func(_ context.Context, host string) ([]string, error) {
		if ips, ok := dns[host]; ok {
			return ips, nil
		}
		return nil, errors.New("no such host")
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "jobs", Kind: "worker"}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)

	d, err := svc.AddDomain(ctx, "loja", "prod", "web", "Loja.PT.", "t")
	if err != nil {
		t.Fatal(err)
	}
	if d.Hostname != "loja.pt" || d.Status != store.DomainFailed || d.ExpectedCNAME != "web-prod-loja.example.test" {
		t.Errorf("domínio sem DNS = %+v", d)
	}
	routing := func() string {
		b, _ := os.ReadFile(filepath.Join(dir, routingFile))
		return string(b)
	}
	if strings.Contains(routing(), "loja.pt") {
		t.Error("domínio sem DNS não pode entrar no routing")
	}

	dns["loja.pt"] = []string{"198.51.100.1"}
	if d, _ = svc.VerifyDomain(ctx, "loja", "prod", "web", "loja.pt"); d.Status != store.DomainFailed || !strings.Contains(d.Error, "198.51.100.1") {
		t.Errorf("DNS para outro IP = %+v", d)
	}

	dns["loja.pt"] = []string{"203.0.113.10"}
	if d, _ = svc.VerifyDomain(ctx, "loja", "prod", "web", "loja.pt"); d.Status != store.DomainActive {
		t.Fatalf("DNS certo = %+v", d)
	}
	if !strings.Contains(routing(), "Host(`loja.pt`)") {
		t.Errorf("domínio ativo devia estar no routing:\n%s", routing())
	}

	// Regras de validação.
	cases := map[string]error{
		"loja.pt":        ErrConflict, // já existe
		"x.example.test": ErrInvalid,  // dentro do domínio da plataforma
		"http://a.pt":    ErrInvalid,
		"*.loja.pt":      ErrInvalid,
		"semponto":       ErrInvalid,
	}
	for host, want := range cases {
		if _, err := svc.AddDomain(ctx, "loja", "prod", "web", host, "t"); !errors.Is(err, want) {
			t.Errorf("AddDomain(%q) = %v, queria %v", host, err, want)
		}
	}
	if _, err := svc.AddDomain(ctx, "loja", "prod", "jobs", "jobs.pt", "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("domínio num worker: err = %v", err)
	}

	if err := svc.DeleteDomain(ctx, "loja", "prod", "web", "loja.pt"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(routing(), "loja.pt") {
		t.Error("domínio apagado ficou no routing")
	}
}

func TestDomainActiveWithoutPublicIPs(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	d, err := svc.AddDomain(ctx, "loja", "prod", "web", "dev.loja.pt", "t")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != store.DomainActive || !strings.Contains(d.Error, "LAUNCHER_PUBLIC_IPS") {
		t.Errorf("sem IPs configurados = %+v", d)
	}
}

func TestRoutingWithTLSRedirects(t *testing.T) {
	ctx := context.Background()
	svc, _, _, dir := newDeployService(t)
	svc.cfg.TLSCertResolver = "le"
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)
	b, _ := os.ReadFile(filepath.Join(dir, routingFile))
	got := string(b)
	for _, want := range []string{`"certResolver": "le"`, `"websecure"`, `"bs-redirect-https"`, `"scheme": "https"`} {
		if !strings.Contains(got, want) {
			t.Errorf("routing com TLS não tem %s:\n%s", want, got)
		}
	}
	view, _ := svc.GetService(ctx, "loja", "prod", "web")
	if len(view.URLs) != 1 || !strings.HasPrefix(view.URLs[0], "https://") {
		t.Errorf("URLs = %v", view.URLs)
	}
}
