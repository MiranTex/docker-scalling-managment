package platform

import (
	"context"
	"errors"
	"testing"

	"launcher/internal/dockerclient"
)

func volumesByName(t *testing.T, svc *Service) map[string]VolumeView {
	t.Helper()
	list, err := svc.ListVolumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]VolumeView{}
	for _, v := range list {
		out[v.Name] = v
	}
	return out
}

// mount simula um container a montar um volume (o fake não preenche Mounts).
func mount(d *fakeDocker, containerID, volume, path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.containers[containerID]
	c.Mounts = append(c.Mounts, struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
	}{"volume", volume, path})
	d.containers[containerID] = c
}

func TestVolumeRules(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	setupEnv(t, svc)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	mount(docker, "cid-bs-loja-prod-db", "bs-loja-prod-db-data", "/var/lib/postgresql/data")
	docker.mu.Lock()
	// Volume da plataforma e volume de outra app do host: nunca aparecem.
	docker.volumes["demo_demo-database-data-pg17"] = map[string]string{dockerclient.PlatformLabel: "true", "com.docker.compose.project": "demo"}
	docker.volumes["outra-app_data"] = map[string]string{"com.docker.compose.project": "outra-app"}
	docker.mu.Unlock()

	vols := volumesByName(t, svc)
	if len(vols) != 2 {
		t.Fatalf("só os 2 volumes do projeto deviam aparecer: %v", vols)
	}
	data := vols["bs-loja-prod-db-data"]
	if data.Status != VolumeInUse || data.Deletable || data.Addon != "db" || data.Kind != "data" || data.Project != "loja" || data.Environment != "prod" || data.SizeBytes != 1<<20 || len(data.UsedBy) != 1 {
		t.Errorf("dados em uso = %+v", data)
	}
	// Backups de um addon que existe, sem ninguém a montá-los: apagáveis.
	if b := vols["bs-loja-prod-db-backups"]; b.Status != VolumeFree || !b.Deletable || b.Kind != "backups" {
		t.Errorf("backups livres = %+v", b)
	}
	if err := svc.DeleteVolume(ctx, "bs-loja-prod-db-data"); !errors.Is(err, ErrInvalid) {
		t.Errorf("apagar volume em uso: %v", err)
	}

	// O container do addon desaparece (ex: a ser recriado): os dados ficam
	// livres, mas continuam protegidos enquanto o addon existir.
	if err := docker.RemoveContainer(ctx, "bs-loja-prod-db", true); err != nil {
		t.Fatal(err)
	}
	data = volumesByName(t, svc)["bs-loja-prod-db-data"]
	if data.Status != VolumeFree || data.Deletable || data.Reason == "" {
		t.Errorf("dados de addon que existe, sem container = %+v", data)
	}
	if err := svc.DeleteVolume(ctx, "bs-loja-prod-db-data"); !errors.Is(err, ErrInvalid) {
		t.Errorf("apagar dados de addon que existe: %v", err)
	}

	// Apagar o addon sem "apagar dados": os volumes ficam órfãos e passam a
	// poder ser apagados (é o "apagar órfãos" da UI, um a um).
	if err := svc.DeleteAddon(ctx, "loja", "prod", "db", false); err != nil {
		t.Fatal(err)
	}
	vols = volumesByName(t, svc)
	for _, name := range []string{"bs-loja-prod-db-data", "bs-loja-prod-db-backups"} {
		v := vols[name]
		if v.Status != VolumeOrphan || !v.Deletable || v.Addon != "" {
			t.Errorf("%s depois de apagar o addon = %+v", name, v)
		}
		if err := svc.DeleteVolume(ctx, name); err != nil {
			t.Errorf("apagar órfão %s: %v", name, err)
		}
	}
	if len(volumesByName(t, svc)) != 0 {
		t.Error("os órfãos deviam ter sido apagados")
	}
	if err := svc.DeleteVolume(ctx, "outra-app_data"); !errors.Is(err, ErrNotFound) {
		t.Errorf("volume de outra app tem de ser invisível: %v", err)
	}
}

// Um órfão que continua montado (ex: por um container que alguém criou à
// mão) não se apaga -- estar em uso ganha a tudo.
func TestOrphanStillMountedIsProtected(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	setupEnv(t, svc)
	docker.mu.Lock()
	docker.volumes["bs-loja-prod-velho-data"] = map[string]string{LabelProjectID: "p", LabelProject: "loja", LabelEnvironmentID: "env-apagado", LabelAddon: "addon-apagado"}
	docker.containers["cid-x"] = dockerclient.Container{ID: "cid-x", Names: []string{"/x"}, State: "exited"}
	docker.mu.Unlock()
	mount(docker, "cid-x", "bs-loja-prod-velho-data", "/data")

	v := volumesByName(t, svc)["bs-loja-prod-velho-data"]
	if v.Status != VolumeInUse || v.Deletable {
		t.Errorf("órfão montado = %+v", v)
	}
	if err := svc.DeleteVolume(ctx, v.Name); !errors.Is(err, ErrInvalid) {
		t.Errorf("apagar órfão montado: %v", err)
	}
}
