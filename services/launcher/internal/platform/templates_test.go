package platform

import (
	"context"
	"errors"
	"testing"

	"launcher/internal/store"
)

func webSpec() TemplateSpec {
	return TemplateSpec{
		Kind: "web", Port: 80, HealthPath: "/", HealthTimeoutSeconds: 60,
		Env: []string{"APP_ENV=production"}, VCPU: 0.5, MemoryMB: 512,
		ScalingMode: "fixed", Replicas: 1, MinReplicas: 1, MaxReplicas: 3, CPUScaleUpPercent: 70, CPUScaleDownPercent: 20,
	}
}

func TestTemplateValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	cases := map[string]CreateTemplateInput{
		"sem imagem":     {Slug: "web", TemplateVersionInput: TemplateVersionInput{Spec: webSpec()}},
		"imagem sem tag": {Slug: "web", TemplateVersionInput: TemplateVersionInput{Image: "ghcr.io/eu/app", Spec: webSpec()}},
		"web sem porta": {Slug: "web", TemplateVersionInput: TemplateVersionInput{Image: "app:1", Spec: func() TemplateSpec {
			s := webSpec()
			s.Port = 0
			return s
		}()}},
		"slug inválido": {Slug: "X", TemplateVersionInput: TemplateVersionInput{Image: "app:1", Spec: webSpec()}},
	}
	for name, in := range cases {
		if _, err := svc.CreateTemplate(ctx, "loja", in, "t"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, queria ErrInvalid", name, err)
		}
	}
	// Uma porta de registry não é uma tag; localhost/x:dev e digests são.
	for _, img := range []string{"localhost:5000/app:1", "localhost/gymapp:dev", "app@sha256:abc"} {
		if err := validTemplateImage(img); err != nil {
			t.Errorf("%s devia ser aceite: %v", img, err)
		}
	}
	if err := validTemplateImage("localhost:5000/app"); !errors.Is(err, ErrInvalid) {
		t.Errorf("porta de registry confundida com tag: %v", err)
	}
}

func TestServiceFromTemplateAndUpdate(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)

	if _, err := svc.CreateTemplate(ctx, "loja", CreateTemplateInput{
		Slug: "laravel-web", TemplateVersionInput: TemplateVersionInput{Image: "app:1", Spec: webSpec(), Notes: "primeira"},
	}, "t"); err != nil {
		t.Fatal(err)
	}

	// Criar a partir do modelo: copia a configuração e faz o primeiro deploy.
	view, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Template: "laravel-web"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if view.Template == nil || view.Template.Version != 1 || view.Port != 80 || view.Env[0] != "APP_ENV=production" {
		t.Fatalf("serviço criado = %+v (modelo %+v)", view.Service, view.Template)
	}
	deps, _ := svc.ListDeployments(ctx, "loja", "prod", "web")
	if len(deps) != 1 {
		t.Fatalf("devia ter feito o primeiro deploy: %+v", deps)
	}
	if d := waitDeploy(t, svc, "web", deps[0].ID); d.Status != store.DeploySucceeded || d.Image != "app:1" {
		t.Fatalf("primeiro deploy = %s %s: %s", d.Status, d.Image, d.Error)
	}
	// O CI faz deploy de uma versão nova da app.
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:2"}, "ci")
	waitDeploy(t, svc, "web", dep.ID)

	// Alterações locais: uma num campo que o modelo vai mudar (memória), e
	// outra num campo que o modelo não mexe (réplicas).
	if _, err := svc.UpdateService(ctx, "loja", "prod", "web", ServiceInput{MemoryMB: func() *int64 { v := int64(768); return &v }(), Replicas: intp(2)}); err != nil {
		t.Fatal(err)
	}

	// v2 do modelo: mais memória e uma variável nova.
	v2 := webSpec()
	v2.MemoryMB = 1024
	v2.Env = append(v2.Env, "LOG_CHANNEL=stderr")
	if _, err := svc.AddTemplateVersion(ctx, "loja", "laravel-web", TemplateVersionInput{Image: "app:9", Spec: v2, Notes: "mais memória"}, "t"); err != nil {
		t.Fatal(err)
	}
	// O tipo não pode mudar entre versões.
	worker := webSpec()
	worker.Kind, worker.Port, worker.HealthPath = "worker", 0, ""
	if _, err := svc.AddTemplateVersion(ctx, "loja", "laravel-web", TemplateVersionInput{Image: "app:9", Spec: worker}, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("mudar o tipo: err = %v", err)
	}

	view, _ = svc.GetService(ctx, "loja", "prod", "web")
	if view.Template.Version != 1 || view.Template.LatestVersion != 2 {
		t.Errorf("ref do modelo = %+v", view.Template)
	}

	upd, err := svc.PreviewTemplateUpdate(ctx, "loja", "prod", "web", 0)
	if err != nil {
		t.Fatal(err)
	}
	changed := map[string]FieldChange{}
	for _, c := range upd.Changes {
		changed[c.Field] = c
	}
	if len(changed) != 2 || !changed["memoryMb"].Overrides || changed["env"].Overrides {
		t.Fatalf("diferenças = %+v", upd.Changes)
	}
	if len(upd.Notes) != 1 || upd.Notes[0].Notes != "mais memória" {
		t.Errorf("notas = %+v", upd.Notes)
	}

	view, err = svc.ApplyTemplateUpdate(ctx, "loja", "prod", "web", 0)
	if err != nil {
		t.Fatal(err)
	}
	if view.MemoryMB != 1024 || len(view.Env) != 2 || view.Replicas != 2 || view.Template.Version != 2 {
		t.Errorf("depois de aplicar: memória %d, env %v, réplicas %d (a local tem de ficar), versão %d",
			view.MemoryMB, view.Env, view.Replicas, view.Template.Version)
	}
	// A imagem que corre não muda: continua a do CI, não a do modelo.
	if view.CurrentRevision == nil || view.CurrentRevision.Image != "app:2" {
		t.Errorf("a atualização do modelo trocou a imagem: %+v", view.CurrentRevision)
	}
	for _, c := range docker.byRole(RoleApp) {
		if c.Image == "app:9" {
			t.Error("nenhuma réplica pode estar com a imagem do modelo")
		}
	}
	if _, err := svc.PreviewTemplateUpdate(ctx, "loja", "prod", "web", 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("já na versão mais recente: err = %v", err)
	}
}

func TestSaveServiceAsTemplate(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(8080)}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveServiceAsTemplate(ctx, "loja", "prod", "web", SaveAsTemplateInput{Template: "base"}, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("sem deploy não há imagem: err = %v", err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:5"}, "t")
	waitDeploy(t, svc, "web", dep.ID)

	detail, err := svc.SaveServiceAsTemplate(ctx, "loja", "prod", "web", SaveAsTemplateInput{Template: "base", Name: "Base web"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if detail.LatestVersion != 1 || detail.Versions[0].Image != "app:5" || detail.Versions[0].Spec.Port != 8080 {
		t.Errorf("modelo novo = %+v", detail)
	}
	// Guardar outra vez no mesmo modelo cria a v2, e o serviço passa a ela.
	if _, err := svc.UpdateService(ctx, "loja", "prod", "web", ServiceInput{Replicas: intp(3)}); err != nil {
		t.Fatal(err)
	}
	detail, err = svc.SaveServiceAsTemplate(ctx, "loja", "prod", "web", SaveAsTemplateInput{Template: "base", Notes: "3 réplicas"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	view, _ := svc.GetService(ctx, "loja", "prod", "web")
	if detail.LatestVersion != 2 || detail.Versions[0].Spec.Replicas != 3 || view.Template.Version != 2 {
		t.Errorf("v2 = %+v, serviço em %+v", detail.Versions[0], view.Template)
	}
	list, _ := svc.ListTemplates(ctx, "loja")
	if len(list) != 1 || list[0].UsedBy != 1 {
		t.Errorf("lista = %+v", list)
	}

	if err := svc.ArchiveTemplate(ctx, "loja", "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web2", Template: "base"}, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("criar a partir de modelo arquivado: err = %v", err)
	}
}
