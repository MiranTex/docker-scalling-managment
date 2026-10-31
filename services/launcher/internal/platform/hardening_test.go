package platform

import (
	"context"
	"slices"
	"testing"

	"launcher/internal/dockerclient"
)

// Réplicas, addons e jobs arrancam endurecidos: sem novas permissões, só
// as capabilities de que os entrypoints precisam, e com limite de
// processos.
func TestAppContainersAreHardened(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, err := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	waitDeploy(t, svc, "web", dep.ID)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	job, err := svc.BackupAddon(ctx, "loja", "prod", "db", "t")
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, svc, job.ID)

	kinds := map[string]bool{}
	for name, req := range docker.created {
		role := req.Labels[LabelRole]
		if role != RoleApp && role != RoleAddon && role != RoleJob {
			continue
		}
		kinds[role] = true
		h := req.HostConfig
		if !slices.Contains(h.SecurityOpt, "no-new-privileges:true") || !slices.Equal(h.CapDrop, []string{"ALL"}) ||
			slices.Contains(h.CapAdd, "NET_RAW") || !slices.Contains(h.CapAdd, "SETUID") ||
			h.PidsLimit == nil || *h.PidsLimit != dockerclient.AppPidsLimit {
			t.Errorf("%s (%s) não está endurecido: %+v", name, role, h)
		}
	}
	for _, role := range []string{RoleApp, RoleAddon, RoleJob} {
		if !kinds[role] {
			t.Errorf("nenhum container %s criado no teste", role)
		}
	}
}
