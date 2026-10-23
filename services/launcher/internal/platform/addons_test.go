package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"launcher/internal/store"
)

func setupEnv(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.CreateProject(ctx, CreateProjectInput{Slug: "loja"}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateEnvironment(ctx, "loja", "prod", "t"); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePostgresAddon(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	setupEnv(t, svc)

	a, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if a.EnvVar != "DATABASE_URL" || a.Host != "db:5432" {
		t.Errorf("addon = %+v", a)
	}

	req, ok := docker.created["bs-loja-prod-db"]
	if !ok {
		t.Fatalf("container não criado; criados: %v", keys(docker.created))
	}
	if req.Image != "postgres:17-alpine" || req.HostConfig.RestartPolicy == nil || req.Healthcheck == nil {
		t.Errorf("pedido de criação = %+v", req)
	}
	if req.HostConfig.NetworkMode != "bs-loja-prod" || req.NetworkingConfig.EndpointsConfig["bs-loja-prod"].Aliases[0] != "db" {
		t.Errorf("rede/alias errados: %+v", req.NetworkingConfig)
	}
	if req.HostConfig.Binds[0] != "bs-loja-prod-db-data:/var/lib/postgresql/data" {
		t.Errorf("binds = %v", req.HostConfig.Binds)
	}
	if _, ok := docker.volumes["bs-loja-prod-db-backups"]; !ok {
		t.Error("volume de backups não criado")
	}

	// A URL vive como segredo gerido, com a mesma password do container.
	_, env, _ := svc.Scope(ctx, "loja", "prod")
	resolved, err := svc.ResolveEnv(ctx, env.ID, []string{"X=${secret:DATABASE_URL}"})
	if err != nil {
		t.Fatal(err)
	}
	var pw string
	for _, e := range req.Env {
		if v, ok := strings.CutPrefix(e, "POSTGRES_PASSWORD="); ok {
			pw = v
		}
	}
	if pw == "" || resolved[0] != "X=postgres://app:"+pw+"@db:5432/app?sslmode=disable" {
		t.Errorf("URL = %q (password do container %q)", resolved[0], pw)
	}

	// Outra base no mesmo ambiente tem de escolher outra variável.
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db2", Kind: "postgres"}, "t"); !errors.Is(err, ErrConflict) {
		t.Errorf("DATABASE_URL repetida: err = %v", err)
	}
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "x", Kind: "mysql"}, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("tipo desconhecido: err = %v", err)
	}
}

func TestRedisAddonAndQuota(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.RedisImage = "redis:7-alpine"
	setupEnv(t, svc)
	mem := int64(300)
	if _, err := svc.UpdateProject(ctx, "loja", UpdateProjectInput{QuotaMemoryMB: &mem}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "cache", Kind: "redis"}, "t"); err != nil {
		t.Fatal(err)
	}
	req := docker.created["bs-loja-prod-cache"]
	if req.Cmd[0] != "redis-server" || !contains(req.Cmd, "noeviction") {
		t.Errorf("cmd = %v", req.Cmd)
	}
	_, env, _ := svc.Scope(ctx, "loja", "prod")
	url, _ := svc.ResolveEnv(ctx, env.ID, []string{"${secret:REDIS_URL}"})
	if !strings.HasPrefix(url[0], "redis://default:") || !strings.HasSuffix(url[0], "@cache:6379/0") {
		t.Errorf("REDIS_URL = %q", url[0])
	}
	// Marca o container como a correr para contar na quota (256 MB).
	if err := docker.StartContainer(ctx, "bs-loja-prod-cache"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "cache2", Kind: "redis", EnvVar: "CACHE2_URL"}, "t"); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("quota: err = %v", err)
	}
}

func TestReconcilerRecreatesMissingAddon(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	setupEnv(t, svc)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	firstEnv := docker.created["bs-loja-prod-db"].Env

	if err := docker.RemoveContainer(ctx, "bs-loja-prod-db", true); err != nil {
		t.Fatal(err)
	}
	svc.reconcileOnce(ctx)

	if _, ok := docker.byName("bs-loja-prod-db"); !ok {
		t.Fatal("o reconciliador devia ter recriado o addon")
	}
	if got := docker.created["bs-loja-prod-db"].Env; strings.Join(got, ",") != strings.Join(firstEnv, ",") {
		t.Errorf("recriado com outra password: %v vs %v", got, firstEnv)
	}
}

func TestDeleteAddonPurge(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	setupEnv(t, svc)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteAddon(ctx, "loja", "prod", "db", true); err != nil {
		t.Fatal(err)
	}
	if len(docker.volumes) != 0 || len(docker.containers) != 0 {
		t.Errorf("sobrou: volumes %v, containers %v", docker.volumes, docker.containers)
	}
	secrets, _ := svc.ListSecrets(ctx, "loja", "prod")
	if len(secrets) != 0 {
		t.Errorf("segredo gerido devia ter ido com o addon: %v", secrets)
	}
}

func TestBackupJob(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	setupEnv(t, svc)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	docker.logs = "backup: 20260101T000000Z.dump (1234 bytes)\n"

	job, err := svc.BackupAddon(ctx, "loja", "prod", "db", "t")
	if err != nil {
		t.Fatal(err)
	}
	final := waitJob(t, svc, job.ID)
	if final.Status != store.JobSucceeded || !strings.Contains(final.Output, "1234 bytes") {
		t.Fatalf("job = %+v", final)
	}
	if !strings.Contains(strings.Join(docker.lastJobCmd, " "), "pg_dump -Fc") {
		t.Errorf("cmd = %v", docker.lastJobCmd)
	}
	// O container do job não pode ficar para trás.
	for _, c := range docker.containers {
		if strings.Contains(c.Names[0], "-job-") {
			t.Errorf("container de job ficou: %s", c.Names[0])
		}
	}

	docker.exitCode = 1
	job, _ = svc.BackupAddon(ctx, "loja", "prod", "db", "t")
	if final := waitJob(t, svc, job.ID); final.Status != store.JobFailed || final.ExitCode == nil || *final.ExitCode != 1 {
		t.Errorf("job falhado = %+v", final)
	}

	if _, err := svc.RestoreAddon(ctx, "loja", "prod", "db", "../../etc/passwd", "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("restore com nome perigoso: err = %v", err)
	}
}

func TestParseBackupList(t *testing.T) {
	got := parseBackupList("20260101T000000Z.dump 10\nlixo\n20260102T000000Z.dump 20\n")
	if len(got) != 2 || got[0].File != "20260102T000000Z.dump" || got[0].Size != 20 {
		t.Errorf("parse = %+v", got)
	}
}

func waitJob(t *testing.T, svc *Service, id string) store.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := svc.GetJob(context.Background(), "loja", "prod", id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != store.JobRunning {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s não terminou", id)
	return store.Job{}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
