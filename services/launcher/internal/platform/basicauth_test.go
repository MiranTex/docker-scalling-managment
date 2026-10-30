package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestBasicAuthProtectsAllRouters(t *testing.T) {
	ctx := context.Background()
	svc, _, _, dir := newDeployService(t)
	svc.cfg.TLSCertResolver = "le"
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "jobs", Kind: "worker"}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)

	routing := func() traefikConfig {
		var cfg traefikConfig
		b, _ := os.ReadFile(filepath.Join(dir, routingFile))
		if err := json.Unmarshal(b, &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	const name = "bs_loja_prod_web"
	if _, ok := routing().HTTP.Middlewares[name+"-auth"]; ok {
		t.Fatal("sem proteção não devia haver middleware de auth")
	}

	for _, bad := range []struct{ user, pw string }{{"", "segredo123"}, {"a:b", "segredo123"}, {"cliente", "curta"}} {
		if err := svc.SetBasicAuth(ctx, "loja", "prod", "web", bad.user, bad.pw); !errors.Is(err, ErrInvalid) {
			t.Errorf("SetBasicAuth(%q, %q) = %v", bad.user, bad.pw, err)
		}
	}
	if err := svc.SetBasicAuth(ctx, "loja", "prod", "jobs", "cliente", "segredo123"); !errors.Is(err, ErrInvalid) {
		t.Errorf("worker protegido: err = %v", err)
	}

	if err := svc.SetBasicAuth(ctx, "loja", "prod", "web", "cliente", "segredo123"); err != nil {
		t.Fatal(err)
	}
	cfg := routing()
	mw, ok := cfg.HTTP.Middlewares[name+"-auth"]
	if !ok || mw.BasicAuth == nil || len(mw.BasicAuth.Users) != 1 || !mw.BasicAuth.RemoveHeader {
		t.Fatalf("middleware de auth = %+v", mw)
	}
	user, hash, _ := strings.Cut(mw.BasicAuth.Users[0], ":")
	if user != "cliente" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("segredo123")) != nil {
		t.Errorf("users = %v", mw.BasicAuth.Users)
	}
	// O router HTTPS pede a password; o HTTP só redireciona.
	if got := cfg.HTTP.Routers[name+"-tls"].Middlewares; strings.Join(got, ",") != "bs-hsts,"+name+"-auth,bs-retry" {
		t.Errorf("middlewares do router tls = %v", got)
	}
	view, _ := svc.GetService(ctx, "loja", "prod", "web")
	if view.BasicAuthUser != "cliente" {
		t.Errorf("view.BasicAuthUser = %q", view.BasicAuthUser)
	}
	if b, _ := json.Marshal(view); strings.Contains(string(b), hash) {
		t.Error("o hash não pode sair na API")
	}

	if err := svc.ClearBasicAuth(ctx, "loja", "prod", "web"); err != nil {
		t.Fatal(err)
	}
	cfg = routing()
	if _, ok := cfg.HTTP.Middlewares[name+"-auth"]; ok {
		t.Error("proteção desligada ficou no routing")
	}
	if got := cfg.HTTP.Routers[name+"-tls"].Middlewares; strings.Join(got, ",") != "bs-hsts,bs-retry" {
		t.Errorf("middlewares sem proteção = %v", got)
	}
}
