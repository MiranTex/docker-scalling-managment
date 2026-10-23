package platform

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"launcher/internal/dockerclient"
	"launcher/internal/secretbox"
	"launcher/internal/store/storetest"
)

func newTestService(t *testing.T) (*Service, *fakeDocker) {
	t.Helper()
	db := storetest.New(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, err := secretbox.New(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	docker := newFakeDocker()
	return New(db, docker, box, Config{TraefikContainer: "traefik", SelfContainer: "launcher", PrometheusContainer: "prometheus"}, nil), docker
}

func TestProjectAndEnvironmentLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)

	if _, err := svc.CreateProject(ctx, CreateProjectInput{Slug: "loja", Name: "Loja"}, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(ctx, CreateProjectInput{Slug: "loja"}, "tester"); !errors.Is(err, ErrConflict) {
		t.Fatalf("projeto repetido: err = %v, queria ErrConflict", err)
	}
	if _, err := svc.CreateProject(ctx, CreateProjectInput{Slug: "Loja_X"}, "tester"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("slug inválido: err = %v, queria ErrInvalid", err)
	}

	env, err := svc.CreateEnvironment(ctx, "loja", "production", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if env.Network != "bs-loja-production" {
		t.Errorf("rede = %q", env.Network)
	}
	labels := docker.networks["bs-loja-production"]
	if labels[LabelProject] != "loja" || labels[LabelEnvironmentID] != env.ID || labels[dockerclient.PlatformLabel] != "true" {
		t.Errorf("labels da rede = %v", labels)
	}
	if got := docker.connected["bs-loja-production"]; strings.Join(got, ",") != "traefik,launcher,prometheus" {
		t.Errorf("containers ligados à rede = %v, queria traefik, launcher e prometheus", got)
	}
	if _, err := svc.CreateEnvironment(ctx, "loja", "production", "tester"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiente repetido: err = %v", err)
	}

	// Um container do ambiente tem de desaparecer com ele, e um de outro
	// ambiente não.
	docker.containers["app-1"] = dockerclient.Container{ID: "app-1", Names: []string{"/app-1"}, Labels: map[string]string{LabelEnvironmentID: env.ID}}
	docker.containers["other"] = dockerclient.Container{ID: "other", Names: []string{"/other"}, Labels: map[string]string{LabelEnvironmentID: "outro"}}

	if err := svc.DeleteProject(ctx, "loja", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := docker.containers["app-1"]; ok {
		t.Error("container do ambiente devia ter sido removido")
	}
	if _, ok := docker.containers["other"]; !ok {
		t.Error("container de outro ambiente não devia ter sido tocado")
	}
	if _, ok := docker.networks["bs-loja-production"]; ok {
		t.Error("rede do ambiente devia ter sido removida")
	}
	if _, err := svc.GetProject(ctx, "loja"); !errors.Is(err, ErrNotFound) {
		t.Errorf("projeto apagado: err = %v", err)
	}
}

func TestSecretsResolveAndWriteOnly(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	if _, err := svc.CreateProject(ctx, CreateProjectInput{Slug: "loja"}, "t"); err != nil {
		t.Fatal(err)
	}
	env, err := svc.CreateEnvironment(ctx, "loja", "staging", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.PutSecret(ctx, "loja", "staging", "DB_PASSWORD", "hunter2", "t"); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutSecret(ctx, "loja", "staging", "bad name", "x", "t"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nome inválido: err = %v", err)
	}

	got, err := svc.ResolveEnv(ctx, env.ID, []string{"PLAIN=1", "URL=postgres://app:${secret:DB_PASSWORD}@db/app"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "PLAIN=1" || got[1] != "URL=postgres://app:hunter2@db/app" {
		t.Errorf("ResolveEnv = %v", got)
	}
	if _, err := svc.ResolveEnv(ctx, env.ID, []string{"X=${secret:NOPE}"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("segredo em falta: err = %v, queria ErrInvalid", err)
	}

	// Gerido pela plataforma = intocável pela API.
	if err := svc.putSecret(ctx, env.ID, "DATABASE_URL", "postgres://x", "addon:db", "platform"); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutSecret(ctx, "loja", "staging", "DATABASE_URL", "hack", "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("alterar segredo gerido: err = %v", err)
	}
	if err := svc.DeleteSecret(ctx, "loja", "staging", "DATABASE_URL"); !errors.Is(err, ErrInvalid) {
		t.Errorf("apagar segredo gerido: err = %v", err)
	}

	list, err := svc.ListSecrets(ctx, "loja", "staging")
	if err != nil || len(list) != 2 {
		t.Fatalf("ListSecrets = %v, %v", list, err)
	}
}
