package platform

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"launcher/internal/store"
)

// filesS3 guarda objetos em memória (com o tipo) e lista por pastas
// (delimiter "/"). Os pedidos de gestão (policy, cors) só respondem bem.
type filesS3 struct {
	mu    sync.Mutex
	objs  map[string]string // "bucket/chave" -> conteúdo
	types map[string]string
}

func (f *filesS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(p, "/")
	q := r.URL.Query()
	switch {
	case q.Has("policy") || q.Has("cors") || key == "" && r.Method == http.MethodPut:
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.objs[p], f.types[p] = string(body), r.Header.Get("Content-Type")
	case r.Method == http.MethodDelete:
		delete(f.objs, p)
		w.WriteHeader(http.StatusNoContent)
	case q.Get("list-type") == "2":
		prefix, delim := q.Get("prefix"), q.Get("delimiter")
		type content struct {
			Key  string
			Size int64
		}
		type cp struct{ Prefix string }
		var out struct {
			XMLName        xml.Name `xml:"ListBucketResult"`
			Contents       []content
			CommonPrefixes []cp
		}
		seen := map[string]bool{}
		var keys []string
		for k := range f.objs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b, name, _ := strings.Cut(k, "/")
			if b != bucket || !strings.HasPrefix(name, prefix) {
				continue
			}
			rest := strings.TrimPrefix(name, prefix)
			if i := strings.Index(rest, delim); delim != "" && i >= 0 {
				if d := prefix + rest[:i+1]; !seen[d] {
					seen[d] = true
					out.CommonPrefixes = append(out.CommonPrefixes, cp{d})
				}
				continue
			}
			out.Contents = append(out.Contents, content{name, int64(len(f.objs[k]))})
		}
		_ = xml.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet:
		body, ok := f.objs[p]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
			return
		}
		w.Header().Set("Content-Type", f.types[p])
		_, _ = io.WriteString(w, body)
	}
}

func TestStorageFiles(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	svc.cfg.StorageImage = "chrislusf/seaweedfs:4.50"
	s3 := &filesS3{objs: map[string]string{}, types: map[string]string{}}
	srv := httptest.NewServer(s3)
	t.Cleanup(srv.Close)
	svc.storageURL = func(store.Project, store.Environment, store.Addon) string { return srv.URL }
	if _, err := svc.CreateAddon(ctx, "loja", "prod", CreateAddonInput{Slug: "files", Kind: "storage"}, "t"); err != nil {
		t.Fatal(err)
	}
	upload := func(bucket, key, body, typ string) error {
		return svc.UploadFile(ctx, "loja", "prod", "files", bucket, key, strings.NewReader(body), int64(len(body)), typ)
	}
	for key, body := range map[string]string{"fotos/2026/a.jpg": "jpg", "fotos/b.png": "png", "raiz.txt": "texto"} {
		if err := upload("app", key, body, "image/jpeg"); err != nil {
			t.Fatal(err)
		}
	}

	root, err := svc.ListFiles(ctx, "loja", "prod", "files", "app", "")
	if err != nil || len(root.Folders) != 1 || root.Folders[0] != "fotos" || len(root.Files) != 1 || root.Files[0].Name != "raiz.txt" {
		t.Fatalf("raiz = %+v, %v", root, err)
	}
	fotos, _ := svc.ListFiles(ctx, "loja", "prod", "files", "app", "fotos/")
	if len(fotos.Folders) != 1 || fotos.Folders[0] != "2026" || len(fotos.Files) != 1 || fotos.Files[0].Key != "fotos/b.png" {
		t.Errorf("fotos/ = %+v", fotos)
	}

	body, size, typ, err := svc.OpenFile(ctx, "loja", "prod", "files", "app", "fotos/2026/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(body)
	body.Close()
	if string(got) != "jpg" || size != 3 || typ != "image/jpeg" {
		t.Errorf("download = %q %d %q", got, size, typ)
	}
	if _, _, _, err := svc.OpenFile(ctx, "loja", "prod", "files", "app", "nao-existe.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ficheiro inexistente: err = %v", err)
	}

	for _, bad := range []string{"../x", "/a", "a//b", "a/", "a/./b", "a\\b", ""} {
		if err := upload("app", bad, "x", ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("nome %q aceite: %v", bad, err)
		}
	}
	if err := svc.UploadFile(ctx, "loja", "prod", "files", "app", "grande.bin", strings.NewReader(""), MaxUploadBytes+1, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("mais de 1 GB: err = %v", err)
	}
	if _, err := svc.ListFiles(ctx, "loja", "prod", "files", "app", "fotos"); !errors.Is(err, ErrInvalid) {
		t.Errorf("pasta sem / final: err = %v", err)
	}

	// Links: sem endereço público não há; num privado é assinado, num
	// público é direto -- pelo domínio próprio, se houver.
	if _, err := svc.FileLink(ctx, "loja", "prod", "files", "app", "raiz.txt"); !errors.Is(err, ErrInvalid) {
		t.Errorf("link sem endereço público: err = %v", err)
	}
	if _, err := svc.SetStoragePublic(ctx, "loja", "prod", "files", true, "t"); err != nil {
		t.Fatal(err)
	}
	link, err := svc.FileLink(ctx, "loja", "prod", "files", "app", "fotos/2026/a.jpg")
	if err != nil || !strings.HasPrefix(link, "http://files-prod-loja.example.test/app/fotos/2026/a.jpg?") || !strings.Contains(link, "X-Amz-Signature=") {
		t.Errorf("link privado = %q, %v", link, err)
	}
	if _, err := svc.CreateBucket(ctx, "loja", "prod", "files", BucketInput{Name: "media", Public: true}, "t"); err != nil {
		t.Fatal(err)
	}
	if err := upload("media", "logo +1.png", "png", "image/png"); err != nil {
		t.Fatal(err)
	}
	if link, _ := svc.FileLink(ctx, "loja", "prod", "files", "media", "logo +1.png"); link != "http://files-prod-loja.example.test/media/logo%20%2B1.png" {
		t.Errorf("link público = %q", link)
	}
	if _, err := svc.AddBucketDomain(ctx, "loja", "prod", "files", "media", "cdn.loja.pt", "t"); err != nil {
		t.Fatal(err)
	}
	if link, _ := svc.FileLink(ctx, "loja", "prod", "files", "media", "logo +1.png"); link != "http://cdn.loja.pt/logo%20%2B1.png" {
		t.Errorf("link pelo domínio = %q", link)
	}

	if err := svc.DeleteFile(ctx, "loja", "prod", "files", "app", "raiz.txt"); err != nil {
		t.Fatal(err)
	}
	if root, _ := svc.ListFiles(ctx, "loja", "prod", "files", "app", ""); len(root.Files) != 0 {
		t.Errorf("o ficheiro apagado continua lá: %+v", root.Files)
	}
}
