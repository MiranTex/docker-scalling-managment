package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"launcher/internal/store"
)

// DeployInput: Image faz um deploy novo dessa imagem com a configuração
// atual do serviço; Revision > 0 volta a pôr essa revisão (rollback); os
// dois vazios = redeploy da imagem atual com a configuração atual (ex:
// para aplicar uma mudança de env) -- ou, se nenhum deploy chegou a
// correr, da imagem da tentativa mais recente.
type DeployInput struct {
	Image    string `json:"image"`
	Revision int    `json:"revision"`
}

// releaseTimeout é o máximo que uma migration pode demorar.
const releaseTimeout = 15 * time.Minute

// replicaLogLines: quanto do log de uma réplica que falhou entra no log do
// deploy.
const replicaLogLines = 30

func (s *Service) ListDeployments(ctx context.Context, projectSlug, envSlug, svcSlug string) ([]store.Deployment, error) {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListDeployments(ctx, svc.ID, 30)
}

func (s *Service) GetDeployment(ctx context.Context, projectSlug, envSlug, svcSlug, id string) (store.Deployment, error) {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return store.Deployment{}, err
	}
	d, err := s.db.GetDeployment(ctx, svc.ID, id)
	return d, mapStoreErr(err)
}

// Deploy cria a revisão e o deploy, e corre o deploy em segundo plano --
// devolve logo, com o deploy em "pending"; o estado acompanha-se em
// GetDeployment.
func (s *Service) Deploy(ctx context.Context, projectSlug, envSlug, svcSlug string, in DeployInput, by string) (store.Deployment, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return store.Deployment{}, err
	}

	var rev store.Revision
	switch {
	case in.Revision > 0:
		if rev, err = s.db.GetRevisionByNumber(ctx, svc.ID, in.Revision); err != nil {
			return store.Deployment{}, mapStoreErr(err)
		}
	default:
		image := strings.TrimSpace(in.Image)
		if image == "" {
			if image, err = s.redeployImage(ctx, svc); err != nil {
				return store.Deployment{}, err
			}
		}
		if strings.ContainsAny(image, " \t\n") {
			return store.Deployment{}, invalid("imagem %q inválida", image)
		}
		if err := s.checkPlatformImageSlug(image, p.Slug); err != nil {
			return store.Deployment{}, err
		}
		rev = store.Revision{
			ID: newID(), ServiceID: svc.ID, Image: image, Cmd: svc.Cmd, Env: svc.Env, ReleaseCmd: svc.ReleaseCmd,
			Port: svc.Port, HealthPath: svc.HealthPath, HealthTimeoutSeconds: svc.HealthTimeoutSeconds,
			VCPU: svc.VCPU, MemoryMB: svc.MemoryMB, MetricsPort: svc.MetricsPort, MetricsPath: svc.MetricsPath,
			NodeSelector: svc.NodeSelector, CreatedBy: by,
		}
	}

	// Falha cedo, antes de criar o que quer que seja: um segredo em falta
	// faria o deploy morrer a meio de qualquer forma.
	if _, err := s.containerEnv(ctx, e, rev); err != nil {
		return store.Deployment{}, err
	}
	if !s.rt.startDeploy(svc.ID) {
		return store.Deployment{}, fmt.Errorf("%w: já há um deploy em curso neste serviço", ErrConflict)
	}
	if in.Revision == 0 {
		if rev, err = s.db.CreateRevision(ctx, rev); err != nil {
			s.rt.endDeploy(svc.ID)
			return store.Deployment{}, mapStoreErr(err)
		}
	}
	dep := store.Deployment{ID: newID(), ServiceID: svc.ID, RevisionID: rev.ID, Status: store.DeployPending, TriggeredBy: by}
	if err := s.db.CreateDeployment(ctx, dep); err != nil {
		s.rt.endDeploy(svc.ID)
		return store.Deployment{}, err
	}
	// Um deploy liga o serviço se estava parado.
	if svc.DesiredState != store.DesiredRunning {
		svc.DesiredState = store.DesiredRunning
		_ = s.db.UpdateService(ctx, svc)
	}

	go func() {
		defer s.rt.endDeploy(svc.ID)
		s.runDeployment(context.Background(), p, e, svc, rev, dep.ID)
		s.Kick()
	}()
	return s.db.GetDeployment(ctx, svc.ID, dep.ID)
}

// runDeployment é o rollout com health-gate:
//
//  1. pull da imagem (com as credenciais do projeto);
//  2. release: corre o releaseCmd (migrations) como job, e pára se falhar;
//  3. sobe N réplicas novas e espera que TODAS passem no health check;
//  4. troca a revisão atual -- o routing passa a ir só para as novas;
//  5. drena e pára as réplicas da revisão anterior.
//
// Uma falha em 1-3 remove as réplicas novas e deixa a revisão anterior a
// servir, intocada.
func (s *Service) runDeployment(ctx context.Context, p store.Project, e store.Environment, svc store.Service, rev store.Revision, depID string) {
	step := func(status, line string) {
		if err := s.db.UpdateDeployment(ctx, depID, status, line); err != nil {
			s.logger.Error("deploy: gravando progresso", "deployment", depID, "err", err)
		}
	}
	var created []string
	fail := func(err error) {
		logLine := "falhou: " + err.Error()
		// O log da réplica que falhou explica quase sempre o porquê -- e
		// tem de ser lido antes de a remover.
		var failed interface{ replica() string }
		if errors.As(err, &failed) {
			logsCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if logs, logErr := s.docker.ContainerLogs(logsCtx, failed.replica(), replicaLogLines); logErr == nil && strings.TrimSpace(logs) != "" {
				logLine += fmt.Sprintf("\n--- últimas linhas do log de %s ---\n%s", failed.replica(), strings.TrimRight(logs, "\n"))
			}
			cancel()
		}
		for _, name := range created {
			s.stopAndRemove(ctx, name)
		}
		if dbErr := s.db.FinishDeployment(ctx, depID, store.DeployFailed, err.Error(), logLine); dbErr != nil {
			s.logger.Error("deploy: gravando falha", "deployment", depID, "err", dbErr)
		}
		s.logger.Warn("deploy falhou", "project", p.Slug, "env", e.Slug, "service", svc.Slug, "revision", rev.Number, "err", err)
	}

	step(store.DeployPulling, "a obter a imagem "+rev.Image)
	if err := s.pullForDeploy(ctx, p.ID, rev.Image, step); err != nil {
		fail(err)
		return
	}

	envVars, err := s.containerEnv(ctx, e, rev)
	if err != nil {
		fail(err)
		return
	}

	if len(rev.ReleaseCmd) > 0 {
		step(store.DeployReleasing, "a correr o release: "+strings.Join(rev.ReleaseCmd, " "))
		job, err := s.runJob(ctx, jobRequest{
			containerSpec: containerSpec{project: p, env: e, image: rev.Image, cmd: rev.ReleaseCmd, envVars: envVars, timeout: releaseTimeout},
			kind:          "release", serviceID: &svc.ID, revisionID: &rev.ID, by: "deploy",
		})
		if err != nil {
			fail(fmt.Errorf("release: %w", err))
			return
		}
		if job.Status == store.JobCancelled {
			fail(fmt.Errorf("o release foi cancelado -- ver o job %s; nada foi trocado", job.ID))
			return
		}
		if job.Status != store.JobSucceeded {
			exit := "?"
			if job.ExitCode != nil {
				exit = fmt.Sprint(*job.ExitCode)
			}
			fail(fmt.Errorf("o release falhou (exit %s) -- ver o job %s; nada foi trocado", exit, job.ID))
			return
		}
		step(store.DeployReleasing, "release concluído")
	}

	n := s.targetReplicas(ctx, svc)
	step(store.DeployStarting, fmt.Sprintf("a subir %d réplica(s) da revisão %d", n, rev.Number))
	for i := 0; i < n; i++ {
		name, err := s.createReplica(ctx, p, e, svc, rev, envVars)
		if err != nil {
			fail(err)
			return
		}
		created = append(created, name)
	}
	if err := s.waitReady(ctx, created, rev); err != nil {
		fail(err)
		return
	}
	step(store.DeployStarting, "todas as réplicas novas passaram no health check")

	step(store.DeploySwitching, "a trocar o tráfego para a revisão "+fmt.Sprint(rev.Number))
	if err := s.db.SetCurrentRevision(ctx, svc.ID, rev.ID); err != nil {
		fail(err)
		return
	}
	created = nil // a partir daqui as novas são as atuais -- já não se apagam
	s.syncRouting(ctx)

	old, err := s.replicasOf(ctx, svc.ID)
	if err == nil {
		var draining []string
		for _, c := range old {
			if c.Labels[LabelRevision] != rev.ID {
				name := containerName(c)
				s.drainReplica(name)
				draining = append(draining, name)
			}
		}
		if len(draining) > 0 {
			s.syncRouting(ctx)
			step(store.DeploySwitching, fmt.Sprintf("a drenar %d réplica(s) antiga(s)", len(draining)))
			time.Sleep(s.drainDelay())
			for _, name := range draining {
				s.stopAndRemove(ctx, name)
			}
		}
	}
	if err := s.db.FinishDeployment(ctx, depID, store.DeploySucceeded, "", "deploy concluído"); err != nil {
		s.logger.Error("deploy: gravando conclusão", "deployment", depID, "err", err)
	}
	s.logger.Info("deploy concluído", "project", p.Slug, "env", e.Slug, "service", svc.Slug, "revision", rev.Number)
}

// pullForDeploy faz sempre pull (uma tag como "latest" pode ter mudado).
// Se o pull falhar sem credenciais configuradas mas a imagem existir no
// host (ex: construída localmente em dev), usa a local.
func (s *Service) pullForDeploy(ctx context.Context, projectID, image string, step func(string, string)) error {
	// Do registry da plataforma: credencial interna, só do próprio
	// projeto, e nunca a cópia local (podia ser de outro projeto).
	if s.isPlatformImage(image) {
		return s.pullFromPlatformRegistry(ctx, image, projectID)
	}
	auth, err := s.registryAuthFor(ctx, projectID, image)
	if err != nil {
		return err
	}
	pullErr := s.docker.PullImageWithAuth(ctx, image, auth)
	if pullErr == nil {
		return nil
	}
	if auth == nil {
		if _, err := s.docker.InspectImage(ctx, image); err == nil {
			step(store.DeployPulling, "pull falhou, a usar a imagem local: "+pullErr.Error())
			return nil
		}
	}
	return fmt.Errorf("pull da imagem %s: %w", image, pullErr)
}

// waitReady espera que todas as réplicas passem no health check dentro do
// tempo limite da revisão. Uma réplica que morra ou reinicie falha logo.
func (s *Service) waitReady(ctx context.Context, names []string, rev store.Revision) error {
	deadline := time.Now().Add(time.Duration(rev.HealthTimeoutSeconds) * time.Second)
	pending := map[string]error{}
	for _, n := range names {
		pending[n] = errors.New("ainda não verificada")
	}
	for {
		for name := range pending {
			err := s.probeReplica(ctx, name, rev, true)
			var died errReplicaDied
			if errors.As(err, &died) {
				return died
			}
			if err == nil {
				s.rt.observe(name, true)
				delete(pending, name)
			} else {
				pending[name] = err
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			for name, err := range pending {
				return errReplicaUnhealthy{name, fmt.Sprintf("a réplica %s não passou no health check em %ds: %v", name, rev.HealthTimeoutSeconds, err)}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.readyPollInterval()):
		}
	}
}

func (s *Service) readyPollInterval() time.Duration {
	if s.cfg.readyPoll > 0 {
		return s.cfg.readyPoll
	}
	return 2 * time.Second
}

// redeployImage é a imagem de um redeploy sem imagem: a que está a correr,
// ou -- se todos os deploys falharam -- a da revisão mais recente, para se
// poder tentar outra vez depois de corrigir a configuração.
func (s *Service) redeployImage(ctx context.Context, svc store.Service) (string, error) {
	if svc.CurrentRevisionID != nil {
		cur, err := s.db.GetRevision(ctx, *svc.CurrentRevisionID)
		if err != nil {
			return "", err
		}
		return cur.Image, nil
	}
	revs, err := s.db.ListRevisions(ctx, svc.ID, 1)
	if err != nil {
		return "", err
	}
	if len(revs) == 0 {
		return "", invalid("o primeiro deploy precisa de uma imagem")
	}
	return revs[0].Image, nil
}
