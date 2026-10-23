package platform

import (
	"context"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

const (
	reconcileInterval = 10 * time.Second
	backupCheckEvery  = 15 * time.Minute
	backupEvery       = 24 * time.Hour
	firstBackupAfter  = time.Hour
)

// Run é o ciclo de vida da plataforma: no arranque limpa o que ficou a meio
// de uma execução anterior, e depois converge periodicamente o Docker para
// o que está na base (addons e, na fase seguinte, serviços). Bloqueia até
// ctx terminar.
func (s *Service) Run(ctx context.Context) {
	s.startupCleanup(ctx)
	s.AttachPlatformToAll(ctx)

	tick := time.NewTicker(reconcileInterval)
	defer tick.Stop()
	backups := time.NewTicker(backupCheckEvery)
	defer backups.Stop()

	s.reconcileOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.reconcileOnce(ctx)
		case <-s.rt.kick:
			s.reconcileOnce(ctx)
		case <-backups.C:
			s.runDueBackups(ctx)
		}
	}
}

// startupCleanup: jobs "running" na base e containers de job no Docker são
// de um launcher anterior que já não os está a acompanhar.
func (s *Service) startupCleanup(ctx context.Context) {
	if err := s.db.FailRunningJobs(ctx); err != nil {
		s.logger.Error("marcando jobs interrompidos", "err", err)
	}
	if err := s.db.FailUnfinishedDeployments(ctx); err != nil {
		s.logger.Error("marcando deploys interrompidos", "err", err)
	}
	jobs, err := s.docker.ListContainers(ctx, true, map[string][]string{"label": {LabelRole + "=" + RoleJob}})
	if err != nil {
		s.logger.Error("listando containers de jobs órfãos", "err", err)
		return
	}
	for _, c := range jobs {
		_ = s.docker.RemoveContainer(ctx, c.ID, true)
	}
}

// Kick pede uma reconciliação já, em vez de esperar pelo próximo tick
// (ex: depois de mudar o scaling de um serviço).
func (s *Service) Kick() {
	select {
	case s.rt.kick <- struct{}{}:
	default:
	}
}

func (s *Service) reconcileOnce(ctx context.Context) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	s.reconcileAddons(ctx)
	s.reconcileServices(ctx)
	s.recheckDomains(ctx)
	s.syncRouting(ctx)
}

func (s *Service) reconcileServices(ctx context.Context) {
	services, err := s.db.ListAllServices(ctx)
	if err != nil {
		s.logger.Error("reconciliador: listando serviços", "err", err)
		return
	}
	containers, err := s.docker.ListContainers(ctx, true, map[string][]string{"label": {LabelRole + "=" + RoleApp}})
	if err != nil {
		s.logger.Error("reconciliador: listando réplicas", "err", err)
		return
	}
	byService := map[string][]dockerclient.Container{}
	for _, c := range containers {
		byService[c.Labels[LabelService]] = append(byService[c.Labels[LabelService]], c)
	}
	known := map[string]bool{}
	scopes := s.newScopeCache()
	for _, svc := range services {
		known[svc.ID] = true
		if s.rt.isDeploying(svc.ID) {
			continue
		}
		p, e, err := scopes.get(ctx, svc.EnvironmentID)
		if err != nil {
			continue
		}
		s.reconcileService(ctx, p, e, svc, byService[svc.ID])
	}
	// Réplicas de serviços que já não existem (apagados com o launcher em
	// baixo, por exemplo).
	for svcID, list := range byService {
		if known[svcID] {
			continue
		}
		for _, c := range list {
			s.logger.Info("reconciliador: réplica de serviço inexistente", "container", containerName(c))
			s.stopAndRemove(ctx, containerName(c))
		}
	}
}

// scopeCache evita ler o mesmo projeto/ambiente da base uma vez por
// recurso, dentro de uma mesma passagem.
type scopeCache struct {
	s        *Service
	envs     map[string]store.Environment
	projects map[string]store.Project
}

func (s *Service) newScopeCache() *scopeCache {
	return &scopeCache{s: s, envs: map[string]store.Environment{}, projects: map[string]store.Project{}}
}

func (c *scopeCache) get(ctx context.Context, envID string) (store.Project, store.Environment, error) {
	e, ok := c.envs[envID]
	if !ok {
		var err error
		if e, err = c.s.db.GetEnvironment(ctx, envID); err != nil {
			return store.Project{}, store.Environment{}, err
		}
		c.envs[envID] = e
	}
	p, ok := c.projects[e.ProjectID]
	if !ok {
		var err error
		if p, err = c.s.db.GetProject(ctx, e.ProjectID); err != nil {
			return store.Project{}, store.Environment{}, err
		}
		c.projects[e.ProjectID] = p
	}
	return p, e, nil
}

// reconcileAddons garante que todo addon tem o seu container a correr: um
// container apagado à mão é recriado (o volume de dados continua lá), um
// parado é arrancado.
func (s *Service) reconcileAddons(ctx context.Context) {
	addons, err := s.db.ListAllAddons(ctx)
	if err != nil {
		s.logger.Error("reconciliador: listando addons", "err", err)
		return
	}
	known := map[string]bool{}
	scopes := s.newScopeCache()
	for _, a := range addons {
		known[a.ID] = true
		p, e, err := scopes.get(ctx, a.EnvironmentID)
		if err != nil {
			s.logger.Error("reconciliador: ambiente do addon", "addon", a.Slug, "err", err)
			continue
		}
		name := addonContainerName(p, e, a)
		info, err := s.docker.InspectContainer(ctx, name)
		switch {
		case isNotFound(err):
			pw, err := s.addonPassword(ctx, e.ID, a)
			if err != nil {
				s.logger.Error("reconciliador: password do addon", "addon", name, "err", err)
				continue
			}
			s.logger.Warn("reconciliador: container do addon desapareceu, a recriar", "addon", name)
			if err := s.startAddonContainer(ctx, p, e, a, pw); err != nil {
				s.logger.Error("reconciliador: recriando addon", "addon", name, "err", err)
			}
		case err != nil:
			s.logger.Error("reconciliador: inspecionando addon", "addon", name, "err", err)
		case !info.State.Running && info.State.Status != "restarting":
			s.logger.Warn("reconciliador: addon parado, a arrancar", "addon", name, "state", info.State.Status)
			if err := s.docker.StartContainer(ctx, name); err != nil {
				s.logger.Error("reconciliador: arrancando addon", "addon", name, "err", err)
			}
		}
	}

	// Containers de addons que já não existem na base (apagados com o
	// launcher em baixo, ou sobras de uma remoção interrompida). Os
	// volumes ficam: só um purge explícito apaga dados.
	containers, err := s.docker.ListContainers(ctx, true, map[string][]string{"label": {LabelRole + "=" + RoleAddon}})
	if err != nil {
		return
	}
	for _, c := range containers {
		if !known[c.Labels[LabelAddon]] {
			s.logger.Info("reconciliador: container de addon inexistente", "container", containerName(c))
			_ = s.docker.StopContainer(ctx, c.ID, 30)
			_ = s.docker.RemoveContainer(ctx, c.ID, true)
		}
	}
}

// runDueBackups faz o pg_dump de cada addon postgres cujo último backup
// tem mais de 24h (o primeiro, 1h depois de criado). Em série: backups em
// paralelo competiriam pelo disco do host.
func (s *Service) runDueBackups(ctx context.Context) {
	addons, err := s.db.ListAllAddons(ctx)
	if err != nil {
		s.logger.Error("backups: listando addons", "err", err)
		return
	}
	now := time.Now()
	scopes := s.newScopeCache()
	for _, a := range addons {
		if a.Kind != store.AddonPostgres || !a.BackupEnabled {
			continue
		}
		due := a.LastBackupAt == nil && now.Sub(a.CreatedAt) > firstBackupAfter ||
			a.LastBackupAt != nil && now.Sub(*a.LastBackupAt) > backupEvery
		if !due {
			continue
		}
		p, e, err := scopes.get(ctx, a.EnvironmentID)
		if err != nil {
			continue
		}
		req, err := s.backupJob(ctx, p, e, a, "scheduler")
		if err != nil {
			s.logger.Error("backups: preparando backup", "addon", a.Slug, "err", err)
			continue
		}
		job, err := s.runJob(ctx, req)
		if err != nil {
			s.logger.Error("backups: correndo backup", "addon", a.Slug, "err", err)
			continue
		}
		// Marca mesmo se falhou: tentar de novo daqui a 15 min encheria
		// os jobs de falhas iguais; o próximo ciclo diário tenta outra vez.
		_ = s.db.MarkAddonBackedUp(ctx, a.ID, now)
		if job.Status != store.JobSucceeded {
			s.logger.Error("backups: backup agendado falhou", "addon", a.Slug, "job_id", job.ID)
		}
	}
}
