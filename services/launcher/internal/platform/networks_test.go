package platform

import (
	"context"
	"errors"
	"testing"
)

func TestServiceMetricsLabels(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "api", Kind: "web", Port: intp(8080), MetricsPort: intp(9100)}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"api", "web"} {
		dep, _ := svc.Deploy(ctx, "loja", "prod", s, DeployInput{Image: "app:1"}, "t")
		waitDeploy(t, svc, s, dep.ID)
	}
	for _, c := range docker.byRole(RoleApp) {
		l := c.Labels
		switch l[LabelServiceSlug] {
		case "api":
			if l["prometheus.scrape"] != "true" || l["prometheus.port"] != "9100" || l["prometheus.path"] != "/metrics" {
				t.Errorf("labels de métricas do api = %v", l)
			}
		case "web":
			if _, ok := l["prometheus.scrape"]; ok {
				t.Errorf("sem porta de métricas não pode ter labels do Prometheus: %v", l)
			}
		}
	}
	if _, err := svc.UpdateService(ctx, "loja", "prod", "api", ServiceInput{MetricsPath: strp("metrics")}); !errors.Is(err, ErrInvalid) {
		t.Errorf("caminho sem /: err = %v", err)
	}
	if _, err := svc.UpdateService(ctx, "loja", "prod", "api", ServiceInput{MetricsPort: intp(70000)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("porta inválida: err = %v", err)
	}
}

// O Prometheus recriado (compose up do monitoring) perde as redes -- a
// próxima passagem de AttachPlatformToAll volta a ligá-lo.
func TestPlatformReattachedAfterRecreate(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	setupEnv(t, svc)
	if err := docker.DisconnectNetwork(ctx, "bs-loja-prod", "prometheus"); err != nil {
		t.Fatal(err)
	}
	svc.AttachPlatformToAll(ctx)
	found := 0
	for _, c := range docker.connected["bs-loja-prod"] {
		if c == "prometheus" || c == "traefik" || c == "launcher" {
			found++
		}
	}
	if found != 3 {
		t.Errorf("membros da plataforma depois de religar = %v", docker.connected["bs-loja-prod"])
	}
}

func TestEnvironmentNetworkView(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)

	n, err := svc.GetEnvironmentNetwork(ctx, "loja", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "bs-loja-prod" || n.Subnet == "" {
		t.Errorf("rede = %+v", n)
	}
	kinds := map[string][]string{}
	for _, m := range n.Members {
		kinds[m.Kind] = append(kinds[m.Kind], m.Name)
	}
	if len(kinds[NetMemberService]) != 1 || kinds[NetMemberService][0] != "web" {
		t.Errorf("serviços = %v", kinds[NetMemberService])
	}
	if len(kinds[NetMemberAddon]) != 1 || kinds[NetMemberAddon][0] != "db" {
		t.Errorf("addons = %v", kinds[NetMemberAddon])
	}
	if len(kinds[NetMemberPlatform]) != 3 || len(kinds[NetMemberOther]) != 0 {
		t.Errorf("plataforma = %v, outros = %v", kinds[NetMemberPlatform], kinds[NetMemberOther])
	}
	// Ordem: serviços, addons, plataforma.
	if n.Members[0].Kind != NetMemberService || n.Members[len(n.Members)-1].Kind != NetMemberPlatform {
		t.Errorf("ordem = %+v", n.Members)
	}
}
