package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"launcher/internal/store"
)

func waitJobRunning(t *testing.T, svc *Service, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		svc.rt.mu.Lock()
		_, ok := svc.rt.jobs[id]
		svc.rt.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s nunca começou", id)
}

func TestCancelManualJob(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)

	docker.blockWait = true
	job, err := svc.RunCommand(ctx, "loja", "prod", "web", []string{"php", "artisan", "serve"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	waitJobRunning(t, svc, job.ID)

	if err := svc.CancelJob(ctx, "loja", "prod", job.ID, "ana"); err != nil {
		t.Fatal(err)
	}
	final := waitJob(t, svc, job.ID)
	if final.Status != store.JobCancelled || !strings.Contains(final.Output, "cancelado por ana") || final.ExitCode != nil {
		t.Fatalf("job cancelado = %+v", final)
	}
	for _, c := range docker.containers {
		if strings.Contains(c.Names[0], "-job-") {
			t.Errorf("container do job ficou: %s", c.Names[0])
		}
	}
	if err := svc.CancelJob(ctx, "loja", "prod", job.ID, "ana"); !errors.Is(err, ErrInvalid) {
		t.Errorf("cancelar um job já terminado: err = %v", err)
	}
}

// Um "running" de um launcher anterior: não há ninguém a acompanhá-lo, por
// isso cancelar fecha-o como falhado.
func TestCancelOrphanJob(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	_, e, _ := svc.Scope(ctx, "loja", "prod")
	if err := svc.db.CreateJob(ctx, store.Job{ID: "orfao123", EnvironmentID: e.ID, Kind: "manual", Image: "x:1", Cmd: []string{"x"}, Status: store.JobRunning, CreatedBy: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelJob(ctx, "loja", "prod", "orfao123", "ana"); err != nil {
		t.Fatal(err)
	}
	j, _ := svc.GetJob(ctx, "loja", "prod", "orfao123")
	if j.Status != store.JobFailed || !strings.Contains(j.Output, "já não estava a acompanhar") {
		t.Errorf("órfão = %+v", j)
	}
}

func TestCancelledReleaseFailsDeploy(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80), ReleaseCmd: listp("php", "artisan", "migrate")}, "t"); err != nil {
		t.Fatal(err)
	}
	docker.blockWait = true
	dep, err := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	var jobID string
	deadline := time.Now().Add(5 * time.Second)
	for jobID == "" && time.Now().Before(deadline) {
		jobs, _ := svc.ListJobs(ctx, "loja", "prod")
		if len(jobs) == 1 {
			jobID = jobs[0].ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitJobRunning(t, svc, jobID)
	if err := svc.CancelJob(ctx, "loja", "prod", jobID, "ana"); err != nil {
		t.Fatal(err)
	}
	d := waitDeploy(t, svc, "web", dep.ID)
	if d.Status != store.DeployFailed || !strings.Contains(d.Error, "cancelado") {
		t.Errorf("deploy com release cancelado = %s: %s", d.Status, d.Error)
	}
	if len(appContainers(docker, "")) != 0 {
		t.Error("nenhuma réplica devia ter subido")
	}
}
