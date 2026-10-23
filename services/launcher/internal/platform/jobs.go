package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// maxJobOutput é quanto do fim do output de um job fica guardado -- o
// suficiente para ver o erro de uma migration, sem encher a base.
const maxJobOutput = 64 * 1024

// containerSpec descreve um container que corre até ao fim, num ambiente.
type containerSpec struct {
	project store.Project
	env     store.Environment
	name    string
	image   string
	cmd     []string
	envVars []string
	binds   []string
	labels  map[string]string
	timeout time.Duration
}

// runToCompletion cria, arranca e espera por um container, e devolve o
// exit code e o fim do output. O container é sempre removido no fim.
func (s *Service) runToCompletion(ctx context.Context, spec containerSpec) (int, string, error) {
	if err := s.ensureImage(ctx, spec.image, spec.project.ID); err != nil {
		return -1, "", err
	}
	labels := environmentLabels(spec.project, spec.env)
	labels[LabelRole] = RoleJob
	for k, v := range spec.labels {
		labels[k] = v
	}
	id, err := s.docker.CreateContainer(ctx, spec.name, dockerclient.CreateContainerRequest{
		Image:  spec.image,
		Cmd:    spec.cmd,
		Env:    spec.envVars,
		Labels: labels,
		HostConfig: &dockerclient.CreateHostConfig{
			Binds:       spec.binds,
			NetworkMode: spec.env.Network,
			SecurityOpt: dockerclient.AppSecurityOpt(),
		},
	})
	if err != nil {
		return -1, "", fmt.Errorf("criando container do job: %w", err)
	}
	// Contexto próprio para a limpeza: se ctx expirou (timeout), ainda
	// temos de conseguir parar e remover o container.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.docker.RemoveContainer(cleanup, id, true)
	}()
	if err := s.docker.StartContainer(ctx, id); err != nil {
		return -1, "", fmt.Errorf("arrancando container do job: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()
	code, waitErr := s.docker.WaitContainer(waitCtx, id)

	logsCtx, cancelLogs := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelLogs()
	output, _ := s.docker.ContainerLogs(logsCtx, id, 2000)
	if len(output) > maxJobOutput {
		output = "[...]\n" + output[len(output)-maxJobOutput:]
	}
	if waitErr != nil {
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return -1, output, fmt.Errorf("job excedeu o tempo limite (%s)", spec.timeout)
		}
		return -1, output, waitErr
	}
	return code, output, nil
}

// jobRequest é um job a registar em launcher.jobs.
type jobRequest struct {
	containerSpec
	kind       string
	addonID    *string
	serviceID  *string
	revisionID *string
	by         string
}

// runJob regista o job, corre-o até ao fim e grava o resultado. Devolve o
// job final; err só para falhas da própria plataforma (o job ter falhado
// está em job.Status).
func (s *Service) runJob(ctx context.Context, req jobRequest) (store.Job, error) {
	job, err := s.createJobRow(ctx, &req)
	if err != nil {
		return store.Job{}, err
	}
	s.executeJob(ctx, &job, req)
	return job, nil
}

// startJob é runJob em segundo plano: devolve logo o job "running" e o
// resultado aparece depois em GetJob. onDone (opcional) corre no fim, com
// o job já terminado.
func (s *Service) startJob(req jobRequest, onDone ...func(store.Job)) (store.Job, error) {
	job, err := s.createJobRow(context.Background(), &req)
	if err != nil {
		return store.Job{}, err
	}
	go func(job store.Job) {
		s.executeJob(context.Background(), &job, req)
		for _, f := range onDone {
			f(job)
		}
	}(job)
	return job, nil
}

func (s *Service) createJobRow(ctx context.Context, req *jobRequest) (store.Job, error) {
	job := store.Job{
		ID:            newID(),
		EnvironmentID: req.env.ID,
		AddonID:       req.addonID,
		ServiceID:     req.serviceID,
		RevisionID:    req.revisionID,
		Kind:          req.kind,
		Image:         req.image,
		Cmd:           req.cmd,
		Status:        store.JobRunning,
		CreatedBy:     req.by,
	}
	if job.Cmd == nil {
		job.Cmd = []string{}
	}
	if err := s.db.CreateJob(ctx, job); err != nil {
		return store.Job{}, err
	}
	req.name = "bs-" + req.project.Slug + "-" + req.env.Slug + "-job-" + job.ID[:8]
	if req.labels == nil {
		req.labels = map[string]string{}
	}
	req.labels[LabelJob] = job.ID
	if req.timeout == 0 {
		req.timeout = 30 * time.Minute
	}
	return job, nil
}

func (s *Service) executeJob(ctx context.Context, job *store.Job, req jobRequest) {
	code, output, err := s.runToCompletion(ctx, req.containerSpec)
	job.Status = store.JobSucceeded
	if err != nil {
		job.Status = store.JobFailed
		output += "\n[plataforma] " + err.Error()
	} else if code != 0 {
		job.Status = store.JobFailed
	}
	job.Output = output
	var exit *int
	if err == nil {
		exit = &code
	}
	job.ExitCode = exit
	saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if dbErr := s.db.FinishJob(saveCtx, job.ID, job.Status, exit, output); dbErr != nil {
		s.logger.Error("erro gravando resultado do job", "job_id", job.ID, "err", dbErr)
	}
	s.logger.Info("job terminado", "job_id", job.ID, "kind", job.Kind, "status", job.Status, "exit_code", code)
}

func (s *Service) ListJobs(ctx context.Context, projectSlug, envSlug string) ([]store.Job, error) {
	_, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListJobs(ctx, e.ID, 50)
}

func (s *Service) GetJob(ctx context.Context, projectSlug, envSlug, id string) (store.Job, error) {
	_, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return store.Job{}, err
	}
	j, err := s.db.GetJob(ctx, e.ID, id)
	return j, mapStoreErr(err)
}

// ensureImage garante a imagem no host: usa a local se existir, senão
// faz pull. projectID serve para escolher credenciais de registry (fase
// seguinte); por agora só registries públicos.
func (s *Service) ensureImage(ctx context.Context, image, projectID string) error {
	if _, err := s.docker.InspectImage(ctx, image); err == nil {
		return nil
	}
	if err := s.docker.PullImage(ctx, image); err != nil {
		return fmt.Errorf("obtendo imagem %s: %w", image, err)
	}
	return nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
