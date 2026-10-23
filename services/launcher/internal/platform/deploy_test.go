package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"launcher/internal/store"
)

// probeSwitch decide, por imagem, se o health check passa -- cada réplica
// herda a imagem da revisão, e o teste muda o comportamento por versão.
type probeSwitch struct {
	mu     sync.Mutex
	failOn map[string]bool // imagem -> falha
	docker *fakeDocker
}

func (p *probeSwitch) probe(_ context.Context, host string, _ int, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.docker.containers[host]; ok && p.failOn[c.Image] {
		return errors.New("connection refused")
	}
	return nil
}

func newDeployService(t *testing.T) (*Service, *fakeDocker, *probeSwitch, string) {
	t.Helper()
	svc, docker := newTestService(t)
	dir := t.TempDir()
	svc.cfg.TraefikDynamicDir = dir
	svc.cfg.PublicBaseDomain = "example.test"
	svc.cfg.PostgresImage = "postgres:17-alpine"
	svc.cfg.readyPoll = 10 * time.Millisecond
	svc.cfg.drainDelay = 10 * time.Millisecond
	svc.cfg.workerReadyAfter = time.Millisecond
	ps := &probeSwitch{failOn: map[string]bool{}, docker: docker}
	svc.probe = ps.probe
	setupEnv(t, svc)
	return svc, docker, ps, dir
}

func intp(v int) *int             { return &v }
func strp(v string) *string       { return &v }
func listp(v ...string) *[]string { return &v }

func waitDeploy(t *testing.T, svc *Service, svcSlug, id string) store.Deployment {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		d, err := svc.GetDeployment(context.Background(), "loja", "prod", svcSlug, id)
		if err != nil {
			t.Fatal(err)
		}
		if d.Status == store.DeploySucceeded || d.Status == store.DeployFailed {
			// O goroutine do deploy ainda tem de libertar o serviço.
			for svc.rt.isDeploying(d.ServiceID) {
				time.Sleep(5 * time.Millisecond)
			}
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("deploy %s não terminou", id)
	return store.Deployment{}
}

func appContainers(d *fakeDocker, image string) []string {
	var out []string
	for _, c := range d.byRole(RoleApp) {
		if image == "" || c.Image == image {
			out = append(out, c.ID)
		}
	}
	return out
}

func TestDeployHealthGatedRollout(t *testing.T) {
	ctx := context.Background()
	svc, docker, ps, dir := newDeployService(t)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutSecret(ctx, "loja", "prod", "APP_KEY", "k3y", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{
		Slug: "web", Kind: "web", Port: intp(8080), HealthPath: strp("/healthz"),
		Env: listp("APP_KEY=${secret:APP_KEY}", "PORT=9999"), Replicas: intp(2),
	}, "t"); err != nil {
		t.Fatal(err)
	}

	// Primeiro deploy.
	dep, err := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if d := waitDeploy(t, svc, "web", dep.ID); d.Status != store.DeploySucceeded {
		t.Fatalf("deploy 1 = %s: %s\n%s", d.Status, d.Error, d.Log)
	}
	v1 := appContainers(docker, "app:1")
	if len(v1) != 2 {
		t.Fatalf("réplicas v1 = %v", v1)
	}
	env := strings.Join(docker.created[v1[0]].Env, "\n")
	for _, want := range []string{"APP_KEY=k3y", "PORT=9999", "DATABASE_URL=postgres://app:"} {
		if !strings.Contains(env, want) {
			t.Errorf("env da réplica não tem %q:\n%s", want, env)
		}
	}
	routing, err := os.ReadFile(filepath.Join(dir, routingFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(routing), "Host(`web-prod-loja.example.test`)") || !strings.Contains(string(routing), "http://"+v1[0]+":8080") {
		t.Errorf("routing não aponta para as réplicas v1:\n%s", routing)
	}

	// Segundo deploy com uma versão que nunca fica saudável: falha, as
	// réplicas novas desaparecem, as antigas ficam e o routing não muda.
	svc.cfg.readyPoll = 5 * time.Millisecond
	if _, err := svc.UpdateService(ctx, "loja", "prod", "web", ServiceInput{HealthTimeoutSeconds: intp(10)}); err != nil {
		t.Fatal(err)
	}
	ps.mu.Lock()
	ps.failOn["app:2"] = true
	ps.mu.Unlock()
	start := time.Now()
	dep, err = svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:2"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	d := waitDeploy(t, svc, "web", dep.ID)
	if d.Status != store.DeployFailed || !strings.Contains(d.Error, "health check") {
		t.Fatalf("deploy 2 = %s: %s", d.Status, d.Error)
	}
	if time.Since(start) < 9*time.Second {
		t.Errorf("o deploy desistiu antes do tempo limite (%s)", time.Since(start))
	}
	if got := appContainers(docker, "app:2"); len(got) != 0 {
		t.Errorf("réplicas da versão falhada ficaram: %v", got)
	}
	if got := appContainers(docker, "app:1"); len(got) != 2 {
		t.Errorf("as réplicas v1 deviam continuar: %v", got)
	}
	view, _ := svc.GetService(ctx, "loja", "prod", "web")
	if view.CurrentRevision == nil || view.CurrentRevision.Number != 1 {
		t.Errorf("revisão atual = %+v, queria 1", view.CurrentRevision)
	}

	// Terceiro deploy saudável: troca e as antigas saem.
	ps.mu.Lock()
	ps.failOn = map[string]bool{}
	ps.mu.Unlock()
	dep, _ = svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:3"}, "t")
	if d := waitDeploy(t, svc, "web", dep.ID); d.Status != store.DeploySucceeded {
		t.Fatalf("deploy 3 = %s: %s", d.Status, d.Error)
	}
	if got := appContainers(docker, ""); len(got) != 2 || docker.containers[got[0]].Image != "app:3" {
		t.Errorf("depois do deploy 3 = %v", got)
	}

	// Rollback para a revisão 1 (a 2 falhou, mas existe).
	dep, err = svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Revision: 1}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if d := waitDeploy(t, svc, "web", dep.ID); d.Status != store.DeploySucceeded || d.RevisionNumber != 1 {
		t.Fatalf("rollback = %s rev %d: %s", d.Status, d.RevisionNumber, d.Error)
	}
	if got := appContainers(docker, "app:1"); len(got) != 2 {
		t.Errorf("rollback devia ter posto app:1 de volta: %v", appContainers(docker, ""))
	}
}

func TestDeployReleaseFailureStopsBeforeReplicas(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{
		Slug: "web", Kind: "web", Port: intp(80), ReleaseCmd: listp("php", "artisan", "migrate", "--force"),
	}, "t"); err != nil {
		t.Fatal(err)
	}
	docker.exitCode = 1
	docker.logs = "SQLSTATE[42P01]: relation does not exist"
	dep, err := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	d := waitDeploy(t, svc, "web", dep.ID)
	if d.Status != store.DeployFailed || !strings.Contains(d.Error, "release falhou") {
		t.Fatalf("deploy = %s: %s", d.Status, d.Error)
	}
	if got := appContainers(docker, ""); len(got) != 0 {
		t.Errorf("nenhuma réplica devia ter sido criada: %v", got)
	}
	jobs, _ := svc.ListJobs(ctx, "loja", "prod")
	if len(jobs) != 1 || jobs[0].Kind != "release" || jobs[0].Status != store.JobFailed {
		t.Errorf("jobs = %+v", jobs)
	}
}

func TestDeployRejectsMissingSecretAndConcurrentDeploys(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "wk", Kind: "worker", Env: listp("X=${secret:NOPE}")}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Deploy(ctx, "loja", "prod", "wk", DeployInput{Image: "app:1"}, "t"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("segredo em falta: err = %v", err)
	}
	if _, err := svc.Deploy(ctx, "loja", "prod", "wk", DeployInput{}, "t"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("primeiro deploy sem imagem: err = %v", err)
	}
}

func TestRegistryCredentialsUsedOnPull(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	if err := svc.PutRegistry(ctx, "loja", "ghcr.io", "bot", "ghp_x", "t"); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutRegistry(ctx, "loja", "https://ghcr.io/x", "bot", "y", "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("registry com esquema: err = %v", err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "wk", Kind: "worker"}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "wk", DeployInput{Image: "ghcr.io/eu/app:1"}, "t")
	if d := waitDeploy(t, svc, "wk", dep.ID); d.Status != store.DeploySucceeded {
		t.Fatalf("deploy = %s: %s", d.Status, d.Error)
	}
	if len(docker.pulls) == 0 || docker.pulls[0] != "ghcr.io/eu/app:1 bot@ghcr.io" {
		t.Errorf("pulls = %v", docker.pulls)
	}
}

func TestReconcilerScalesAndReplacesReplicas(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)

	// Mudar o scaling aplica-se sem deploy.
	if _, err := svc.UpdateService(ctx, "loja", "prod", "web", ServiceInput{Replicas: intp(3)}); err != nil {
		t.Fatal(err)
	}
	svc.reconcileOnce(ctx)
	if got := appContainers(docker, ""); len(got) != 3 {
		t.Fatalf("depois de subir para 3: %v", got)
	}

	// Uma réplica parada por fora é substituída.
	victim := appContainers(docker, "")[0]
	docker.mu.Lock()
	c := docker.containers[victim]
	c.State = "exited"
	docker.containers[victim] = c
	docker.mu.Unlock()
	svc.reconcileOnce(ctx)
	if _, ok := docker.containers[victim]; ok {
		t.Error("a réplica parada devia ter sido removida")
	}
	if got := appContainers(docker, ""); len(got) != 3 {
		t.Errorf("devia ter reposto a terceira: %v", got)
	}

	// Descer para 1: duas entram em drenagem e só saem depois do prazo.
	if _, err := svc.UpdateService(ctx, "loja", "prod", "web", ServiceInput{Replicas: intp(1)}); err != nil {
		t.Fatal(err)
	}
	svc.reconcileOnce(ctx)
	time.Sleep(20 * time.Millisecond)
	svc.reconcileOnce(ctx)
	if got := appContainers(docker, ""); len(got) != 1 {
		t.Errorf("depois de descer para 1: %v", got)
	}

	// Parar o serviço remove tudo; voltar a ligar repõe.
	if err := svc.SetServiceState(ctx, "loja", "prod", "web", store.DesiredStopped); err != nil {
		t.Fatal(err)
	}
	svc.reconcileOnce(ctx)
	if got := appContainers(docker, ""); len(got) != 0 {
		t.Errorf("serviço parado ainda tem réplicas: %v", got)
	}
	if err := svc.SetServiceState(ctx, "loja", "prod", "web", store.DesiredRunning); err != nil {
		t.Fatal(err)
	}
	svc.reconcileOnce(ctx)
	if got := appContainers(docker, ""); len(got) != 1 {
		t.Errorf("serviço religado: %v", got)
	}
}

func TestAutoscaleUpAndDown(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{
		Slug: "web", Kind: "web", Port: intp(80), ScalingMode: strp("auto"), MinReplicas: intp(1), MaxReplicas: intp(3),
	}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)
	view, _ := svc.GetService(ctx, "loja", "prod", "web")
	svcID := view.ID

	// 0.5 vCPU a 45% de um core = 90% do alocado > 70%.
	docker.cpuPercent = 45
	svc.reconcileOnce(ctx)
	if got := appContainers(docker, ""); len(got) != 2 {
		t.Fatalf("devia ter subido para 2: %v", got)
	}

	// Em cooldown não muda, mesmo com CPU alta.
	svc.reconcileOnce(ctx)
	if got := appContainers(docker, ""); len(got) != 2 {
		t.Fatalf("cooldown ignorado: %v", got)
	}

	// Fim do cooldown + CPU baixa: desce.
	svc.rt.mu.Lock()
	svc.rt.lastScale[svcID] = time.Time{}
	svc.rt.lastEval[svcID] = time.Time{}
	svc.rt.mu.Unlock()
	docker.cpuPercent = 1
	svc.reconcileOnce(ctx)
	time.Sleep(20 * time.Millisecond)
	svc.reconcileOnce(ctx)
	if got := appContainers(docker, ""); len(got) != 1 {
		t.Errorf("devia ter descido para 1: %v", got)
	}
}

func TestDeleteServiceRemovesReplicasAndRoute(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, dir := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)
	if err := svc.DeleteService(ctx, "loja", "prod", "web"); err != nil {
		t.Fatal(err)
	}
	if got := appContainers(docker, ""); len(got) != 0 {
		t.Errorf("réplicas ficaram: %v", got)
	}
	routing, _ := os.ReadFile(filepath.Join(dir, routingFile))
	if strings.Contains(string(routing), "web-prod-loja") {
		t.Errorf("rota ficou:\n%s", routing)
	}
}

func TestMergeEnv(t *testing.T) {
	got := mergeEnv([]string{"PORT=80", "DATABASE_URL=a"}, []string{"X=1", "PORT=9000"})
	if strings.Join(got, ",") != "PORT=9000,DATABASE_URL=a,X=1" {
		t.Errorf("mergeEnv = %v", got)
	}
}

func TestValidateService(t *testing.T) {
	base := store.Service{Kind: "web", Port: 80, HealthTimeoutSeconds: 120, VCPU: 0.5, MemoryMB: 256, ScalingMode: "fixed", Replicas: 1}
	if err := validateService(base); err != nil {
		t.Fatal(err)
	}
	bad := []func(*store.Service){
		func(s *store.Service) { s.Port = 0 },
		func(s *store.Service) { s.Kind = "worker" }, // worker com porta
		func(s *store.Service) { s.HealthPath = "healthz" },
		func(s *store.Service) { s.Env = []string{"semigual"} },
		func(s *store.Service) { s.Replicas = 0 },
		func(s *store.Service) { s.ScalingMode = "auto"; s.MinReplicas = 3; s.MaxReplicas = 2 },
		func(s *store.Service) {
			s.ScalingMode = "auto"
			s.MinReplicas, s.MaxReplicas = 1, 2
			s.CPUScaleUpPercent, s.CPUScaleDownPercent = 20, 50
		},
	}
	for i, mut := range bad {
		s := base
		mut(&s)
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("caso %d devia ser inválido: %+v", i, s)
		}
	}
}
