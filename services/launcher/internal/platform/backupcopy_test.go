package platform

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"launcher/internal/objstore"
	"launcher/internal/store"
)

// fakeStorage guarda objetos em memória e fala o bastante da API do S3
// (PUT, listagem v2, DELETE) para o objstore.
type fakeStorage struct {
	mu      sync.Mutex
	objects map[string]string // "bucket/chave" -> conteúdo
}

func (f *fakeStorage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.objects[path] = string(body)
	case r.Method == http.MethodDelete:
		delete(f.objects, path)
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		type content struct {
			Key  string
			Size int64
		}
		var out struct {
			XMLName  xml.Name `xml:"ListBucketResult"`
			Contents []content
		}
		prefix := path + "/" + r.URL.Query().Get("prefix")
		var keys []string
		for k := range f.objects {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Contents = append(out.Contents, content{Key: strings.TrimPrefix(k, path+"/"), Size: int64(len(f.objects[k]))})
		}
		_ = xml.NewEncoder(w).Encode(out)
	default:
		http.NotFound(w, r)
	}
}

func TestBackupIsCopiedToPlatformStorage(t *testing.T) {
	ctx := context.Background()
	svc, docker := newTestService(t)
	svc.cfg.PostgresImage = "postgres:17-alpine"
	storage := &fakeStorage{objects: map[string]string{}}
	srv := httptest.NewServer(storage)
	t.Cleanup(srv.Close)
	client, err := objstore.New(srv.URL, "us-east-1", "k", "s", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.cfg.Backups = client
	setupEnv(t, svc)
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "db", Kind: "postgres"}, "t"); err != nil {
		t.Fatal(err)
	}
	// 31 cópias antigas: depois do backup novo ficam as 30 mais recentes.
	for i := 0; i < 31; i++ {
		storage.objects["addon-backups/loja/prod/db/2025010"+string(rune('0'+i/10))+"T0000"+string(rune('0'+i%10))+"0Z.dump"] = "velho"
	}

	job, err := svc.BackupAddon(ctx, "loja", "prod", "db", "t")
	if err != nil {
		t.Fatal(err)
	}
	final := waitJob(t, svc, job.ID)
	if final.Status != store.JobSucceeded || !strings.Contains(final.Output, "cópia no storage da plataforma: addon-backups/loja/prod/db/") {
		t.Fatalf("job = %+v", final)
	}
	var file string
	for _, e := range docker.lastJobEnv {
		if v, ok := strings.CutPrefix(e, "BACKUP_FILE="); ok {
			file = v
		}
	}
	got, ok := storage.objects["addon-backups/loja/prod/db/"+file]
	if file == "" || !ok || got != "/backups/"+file {
		t.Errorf("cópia de %q = %q (%v); objetos: %d", file, got, ok, len(storage.objects))
	}
	if len(storage.objects) != 30 {
		t.Errorf("ficaram %d cópias, queria 30", len(storage.objects))
	}
	if _, ok := storage.objects["addon-backups/loja/prod/db/20250100T00000Z.dump"]; ok {
		t.Error("a cópia mais antiga devia ter sido apagada")
	}
	for _, c := range docker.containers {
		if strings.Contains(c.Names[0], "-copy-") {
			t.Errorf("container de cópia ficou: %s", c.Names[0])
		}
	}

	// Sem storage a responder, o job falha -- o dump está só no volume.
	srv.Close()
	job, _ = svc.BackupAddon(ctx, "loja", "prod", "db", "t")
	if final := waitJob(t, svc, job.ID); final.Status != store.JobFailed || !strings.Contains(final.Output, "o dump ficou no volume") {
		t.Errorf("job sem storage = %+v", final)
	}
}
