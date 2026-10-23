package platform

import (
	"context"
	"fmt"
	"strings"

	"launcher/internal/store"
)

const (
	maxReplicas            = 20
	defaultServiceVCPU     = 0.5
	defaultServiceMemoryMB = 512
)

// ServiceInput é o corpo de criação/edição de um serviço. Ponteiros =
// campos opcionais num PATCH (nil = não mexer).
type ServiceInput struct {
	Slug                 string    `json:"slug"`
	Kind                 string    `json:"kind"`
	Port                 *int      `json:"port"`
	HealthPath           *string   `json:"healthPath"`
	HealthTimeoutSeconds *int      `json:"healthTimeoutSeconds"`
	Cmd                  *[]string `json:"cmd"`
	Env                  *[]string `json:"env"`
	ReleaseCmd           *[]string `json:"releaseCmd"`
	VCPU                 *float64  `json:"vcpu"`
	MemoryMB             *int64    `json:"memoryMb"`
	ScalingMode          *string   `json:"scalingMode"`
	Replicas             *int      `json:"replicas"`
	MinReplicas          *int      `json:"minReplicas"`
	MaxReplicas          *int      `json:"maxReplicas"`
	CPUScaleUpPercent    *float64  `json:"cpuScaleUpPercent"`
	CPUScaleDownPercent  *float64  `json:"cpuScaleDownPercent"`
}

// apply copia os campos preenchidos para svc.
func (in ServiceInput) apply(svc *store.Service) {
	if in.Port != nil {
		svc.Port = *in.Port
	}
	if in.HealthPath != nil {
		svc.HealthPath = strings.TrimSpace(*in.HealthPath)
	}
	if in.HealthTimeoutSeconds != nil {
		svc.HealthTimeoutSeconds = *in.HealthTimeoutSeconds
	}
	if in.Cmd != nil {
		svc.Cmd = *in.Cmd
	}
	if in.Env != nil {
		svc.Env = *in.Env
	}
	if in.ReleaseCmd != nil {
		svc.ReleaseCmd = *in.ReleaseCmd
	}
	if in.VCPU != nil {
		svc.VCPU = *in.VCPU
	}
	if in.MemoryMB != nil {
		svc.MemoryMB = *in.MemoryMB
	}
	if in.ScalingMode != nil {
		svc.ScalingMode = *in.ScalingMode
	}
	if in.Replicas != nil {
		svc.Replicas = *in.Replicas
	}
	if in.MinReplicas != nil {
		svc.MinReplicas = *in.MinReplicas
	}
	if in.MaxReplicas != nil {
		svc.MaxReplicas = *in.MaxReplicas
	}
	if in.CPUScaleUpPercent != nil {
		svc.CPUScaleUpPercent = *in.CPUScaleUpPercent
	}
	if in.CPUScaleDownPercent != nil {
		svc.CPUScaleDownPercent = *in.CPUScaleDownPercent
	}
}

func validateService(svc store.Service) error {
	switch svc.Kind {
	case store.ServiceWeb:
		if svc.Port < 1 || svc.Port > 65535 {
			return invalid("um serviço web precisa da porta onde a aplicação escuta (1-65535)")
		}
	case store.ServiceWorker:
		if svc.Port != 0 {
			return invalid("um worker não recebe tráfego -- não indiques porta")
		}
	default:
		return invalid("tipo de serviço %q desconhecido -- use \"web\" ou \"worker\"", svc.Kind)
	}
	if svc.HealthPath != "" && !strings.HasPrefix(svc.HealthPath, "/") {
		return invalid("o health check tem de ser um caminho começado por \"/\" (ex: /healthz)")
	}
	if svc.HealthTimeoutSeconds < 10 || svc.HealthTimeoutSeconds > 900 {
		return invalid("tempo limite do health check entre 10 e 900 segundos")
	}
	if svc.VCPU <= 0 || svc.MemoryMB < 32 {
		return invalid("recursos inválidos: vCPU > 0 e pelo menos 32 MB de memória")
	}
	for _, e := range svc.Env {
		key, _, ok := strings.Cut(e, "=")
		if !ok || !envVarRe.MatchString(key) {
			return invalid("variável de ambiente %q inválida -- use NOME=valor", e)
		}
	}
	switch svc.ScalingMode {
	case store.ScalingFixed:
		if svc.Replicas < 1 || svc.Replicas > maxReplicas {
			return invalid("réplicas entre 1 e %d (para parar o serviço usa \"parar\")", maxReplicas)
		}
	case store.ScalingAuto:
		if svc.MinReplicas < 1 || svc.MaxReplicas < svc.MinReplicas || svc.MaxReplicas > maxReplicas {
			return invalid("autoscaling: 1 <= mínimo <= máximo <= %d", maxReplicas)
		}
		if svc.CPUScaleDownPercent <= 0 || svc.CPUScaleUpPercent <= svc.CPUScaleDownPercent || svc.CPUScaleUpPercent > 100 {
			return invalid("autoscaling: 0 < limiar de descida < limiar de subida <= 100")
		}
	default:
		return invalid("modo de scaling %q desconhecido -- use \"fixed\" ou \"auto\"", svc.ScalingMode)
	}
	return nil
}

func (s *Service) serviceScope(ctx context.Context, projectSlug, envSlug, svcSlug string) (store.Project, store.Environment, store.Service, error) {
	p, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return store.Project{}, store.Environment{}, store.Service{}, err
	}
	svc, err := s.db.GetServiceBySlug(ctx, e.ID, svcSlug)
	if err != nil {
		return store.Project{}, store.Environment{}, store.Service{}, mapStoreErr(err)
	}
	return p, e, svc, nil
}

// ServiceView é um serviço com o que interessa ver numa lista: a revisão
// atual, quantas réplicas estão prontas, e o endereço público.
type ServiceView struct {
	store.Service
	CurrentRevision *store.Revision `json:"currentRevision,omitempty"`
	ReadyReplicas   int             `json:"readyReplicas"`
	Deploying       bool            `json:"deploying"`
	URLs            []string        `json:"urls"`
	// Problem: porque é que o serviço não está como devia (ex: quota).
	Problem string `json:"problem,omitempty"`
}

func (s *Service) serviceView(ctx context.Context, p store.Project, e store.Environment, svc store.Service) ServiceView {
	v := ServiceView{Service: svc, URLs: []string{}, Deploying: s.rt.isDeploying(svc.ID), Problem: s.rt.getProblem(svc.ID)}
	if svc.CurrentRevisionID != nil {
		if rev, err := s.db.GetRevision(ctx, *svc.CurrentRevisionID); err == nil {
			v.CurrentRevision = &rev
			if replicas, err := s.replicasOf(ctx, svc.ID); err == nil {
				for _, r := range replicas {
					if r.Labels[LabelRevision] == rev.ID && s.rt.isReady(containerName(r)) {
						v.ReadyReplicas++
					}
				}
			}
		}
	}
	if svc.Kind == store.ServiceWeb {
		for _, h := range s.serviceHosts(ctx, p, e, svc) {
			v.URLs = append(v.URLs, s.scheme()+"://"+h)
		}
	}
	return v
}

func (s *Service) ListServices(ctx context.Context, projectSlug, envSlug string) ([]ServiceView, error) {
	p, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return nil, err
	}
	services, err := s.db.ListServices(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	out := make([]ServiceView, 0, len(services))
	for _, svc := range services {
		out = append(out, s.serviceView(ctx, p, e, svc))
	}
	return out, nil
}

func (s *Service) GetService(ctx context.Context, projectSlug, envSlug, svcSlug string) (ServiceView, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return ServiceView{}, err
	}
	return s.serviceView(ctx, p, e, svc), nil
}

func (s *Service) CreateService(ctx context.Context, projectSlug, envSlug string, in ServiceInput, by string) (ServiceView, error) {
	p, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return ServiceView{}, err
	}
	if err := validSlug("slug do serviço", in.Slug); err != nil {
		return ServiceView{}, err
	}
	svc := store.Service{
		ID: newID(), EnvironmentID: e.ID, Slug: in.Slug, Kind: in.Kind,
		HealthTimeoutSeconds: 120, VCPU: defaultServiceVCPU, MemoryMB: defaultServiceMemoryMB,
		ScalingMode: store.ScalingFixed, Replicas: 1, MinReplicas: 1, MaxReplicas: 3,
		CPUScaleUpPercent: 70, CPUScaleDownPercent: 20,
		DesiredState: store.DesiredRunning, CreatedBy: by,
		Cmd: []string{}, Env: []string{}, ReleaseCmd: []string{},
	}
	in.apply(&svc)
	if err := validateService(svc); err != nil {
		return ServiceView{}, err
	}
	if err := s.db.CreateService(ctx, svc); err != nil {
		return ServiceView{}, mapStoreErr(err)
	}
	return s.serviceView(ctx, p, e, svc), nil
}

// UpdateService grava a configuração. Scaling aplica-se já (o
// reconciliador ajusta as réplicas); o resto (imagem, env, cmd, recursos,
// porta) só no próximo deploy, porque vive na revisão.
func (s *Service) UpdateService(ctx context.Context, projectSlug, envSlug, svcSlug string, in ServiceInput) (ServiceView, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return ServiceView{}, err
	}
	in.apply(&svc)
	if err := validateService(svc); err != nil {
		return ServiceView{}, err
	}
	if err := s.db.UpdateService(ctx, svc); err != nil {
		return ServiceView{}, mapStoreErr(err)
	}
	s.rt.forgetDesired(svc.ID)
	s.Kick()
	return s.serviceView(ctx, p, e, svc), nil
}

// SetServiceState liga ("running") ou desliga ("stopped") um serviço sem
// perder a configuração nem a revisão.
func (s *Service) SetServiceState(ctx context.Context, projectSlug, envSlug, svcSlug, state string) error {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return err
	}
	if state != store.DesiredRunning && state != store.DesiredStopped {
		return invalid("estado %q desconhecido", state)
	}
	svc.DesiredState = state
	if err := s.db.UpdateService(ctx, svc); err != nil {
		return err
	}
	s.Kick()
	return nil
}

func (s *Service) DeleteService(ctx context.Context, projectSlug, envSlug, svcSlug string) error {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return err
	}
	if s.rt.isDeploying(svc.ID) {
		return fmt.Errorf("%w: há um deploy em curso neste serviço", ErrConflict)
	}
	if err := s.db.DeleteService(ctx, svc.ID); err != nil {
		return err
	}
	// Primeiro sai do routing, depois param as réplicas.
	s.syncRouting(ctx)
	replicas, err := s.replicasOf(ctx, svc.ID)
	if err != nil {
		return err
	}
	for _, r := range replicas {
		_ = s.docker.StopContainer(ctx, r.ID, 30)
		_ = s.docker.RemoveContainer(ctx, r.ID, true)
	}
	return nil
}

func (s *Service) ListRevisions(ctx context.Context, projectSlug, envSlug, svcSlug string) ([]store.Revision, error) {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListRevisions(ctx, svc.ID, 30)
}

// RunCommand corre um comando pontual (ex: php artisan cache:clear) com a
// imagem e o env da revisão atual do serviço.
func (s *Service) RunCommand(ctx context.Context, projectSlug, envSlug, svcSlug string, cmd []string, by string) (store.Job, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return store.Job{}, err
	}
	if len(cmd) == 0 {
		return store.Job{}, invalid("comando vazio")
	}
	if svc.CurrentRevisionID == nil {
		return store.Job{}, invalid("o serviço ainda não tem nenhum deploy -- não há imagem para correr o comando")
	}
	rev, err := s.db.GetRevision(ctx, *svc.CurrentRevisionID)
	if err != nil {
		return store.Job{}, err
	}
	envVars, err := s.containerEnv(ctx, e, rev)
	if err != nil {
		return store.Job{}, err
	}
	return s.startJob(jobRequest{
		containerSpec: containerSpec{project: p, env: e, image: rev.Image, cmd: cmd, envVars: envVars},
		kind:          "manual", serviceID: &svc.ID, revisionID: &rev.ID, by: by,
	})
}
