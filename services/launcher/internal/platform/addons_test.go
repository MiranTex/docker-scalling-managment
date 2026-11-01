package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"launcher/internal/dockerclient"
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
	if pw == "" || resolved[0] != "X=postgres://app:"+pw+"@db:5432/app?sslmode=require" {
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

func TestAddonStopStart(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	setupEnv(t, svc)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		info, err := docker.InspectContainer(ctx, "bs-loja-prod-db")
		if err != nil {
			return "missing"
		}
		return info.State.Status
	}

	if err := svc.SetAddonState(ctx, "loja", "prod", "db", "stopped"); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != "exited" {
		t.Fatalf("depois de parar: %s", got)
	}
	// O reconciliador deixa-o parado, e o que precisa dele falha logo.
	svc.reconcileAddons(ctx)
	if got := state(); got != "exited" {
		t.Errorf("o reconciliador arrancou um addon parado de propósito: %s", got)
	}
	if _, err := svc.BackupAddon(ctx, "loja", "prod", "db", "t"); !errors.Is(err, ErrConflict) {
		t.Errorf("backup de um addon parado: err = %v", err)
	}
	if _, err := svc.AddonTerminalTarget(ctx, "loja", "prod", "db"); !errors.Is(err, ErrConflict) {
		t.Errorf("terminal de um addon parado: err = %v", err)
	}
	views, _ := svc.ListAddons(ctx, "loja", "prod")
	if views[0].Status != "stopped" || views[0].DesiredState != "stopped" {
		t.Errorf("vista = %s / %s", views[0].Status, views[0].DesiredState)
	}

	if err := svc.SetAddonState(ctx, "loja", "prod", "db", "running"); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != "running" {
		t.Errorf("depois de arrancar: %s", got)
	}
	// Arrancar recria o container se entretanto desapareceu.
	_ = svc.SetAddonState(ctx, "loja", "prod", "db", "stopped")
	_ = docker.RemoveContainer(ctx, "bs-loja-prod-db", true)
	if err := svc.SetAddonState(ctx, "loja", "prod", "db", "running"); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != "running" {
		t.Errorf("depois de arrancar sem container: %s", got)
	}
	if err := svc.SetAddonState(ctx, "loja", "prod", "db", "paused"); !errors.Is(err, ErrInvalid) {
		t.Errorf("estado inválido: err = %v", err)
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

func TestReconcilerRemovesOrphanAddonContainers(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	setupEnv(t, svc)
	docker.containers["cid-orfao"] = dockerclient.Container{ID: "cid-orfao", Names: []string{"/orfao"}, State: "running",
		Labels: map[string]string{LabelRole: RoleAddon, LabelAddon: "ja-nao-existe"}}
	svc.reconcileOnce(ctx)
	if _, ok := docker.byName("orfao"); ok {
		t.Error("container de addon sem linha na base devia ter sido removido")
	}
}

func TestStorageAddon(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.StorageImage = "chrislusf/seaweedfs:4.50"
	setupEnv(t, svc)

	a, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "files", Kind: "storage"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if a.EnvVar != "STORAGE" || a.Host != "files:8333" || a.BackupEnabled || len(a.EnvVars) != 6 {
		t.Errorf("addon = %+v", a)
	}
	req := docker.created["bs-loja-prod-files"]
	if req.Image != "chrislusf/seaweedfs:4.50" || req.Cmd[0] != "mini" || req.HostConfig.Binds[0] != "bs-loja-prod-files-data:/data" {
		t.Errorf("pedido de criação = %+v", req)
	}
	containerEnv := map[string]string{}
	for _, kv := range req.Env {
		k, v, _ := strings.Cut(kv, "=")
		containerEnv[k] = v
	}
	key, secret := containerEnv["AWS_ACCESS_KEY_ID"], containerEnv["AWS_SECRET_ACCESS_KEY"]
	if len(key) != 20 || secret == "" || containerEnv["WEED_JWT_FILER_SIGNING_KEY"] == "" {
		t.Errorf("env do container = %v", req.Env)
	}

	// Os serviços do ambiente recebem todas as variáveis, com as mesmas
	// credenciais do container.
	_, env, _ := svc.Scope(ctx, "loja", "prod")
	got, err := svc.containerEnv(ctx, env, store.Revision{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"STORAGE_URL=http://" + key + ":" + secret + "@files:8333/app",
		"STORAGE_ENDPOINT=http://files:8333",
		"STORAGE_BUCKET=app",
		"STORAGE_REGION=us-east-1",
		"STORAGE_ACCESS_KEY_ID=" + key,
		"STORAGE_SECRET_ACCESS_KEY=" + secret,
	}
	for _, w := range want {
		if !contains(got, w) {
			t.Errorf("env dos serviços sem %s: %v", w, got)
		}
	}

	creds, err := svc.AddonCredentials(ctx, "loja", "prod", "files")
	if err != nil {
		t.Fatal(err)
	}
	if creds.User != key || creds.Password != secret || creds.Database != "app" || creds.Port != 8333 || creds.Region != "us-east-1" {
		t.Errorf("credenciais = %+v", creds)
	}

	// Recriado pelo reconciliador, mantém as credenciais.
	if err := docker.RemoveContainer(ctx, "bs-loja-prod-files", true); err != nil {
		t.Fatal(err)
	}
	svc.reconcileOnce(ctx)
	if !contains(docker.created["bs-loja-prod-files"].Env, "AWS_ACCESS_KEY_ID="+key) {
		t.Error("o container recriado mudou de credenciais")
	}

	// Um segundo storage precisa de outro prefixo.
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "media", Kind: "storage"}, "t"); !errors.Is(err, ErrConflict) {
		t.Errorf("prefixo repetido: err = %v", err)
	}
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "media", Kind: "storage", EnvVar: "MEDIA"}, "t"); err != nil {
		t.Fatal(err)
	}

	// Apagar leva todos os segredos do addon.
	if err := svc.DeleteAddon(ctx, "loja", "prod", "files", false); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.containerEnv(ctx, env, store.Revision{})
	for _, kv := range got {
		if strings.HasPrefix(kv, "STORAGE_") {
			t.Errorf("segredo ficou depois de apagar: %s", kv)
		}
	}
	if !contains(got, "MEDIA_BUCKET=app") {
		t.Errorf("o outro storage perdeu as variáveis: %v", got)
	}
}

// O Postgres arranca com TLS: certificado copiado antes de arrancar, label
// a marcá-lo e o URL em sslmode=require. Um addon de antes do TLS é
// recriado pelo reconciliador com a mesma password, e o URL muda.
func TestPostgresAddonTLS(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	setupEnv(t, svc)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	req := docker.created["bs-loja-prod-db"]
	if req.Labels[LabelAddonTLS] != "1" || !strings.Contains(strings.Join(req.Cmd, " "), "ssl=on") {
		t.Errorf("container sem TLS: labels %v, cmd %v", req.Labels, req.Cmd)
	}
	if !docker.copied["cid-bs-loja-prod-db:/etc/bs-tls/server.key"] || !docker.copied["cid-bs-loja-prod-db:/etc/bs-tls/server.crt"] {
		t.Errorf("certificado não entrou no container: %v", docker.copied)
	}

	// Um addon de antes: sem a label, e com o URL antigo.
	_, env, _ := svc.Scope(ctx, "loja", "prod")
	a, _ := svc.db.GetAddonBySlug(ctx, env.ID, "db")
	_, pw, _ := svc.addonLogin(ctx, env.ID, a)
	if err := svc.putSecret(ctx, env.ID, "DATABASE_URL", "postgres://app:"+pw+"@db:5432/app?sslmode=disable", "addon:db", "t"); err != nil {
		t.Fatal(err)
	}
	docker.mu.Lock()
	c := docker.containers["cid-bs-loja-prod-db"]
	c.Labels = map[string]string{}
	c.State = "running"
	docker.containers["cid-bs-loja-prod-db"] = c
	docker.mu.Unlock()

	svc.reconcileOnce(ctx)
	if docker.created["bs-loja-prod-db"].Labels[LabelAddonTLS] != "1" {
		t.Error("o reconciliador não recriou o addon com TLS")
	}
	got, _ := svc.ResolveEnv(ctx, env.ID, []string{"${secret:DATABASE_URL}"})
	if got[0] != "postgres://app:"+pw+"@db:5432/app?sslmode=require" {
		t.Errorf("URL depois da migração = %q", got[0])
	}
	if !contains(docker.created["bs-loja-prod-db"].Env, "POSTGRES_PASSWORD="+pw) {
		t.Error("a password mudou na migração")
	}
}

// Os nomes basestack-* são os da plataforma nas redes dos ambientes (o
// launcher chega à base por basestack-database): nenhum serviço ou addon
// os pode ter.
func TestReservedHostSlugs(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	setupEnv(t, svc)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "basestack-database", Kind: "postgres"}, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("addon basestack-database: err = %v", err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "basestack-auth", Kind: "worker"}, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("serviço basestack-auth: err = %v", err)
	}
	// "database" continua a poder ser o nome de um addon de um projeto.
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "database", Kind: "postgres"}, "t"); err != nil {
		t.Errorf("addon database: %v", err)
	}
}
