package platform

import (
	"context"
	"fmt"
	"strings"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

type CreateProjectInput struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func (s *Service) ListProjects(ctx context.Context) ([]store.Project, error) {
	return s.db.ListProjects(ctx, store.DefaultAccountID)
}

func (s *Service) GetProject(ctx context.Context, slug string) (store.Project, error) {
	p, err := s.db.GetProjectBySlug(ctx, store.DefaultAccountID, slug)
	return p, mapStoreErr(err)
}

func (s *Service) CreateProject(ctx context.Context, in CreateProjectInput, by string) (store.Project, error) {
	if err := validSlug("slug do projeto", in.Slug); err != nil {
		return store.Project{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = in.Slug
	}
	p := store.Project{ID: newID(), AccountID: store.DefaultAccountID, Slug: in.Slug, Name: name, CreatedBy: by}
	if err := s.db.CreateProject(ctx, p); err != nil {
		return store.Project{}, mapStoreErr(err)
	}
	return s.GetProject(ctx, in.Slug)
}

type UpdateProjectInput struct {
	Name          *string  `json:"name"`
	QuotaVCPU     *float64 `json:"quotaVcpu"`
	QuotaMemoryMB *int64   `json:"quotaMemoryMb"`
}

func (s *Service) UpdateProject(ctx context.Context, slug string, in UpdateProjectInput) (store.Project, error) {
	p, err := s.GetProject(ctx, slug)
	if err != nil {
		return store.Project{}, err
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return store.Project{}, invalid("nome não pode ser vazio")
		}
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.QuotaVCPU != nil {
		if *in.QuotaVCPU < 0 {
			return store.Project{}, invalid("quota de vCPU não pode ser negativa")
		}
		p.QuotaVCPU = *in.QuotaVCPU
	}
	if in.QuotaMemoryMB != nil {
		if *in.QuotaMemoryMB < 0 {
			return store.Project{}, invalid("quota de memória não pode ser negativa")
		}
		p.QuotaMemoryMB = *in.QuotaMemoryMB
	}
	if err := s.db.UpdateProject(ctx, p); err != nil {
		return store.Project{}, mapStoreErr(err)
	}
	return p, nil
}

// DeleteProject apaga todos os ambientes (containers, redes) e depois o
// projeto. purgeData também apaga os volumes de dados dos addons.
func (s *Service) DeleteProject(ctx context.Context, slug string, purgeData bool) error {
	p, err := s.GetProject(ctx, slug)
	if err != nil {
		return err
	}
	envs, err := s.db.ListEnvironments(ctx, p.ID)
	if err != nil {
		return err
	}
	// O routing sai no próximo reconcile, pedido já.
	defer s.Kick()
	for _, e := range envs {
		if err := s.teardownEnvironment(ctx, e, purgeData); err != nil {
			return fmt.Errorf("apagando ambiente %q: %w", e.Slug, err)
		}
	}
	return s.db.DeleteProject(ctx, p.ID)
}

func (s *Service) ListEnvironments(ctx context.Context, projectSlug string) ([]store.Environment, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListEnvironments(ctx, p.ID)
}

// Scope resolve projeto+ambiente pelos slugs do URL -- quase toda operação
// dentro de um ambiente começa aqui.
func (s *Service) Scope(ctx context.Context, projectSlug, envSlug string) (store.Project, store.Environment, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return store.Project{}, store.Environment{}, err
	}
	e, err := s.db.GetEnvironmentBySlug(ctx, p.ID, envSlug)
	if err != nil {
		return store.Project{}, store.Environment{}, mapStoreErr(err)
	}
	return p, e, nil
}

// networkName é bs-<projeto>-<ambiente>: legível em `docker network ls` e
// sem colisões, porque os slugs não podem ter "--" no início/fim.
func networkName(projectSlug, envSlug string) string {
	return "bs-" + projectSlug + "-" + envSlug
}

func environmentLabels(p store.Project, e store.Environment) map[string]string {
	return map[string]string{
		dockerclient.PlatformLabel: dockerclient.PlatformLabelValue,
		LabelProject:               p.Slug,
		LabelEnvironment:           e.Slug,
		LabelProjectID:             p.ID,
		LabelEnvironmentID:         e.ID,
	}
}

func (s *Service) CreateEnvironment(ctx context.Context, projectSlug, envSlug, by string) (store.Environment, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return store.Environment{}, err
	}
	if err := validSlug("slug do ambiente", envSlug); err != nil {
		return store.Environment{}, err
	}
	e := store.Environment{ID: newID(), ProjectID: p.ID, Slug: envSlug, Network: networkName(p.Slug, envSlug), CreatedBy: by}

	// A linha primeiro: o UNIQUE apanha um ambiente repetido antes de
	// mexer no Docker.
	if err := s.db.CreateEnvironment(ctx, e); err != nil {
		return store.Environment{}, mapStoreErr(err)
	}
	if _, err := s.docker.CreateLabeledNetwork(ctx, e.Network, environmentLabels(p, e)); err != nil {
		_ = s.db.DeleteEnvironment(ctx, e.ID)
		return store.Environment{}, fmt.Errorf("criando rede %s: %w", e.Network, err)
	}
	s.attachPlatform(ctx, e.Network)
	return s.db.GetEnvironment(ctx, e.ID)
}

// attachPlatform liga o Traefik e o próprio launcher à rede de um
// ambiente. De melhor esforço: em dev o Traefik pode nem estar a correr, e
// já estar ligado não é erro que interesse.
func (s *Service) attachPlatform(ctx context.Context, network string) {
	for _, c := range []string{s.cfg.TraefikContainer, s.cfg.SelfContainer} {
		if c == "" {
			continue
		}
		if err := s.docker.ConnectNetwork(ctx, network, c); err != nil && !strings.Contains(err.Error(), "already exists") {
			s.logger.Warn("não foi possível ligar container da plataforma à rede do ambiente", "network", network, "container", c, "err", err)
		}
	}
}

// AttachPlatformToAll volta a ligar o Traefik e o launcher a todas as redes
// de ambiente -- chamado no arranque, porque um container recriado (ex:
// upgrade do Traefik) perde as ligações feitas em runtime.
func (s *Service) AttachPlatformToAll(ctx context.Context) {
	envs, err := s.db.ListAllEnvironments(ctx)
	if err != nil {
		s.logger.Error("listando ambientes para ligar a plataforma", "err", err)
		return
	}
	for _, e := range envs {
		s.attachPlatform(ctx, e.Network)
	}
}

func (s *Service) DeleteEnvironment(ctx context.Context, projectSlug, envSlug string, purgeData bool) error {
	_, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return err
	}
	defer s.Kick()
	return s.teardownEnvironment(ctx, e, purgeData)
}

// teardownEnvironment para e remove todo container do ambiente (apps,
// addons, jobs), desliga a plataforma da rede, apaga a rede e por fim a
// linha (que leva consigo segredos e o resto, por ON DELETE CASCADE).
func (s *Service) teardownEnvironment(ctx context.Context, e store.Environment, purgeData bool) error {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	services, err := s.db.ListServices(ctx, e.ID)
	if err != nil {
		return err
	}
	for _, svc := range services {
		if s.rt.isDeploying(svc.ID) {
			return fmt.Errorf("%w: o serviço %s tem um deploy em curso -- espera que acabe", ErrConflict, svc.Slug)
		}
	}
	containers, err := s.docker.ListContainers(ctx, true, map[string][]string{"label": {LabelEnvironmentID + "=" + e.ID}})
	if err != nil {
		return fmt.Errorf("listando containers do ambiente: %w", err)
	}
	for _, c := range containers {
		_ = s.docker.StopContainer(ctx, c.ID, 10)
		if err := s.docker.RemoveContainer(ctx, c.ID, true); err != nil {
			return fmt.Errorf("removendo container %s: %w", c.ID, err)
		}
	}
	if purgeData {
		if err := s.purgeEnvironmentVolumes(ctx, e); err != nil {
			return err
		}
	}
	for _, c := range []string{s.cfg.TraefikContainer, s.cfg.SelfContainer} {
		if c != "" {
			_ = s.docker.DisconnectNetwork(ctx, e.Network, c)
		}
	}
	if err := s.docker.RemoveNetwork(ctx, e.Network); err != nil && !strings.Contains(err.Error(), "404") {
		return fmt.Errorf("removendo rede %s: %w", e.Network, err)
	}
	return s.db.DeleteEnvironment(ctx, e.ID)
}

// purgeEnvironmentVolumes apaga os volumes de dados do ambiente (addons e
// backups), encontrados pela label do ambiente.
func (s *Service) purgeEnvironmentVolumes(ctx context.Context, e store.Environment) error {
	vols, err := s.docker.ListVolumesByLabel(ctx, LabelEnvironmentID+"="+e.ID)
	if err != nil {
		return fmt.Errorf("listando volumes do ambiente: %w", err)
	}
	for _, v := range vols {
		if err := s.docker.RemoveVolume(ctx, v.Name); err != nil {
			return fmt.Errorf("apagando volume %s: %w", v.Name, err)
		}
	}
	return nil
}
