package platform

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"launcher/internal/store"
)

// bucketAPI regista os pedidos de gestão de buckets (criar, policy, CORS,
// apagar) que o launcher faz ao storage.
type bucketAPI struct {
	mu       sync.Mutex
	calls    []string          // "PUT /media?policy"
	bodies   map[string]string // o último corpo por chamada
	notEmpty map[string]bool   // buckets que recusam ser apagados
}

func (b *bucketAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	call := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	b.calls = append(b.calls, call)
	body, _ := io.ReadAll(r.Body)
	b.bodies[call] = string(body)
	if r.Method == http.MethodDelete && r.URL.RawQuery == "" && b.notEmpty[strings.TrimPrefix(r.URL.Path, "/")] {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, "<Error><Code>BucketNotEmpty</Code></Error>")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *bucketAPI) called(call string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.calls {
		if c == call {
			return true
		}
	}
	return false
}

func TestStorageBucketsAndPublicAccess(t *testing.T) {
	ctx := context.Background()
	svc, _, _, dir := newDeployService(t)
	svc.cfg.StorageImage = "chrislusf/seaweedfs:4.50"
	api := &bucketAPI{bodies: map[string]string{}, notEmpty: map[string]bool{}}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	svc.storageURL = func(store.Project, store.Environment, store.Addon) string { return srv.URL }
	routing := func() string {
		svc.syncRouting(ctx)
		b, _ := os.ReadFile(filepath.Join(dir, routingFile))
		return string(b)
	}

	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "files", Kind: "storage"}, "t"); err != nil {
		t.Fatal(err)
	}
	if err := svc.docker.StartContainer(ctx, "bs-loja-prod-files"); err != nil {
		t.Fatal(err)
	}
	buckets, err := svc.ListBuckets(ctx, "loja", "prod", "files")
	if err != nil || len(buckets) != 1 || buckets[0].Name != "app" || buckets[0].Public {
		t.Fatalf("buckets de um storage novo = %+v, %v", buckets, err)
	}

	// Um bucket público com CORS: criado, com a policy de leitura e o CORS.
	media, err := svc.CreateBucket(ctx, "loja", "prod", "files", BucketInput{Name: "media", Public: true, CORSOrigins: []string{"https://app.loja.pt/"}}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if !api.called("PUT /media") || !strings.Contains(api.bodies["PUT /media?policy"], `"s3:GetObject"`) ||
		!strings.Contains(api.bodies["PUT /media?cors"], "<AllowedOrigin>https://app.loja.pt</AllowedOrigin>") {
		t.Errorf("pedidos ao storage: %v", api.calls)
	}
	if !media.Public || media.CORSOrigins[0] != "https://app.loja.pt" || media.URL != "" {
		t.Errorf("bucket = %+v", media)
	}
	for _, bad := range []BucketInput{{Name: "Media"}, {Name: "ab"}, {Name: "x_y"}, {Name: "docs", CORSOrigins: []string{"app.loja.pt"}}, {Name: "media"}} {
		if _, err := svc.CreateBucket(ctx, "loja", "prod", "files", bad, "t"); err == nil {
			t.Errorf("bucket %+v aceite", bad)
		}
	}

	// O endereço público: a variável, a vista do addon e o routing.
	view, err := svc.SetStoragePublic(ctx, "loja", "prod", "files", true, "t")
	if err != nil {
		t.Fatal(err)
	}
	if view.PublicURL != "http://files-prod-loja.example.test" || !contains(view.EnvVars, "STORAGE_PUBLIC_URL") {
		t.Errorf("addon com endereço público = %+v", view)
	}
	_, env, _ := svc.Scope(ctx, "loja", "prod")
	if got, _ := svc.containerEnv(ctx, env, store.Revision{}); !contains(got, "STORAGE_PUBLIC_URL=http://files-prod-loja.example.test") {
		t.Errorf("env dos serviços sem STORAGE_PUBLIC_URL: %v", got)
	}
	if r := routing(); !strings.Contains(r, "Host(`files-prod-loja.example.test`)") || !strings.Contains(r, ":8333") {
		t.Errorf("routing sem o storage:\n%s", r)
	}
	if buckets, _ := svc.ListBuckets(ctx, "loja", "prod", "files"); buckets[1].URL != "http://files-prod-loja.example.test/media" {
		t.Errorf("URL do bucket = %q", buckets[1].URL)
	}

	// Domínio próprio: só em buckets públicos, servido com o prefixo.
	if _, err := svc.AddBucketDomain(ctx, "loja", "prod", "files", "app", "privado.loja.pt", "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("domínio num bucket privado: err = %v", err)
	}
	d, err := svc.AddBucketDomain(ctx, "loja", "prod", "files", "media", "cdn.loja.pt", "t")
	if err != nil || d.Status != store.DomainActive || d.ExpectedCNAME != "files-prod-loja.example.test" {
		t.Fatalf("domínio do bucket = %+v, %v", d, err)
	}
	if r := routing(); !strings.Contains(r, "Host(`cdn.loja.pt`)") || !strings.Contains(r, `"prefix": "/media"`) {
		t.Errorf("routing sem o domínio do bucket:\n%s", r)
	}

	// Privado outra vez: sem policy, e o domínio sai do routing.
	if _, err := svc.UpdateBucket(ctx, "loja", "prod", "files", "media", BucketUpdate{Public: new(bool)}); err != nil {
		t.Fatal(err)
	}
	if !api.called("DELETE /media?policy") {
		t.Errorf("a policy não foi tirada: %v", api.calls)
	}
	if strings.Contains(routing(), "cdn.loja.pt") {
		t.Error("um bucket privado ficou com o domínio no routing")
	}

	// Apagar: o app nunca; um bucket com ficheiros também não.
	if err := svc.DeleteBucket(ctx, "loja", "prod", "files", "app"); !errors.Is(err, ErrInvalid) {
		t.Errorf("apagar o app: err = %v", err)
	}
	api.notEmpty["media"] = true
	if err := svc.DeleteBucket(ctx, "loja", "prod", "files", "media"); err == nil || !strings.Contains(err.Error(), "tem ficheiros") {
		t.Errorf("apagar um bucket com ficheiros: err = %v", err)
	}
	api.notEmpty["media"] = false
	if err := svc.DeleteBucket(ctx, "loja", "prod", "files", "media"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SetStoragePublic(ctx, "loja", "prod", "files", false, "t"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.containerEnv(ctx, env, store.Revision{}); strings.Contains(strings.Join(got, " "), "STORAGE_PUBLIC_URL") {
		t.Errorf("STORAGE_PUBLIC_URL ficou depois de desligar: %v", got)
	}
	if strings.Contains(routing(), "files-prod-loja") {
		t.Error("o storage ficou no routing depois de desligar o endereço público")
	}
}
