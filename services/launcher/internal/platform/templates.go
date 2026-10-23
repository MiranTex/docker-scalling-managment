package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"launcher/internal/store"
)

// TemplateSpec é a configuração de um serviço guardada numa versão de
// modelo -- tudo menos o slug e a imagem. A imagem vive à parte
// (TemplateVersion.Image): só serve para o primeiro deploy, e atualizar um
// serviço para uma versão nova nunca a troca.
type TemplateSpec struct {
	Kind                 string   `json:"kind"`
	Port                 int      `json:"port"`
	HealthPath           string   `json:"healthPath"`
	HealthTimeoutSeconds int      `json:"healthTimeoutSeconds"`
	Cmd                  []string `json:"cmd"`
	Env                  []string `json:"env"`
	ReleaseCmd           []string `json:"releaseCmd"`
	VCPU                 float64  `json:"vcpu"`
	MemoryMB             int64    `json:"memoryMb"`
	ScalingMode          string   `json:"scalingMode"`
	Replicas             int      `json:"replicas"`
	MinReplicas          int      `json:"minReplicas"`
	MaxReplicas          int      `json:"maxReplicas"`
	CPUScaleUpPercent    float64  `json:"cpuScaleUpPercent"`
	CPUScaleDownPercent  float64  `json:"cpuScaleDownPercent"`
	MetricsPort          int      `json:"metricsPort"`
	MetricsPath          string   `json:"metricsPath"`
}

func specFromService(s store.Service) TemplateSpec {
	return TemplateSpec{
		Kind: s.Kind, Port: s.Port, HealthPath: s.HealthPath, HealthTimeoutSeconds: s.HealthTimeoutSeconds,
		Cmd: s.Cmd, Env: s.Env, ReleaseCmd: s.ReleaseCmd, VCPU: s.VCPU, MemoryMB: s.MemoryMB,
		ScalingMode: s.ScalingMode, Replicas: s.Replicas, MinReplicas: s.MinReplicas, MaxReplicas: s.MaxReplicas,
		CPUScaleUpPercent: s.CPUScaleUpPercent, CPUScaleDownPercent: s.CPUScaleDownPercent,
		MetricsPort: s.MetricsPort, MetricsPath: s.MetricsPath,
	}
}

func (t TemplateSpec) applyTo(s *store.Service) {
	s.Kind, s.Port, s.HealthPath, s.HealthTimeoutSeconds = t.Kind, t.Port, t.HealthPath, t.HealthTimeoutSeconds
	s.Cmd, s.Env, s.ReleaseCmd = nonNil(t.Cmd), nonNil(t.Env), nonNil(t.ReleaseCmd)
	s.VCPU, s.MemoryMB = t.VCPU, t.MemoryMB
	s.ScalingMode, s.Replicas, s.MinReplicas, s.MaxReplicas = t.ScalingMode, t.Replicas, t.MinReplicas, t.MaxReplicas
	s.CPUScaleUpPercent, s.CPUScaleDownPercent = t.CPUScaleUpPercent, t.CPUScaleDownPercent
	s.MetricsPort, s.MetricsPath = t.MetricsPort, t.MetricsPath
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// asFields devolve a spec como campo -> valor JSON, para comparar campo a
// campo sem uma lista de campos escrita à mão (que ficaria desatualizada).
func (t TemplateSpec) asFields() map[string]json.RawMessage {
	t.Cmd, t.Env, t.ReleaseCmd = nonNil(t.Cmd), nonNil(t.Env), nonNil(t.ReleaseCmd)
	b, _ := json.Marshal(t)
	out := map[string]json.RawMessage{}
	_ = json.Unmarshal(b, &out)
	return out
}

// validateTemplateSpec confere uma spec com as mesmas regras de um serviço.
func validateTemplateSpec(spec TemplateSpec) error {
	s := store.Service{}
	spec.applyTo(&s)
	return validateService(s)
}

func validTemplateImage(image string) error {
	image = strings.TrimSpace(image)
	if image == "" {
		return invalid("a imagem é obrigatória -- é ela que corre no primeiro deploy")
	}
	if strings.ContainsAny(image, " \t\n") {
		return invalid("imagem %q inválida", image)
	}
	// Uma tag explícita: sem ela o Docker assume "latest", que muda por
	// baixo e torna o primeiro deploy imprevisível.
	last := image[strings.LastIndex(image, "/")+1:]
	if !strings.Contains(last, ":") && !strings.Contains(last, "@") {
		return invalid("indica a tag da imagem (ex: %s:1.0.0)", image)
	}
	return nil
}

// TemplateDetail é um modelo com o histórico de versões.
type TemplateDetail struct {
	store.Template
	Versions []TemplateVersionView `json:"versions"`
}

type TemplateVersionView struct {
	store.TemplateVersion
	Spec TemplateSpec `json:"spec"`
}

func versionView(v store.TemplateVersion) (TemplateVersionView, error) {
	var spec TemplateSpec
	if err := json.Unmarshal(v.Spec, &spec); err != nil {
		return TemplateVersionView{}, fmt.Errorf("versão %d com spec ilegível: %w", v.Version, err)
	}
	return TemplateVersionView{TemplateVersion: v, Spec: spec}, nil
}

func (s *Service) templateScope(ctx context.Context, projectSlug, templateSlug string) (store.Project, store.Template, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return store.Project{}, store.Template{}, err
	}
	t, err := s.db.GetTemplateBySlug(ctx, p.ID, templateSlug)
	if err != nil {
		return store.Project{}, store.Template{}, mapStoreErr(err)
	}
	return p, t, nil
}

func (s *Service) ListTemplates(ctx context.Context, projectSlug string) ([]store.Template, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListTemplates(ctx, p.ID)
}

func (s *Service) GetTemplate(ctx context.Context, projectSlug, templateSlug string) (TemplateDetail, error) {
	_, t, err := s.templateScope(ctx, projectSlug, templateSlug)
	if err != nil {
		return TemplateDetail{}, err
	}
	versions, err := s.db.ListTemplateVersions(ctx, t.ID)
	if err != nil {
		return TemplateDetail{}, err
	}
	out := TemplateDetail{Template: t, Versions: []TemplateVersionView{}}
	for _, v := range versions {
		vv, err := versionView(v)
		if err != nil {
			return TemplateDetail{}, err
		}
		out.Versions = append(out.Versions, vv)
	}
	return out, nil
}

type TemplateVersionInput struct {
	Image string       `json:"image"`
	Notes string       `json:"notes"`
	Spec  TemplateSpec `json:"spec"`
}

type CreateTemplateInput struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TemplateVersionInput
}

func (s *Service) CreateTemplate(ctx context.Context, projectSlug string, in CreateTemplateInput, by string) (TemplateDetail, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return TemplateDetail{}, err
	}
	if err := validSlug("slug do modelo", in.Slug); err != nil {
		return TemplateDetail{}, err
	}
	if err := validTemplateImage(in.Image); err != nil {
		return TemplateDetail{}, err
	}
	if err := validateTemplateSpec(in.Spec); err != nil {
		return TemplateDetail{}, err
	}
	spec, _ := json.Marshal(in.Spec)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = in.Slug
	}
	t := store.Template{ID: newID(), ProjectID: p.ID, Slug: in.Slug, Name: name, Description: strings.TrimSpace(in.Description), CreatedBy: by}
	v := store.TemplateVersion{ID: newID(), Spec: spec, Image: strings.TrimSpace(in.Image), Notes: strings.TrimSpace(in.Notes), CreatedBy: by}
	if err := s.db.CreateTemplate(ctx, t, v); err != nil {
		return TemplateDetail{}, mapStoreErr(err)
	}
	return s.GetTemplate(ctx, projectSlug, in.Slug)
}

// AddTemplateVersion cria a versão seguinte. O tipo (web/worker) é fixo
// desde a v1: trocá-lo num serviço existente mudaria routing e portas de um
// serviço que já está no ar.
func (s *Service) AddTemplateVersion(ctx context.Context, projectSlug, templateSlug string, in TemplateVersionInput, by string) (TemplateVersionView, error) {
	_, t, err := s.templateScope(ctx, projectSlug, templateSlug)
	if err != nil {
		return TemplateVersionView{}, err
	}
	if t.ArchivedAt != nil {
		return TemplateVersionView{}, invalid("o modelo %s está arquivado", t.Slug)
	}
	if err := validTemplateImage(in.Image); err != nil {
		return TemplateVersionView{}, err
	}
	if err := validateTemplateSpec(in.Spec); err != nil {
		return TemplateVersionView{}, err
	}
	first, err := s.db.GetTemplateVersion(ctx, t.ID, 1)
	if err != nil {
		return TemplateVersionView{}, err
	}
	firstView, err := versionView(first)
	if err != nil {
		return TemplateVersionView{}, err
	}
	if in.Spec.Kind != firstView.Spec.Kind {
		return TemplateVersionView{}, invalid("o tipo do modelo é %s desde a v1 e não pode mudar -- cria outro modelo", firstView.Spec.Kind)
	}
	spec, _ := json.Marshal(in.Spec)
	v, err := s.db.CreateTemplateVersion(ctx, store.TemplateVersion{ID: newID(), TemplateID: t.ID, Spec: spec, Image: strings.TrimSpace(in.Image), Notes: strings.TrimSpace(in.Notes), CreatedBy: by})
	if err != nil {
		return TemplateVersionView{}, mapStoreErr(err)
	}
	return versionView(v)
}

func (s *Service) UpdateTemplateInfo(ctx context.Context, projectSlug, templateSlug, name, description string) error {
	_, t, err := s.templateScope(ctx, projectSlug, templateSlug)
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return invalid("o nome não pode ficar vazio")
	}
	return s.db.UpdateTemplateInfo(ctx, t.ID, strings.TrimSpace(name), strings.TrimSpace(description))
}

func (s *Service) ArchiveTemplate(ctx context.Context, projectSlug, templateSlug string) error {
	_, t, err := s.templateScope(ctx, projectSlug, templateSlug)
	if err != nil {
		return err
	}
	return s.db.ArchiveTemplate(ctx, t.ID)
}

// resolveTemplateVersion devolve a versão pedida (0 = a mais recente) de um
// modelo ativo do projeto.
func (s *Service) resolveTemplateVersion(ctx context.Context, p store.Project, templateSlug string, version int) (store.Template, TemplateVersionView, error) {
	t, err := s.db.GetTemplateBySlug(ctx, p.ID, templateSlug)
	if err != nil {
		return store.Template{}, TemplateVersionView{}, mapStoreErr(err)
	}
	if t.ArchivedAt != nil {
		return store.Template{}, TemplateVersionView{}, invalid("o modelo %s está arquivado", t.Slug)
	}
	if version == 0 {
		version = t.LatestVersion
	}
	v, err := s.db.GetTemplateVersion(ctx, t.ID, version)
	if err != nil {
		return store.Template{}, TemplateVersionView{}, mapStoreErr(err)
	}
	vv, err := versionView(v)
	return t, vv, err
}

// createServiceFromTemplate copia a spec para o serviço, regista a origem
// e faz o primeiro deploy (com a imagem do modelo, ou a do pedido).
func (s *Service) createServiceFromTemplate(ctx context.Context, p store.Project, e store.Environment, in ServiceInput, by string) (ServiceView, error) {
	t, v, err := s.resolveTemplateVersion(ctx, p, in.Template, in.TemplateVersion)
	if err != nil {
		return ServiceView{}, err
	}
	image := v.Image
	if strings.TrimSpace(in.Image) != "" {
		image = strings.TrimSpace(in.Image)
	}
	svc := store.Service{ID: newID(), EnvironmentID: e.ID, Slug: in.Slug, DesiredState: store.DesiredRunning, CreatedBy: by}
	v.Spec.applyTo(&svc)
	if err := validateService(svc); err != nil {
		return ServiceView{}, err
	}
	if err := s.db.CreateService(ctx, svc); err != nil {
		return ServiceView{}, mapStoreErr(err)
	}
	if err := s.db.SetServiceTemplate(ctx, svc.ID, t.ID, v.Version); err != nil {
		return ServiceView{}, err
	}
	if _, err := s.Deploy(ctx, p.Slug, e.Slug, in.Slug, DeployInput{Image: image}, by); err != nil {
		// O serviço fica criado (com a configuração do modelo); só o
		// deploy falhou -- ex: um segredo referenciado que ainda não existe.
		return ServiceView{}, fmt.Errorf("serviço criado, mas o primeiro deploy não arrancou: %w", err)
	}
	created, err := s.db.GetService(ctx, svc.ID)
	if err != nil {
		return ServiceView{}, err
	}
	return s.serviceView(ctx, p, e, created), nil
}

// TemplateRef é o que a página de um serviço mostra sobre o seu modelo.
type TemplateRef struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Version       int    `json:"version"`
	LatestVersion int    `json:"latestVersion"`
	Archived      bool   `json:"archived"`
}

func (s *Service) templateRef(ctx context.Context, svc store.Service) *TemplateRef {
	if svc.TemplateID == nil || svc.TemplateVersion == nil {
		return nil
	}
	t, err := s.db.GetTemplate(ctx, *svc.TemplateID)
	if err != nil {
		return nil
	}
	return &TemplateRef{Slug: t.Slug, Name: t.Name, Version: *svc.TemplateVersion, LatestVersion: t.LatestVersion, Archived: t.ArchivedAt != nil}
}

// FieldChange é um campo que o modelo mudou entre a versão do serviço e a
// versão alvo. Overrides = o serviço já tinha mudado este campo à mão (o
// valor atual difere do da versão de origem) e a atualização vai substituir
// essa alteração.
type FieldChange struct {
	Field     string          `json:"field"`
	From      json.RawMessage `json:"from"`
	To        json.RawMessage `json:"to"`
	Current   json.RawMessage `json:"current"`
	Overrides bool            `json:"overrides"`
}

type TemplateUpdate struct {
	Template    string        `json:"template"`
	FromVersion int           `json:"fromVersion"`
	ToVersion   int           `json:"toVersion"`
	Changes     []FieldChange `json:"changes"`
	// Notes das versões entre a de origem (exclusive) e a alvo (inclusive).
	Notes []VersionNote `json:"notes"`
}

type VersionNote struct {
	Version int    `json:"version"`
	Notes   string `json:"notes"`
}

var errNoTemplate = errors.New("este serviço não foi criado a partir de um modelo")

func (s *Service) templateUpdate(ctx context.Context, svc store.Service, to int) (TemplateUpdate, error) {
	if svc.TemplateID == nil || svc.TemplateVersion == nil {
		return TemplateUpdate{}, invalid("%v", errNoTemplate)
	}
	t, err := s.db.GetTemplate(ctx, *svc.TemplateID)
	if err != nil {
		return TemplateUpdate{}, mapStoreErr(err)
	}
	if to == 0 {
		to = t.LatestVersion
	}
	from := *svc.TemplateVersion
	if to <= from {
		return TemplateUpdate{}, invalid("o serviço já está na v%d de %s", from, t.Slug)
	}
	fromV, err := s.db.GetTemplateVersion(ctx, t.ID, from)
	if err != nil {
		return TemplateUpdate{}, mapStoreErr(err)
	}
	toV, err := s.db.GetTemplateVersion(ctx, t.ID, to)
	if err != nil {
		return TemplateUpdate{}, mapStoreErr(err)
	}
	fromView, err := versionView(fromV)
	if err != nil {
		return TemplateUpdate{}, err
	}
	toView, err := versionView(toV)
	if err != nil {
		return TemplateUpdate{}, err
	}

	base, target, current := fromView.Spec.asFields(), toView.Spec.asFields(), specFromService(svc).asFields()
	out := TemplateUpdate{Template: t.Slug, FromVersion: from, ToVersion: to, Changes: []FieldChange{}, Notes: []VersionNote{}}
	for field, want := range target {
		if bytes.Equal(base[field], want) {
			continue
		}
		out.Changes = append(out.Changes, FieldChange{
			Field: field, From: base[field], To: want, Current: current[field],
			Overrides: !bytes.Equal(current[field], base[field]) && !bytes.Equal(current[field], want),
		})
	}
	sortChanges(out.Changes)
	versions, err := s.db.ListTemplateVersions(ctx, t.ID)
	if err != nil {
		return TemplateUpdate{}, err
	}
	for i := len(versions) - 1; i >= 0; i-- {
		if v := versions[i]; v.Version > from && v.Version <= to && v.Notes != "" {
			out.Notes = append(out.Notes, VersionNote{Version: v.Version, Notes: v.Notes})
		}
	}
	return out, nil
}

// fieldOrder é a ordem em que os campos aparecem nas diferenças (a mesma
// do formulário do serviço).
var fieldOrder = []string{"kind", "port", "healthPath", "healthTimeoutSeconds", "vcpu", "memoryMb", "metricsPort", "metricsPath",
	"cmd", "releaseCmd", "env", "scalingMode", "replicas", "minReplicas", "maxReplicas", "cpuScaleUpPercent", "cpuScaleDownPercent"}

func sortChanges(c []FieldChange) {
	rank := map[string]int{}
	for i, f := range fieldOrder {
		rank[f] = i
	}
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && rank[c[j].Field] < rank[c[j-1].Field]; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

func (s *Service) PreviewTemplateUpdate(ctx context.Context, projectSlug, envSlug, svcSlug string, to int) (TemplateUpdate, error) {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return TemplateUpdate{}, err
	}
	return s.templateUpdate(ctx, svc, to)
}

// ApplyTemplateUpdate aplica só os campos que o modelo mudou, e passa o
// serviço para a versão alvo. Como qualquer edição, o scaling entra já e o
// resto no próximo deploy; a imagem não é tocada.
func (s *Service) ApplyTemplateUpdate(ctx context.Context, projectSlug, envSlug, svcSlug string, to int) (ServiceView, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return ServiceView{}, err
	}
	upd, err := s.templateUpdate(ctx, svc, to)
	if err != nil {
		return ServiceView{}, err
	}
	current := specFromService(svc)
	fields := current.asFields()
	for _, c := range upd.Changes {
		fields[c.Field] = c.To
	}
	merged, _ := json.Marshal(fields)
	var next TemplateSpec
	if err := json.Unmarshal(merged, &next); err != nil {
		return ServiceView{}, err
	}
	next.applyTo(&svc)
	if err := validateService(svc); err != nil {
		return ServiceView{}, err
	}
	if err := s.db.UpdateService(ctx, svc); err != nil {
		return ServiceView{}, err
	}
	if err := s.db.SetServiceTemplate(ctx, svc.ID, *svc.TemplateID, upd.ToVersion); err != nil {
		return ServiceView{}, err
	}
	s.rt.forgetDesired(svc.ID)
	s.Kick()
	updated, err := s.db.GetService(ctx, svc.ID)
	if err != nil {
		return ServiceView{}, err
	}
	return s.serviceView(ctx, p, e, updated), nil
}

type SaveAsTemplateInput struct {
	// Template: slug de um modelo existente (cria uma versão nova) ou de um
	// novo (cria o modelo, v1).
	Template string `json:"template"`
	Name     string `json:"name"`
	Notes    string `json:"notes"`
}

// SaveServiceAsTemplate guarda a configuração atual do serviço como modelo
// (novo, ou versão nova de um existente), com a imagem que está a correr.
func (s *Service) SaveServiceAsTemplate(ctx context.Context, projectSlug, envSlug, svcSlug string, in SaveAsTemplateInput, by string) (TemplateDetail, error) {
	p, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return TemplateDetail{}, err
	}
	if svc.CurrentRevisionID == nil {
		return TemplateDetail{}, invalid("o serviço ainda não tem nenhum deploy bem-sucedido -- não há imagem para o modelo")
	}
	rev, err := s.db.GetRevision(ctx, *svc.CurrentRevisionID)
	if err != nil {
		return TemplateDetail{}, err
	}
	version := TemplateVersionInput{Image: rev.Image, Notes: in.Notes, Spec: specFromService(svc)}
	existing, err := s.db.GetTemplateBySlug(ctx, p.ID, in.Template)
	switch {
	case errors.Is(err, store.ErrTemplateNotFound):
		detail, err := s.CreateTemplate(ctx, projectSlug, CreateTemplateInput{Slug: in.Template, Name: in.Name, TemplateVersionInput: version}, by)
		if err != nil {
			return TemplateDetail{}, err
		}
		if err := s.db.SetServiceTemplate(ctx, svc.ID, detail.ID, 1); err != nil {
			return TemplateDetail{}, err
		}
		return detail, nil
	case err != nil:
		return TemplateDetail{}, err
	}
	v, err := s.AddTemplateVersion(ctx, projectSlug, existing.Slug, version, by)
	if err != nil {
		return TemplateDetail{}, err
	}
	if err := s.db.SetServiceTemplate(ctx, svc.ID, existing.ID, v.Version); err != nil {
		return TemplateDetail{}, err
	}
	return s.GetTemplate(ctx, projectSlug, existing.Slug)
}
