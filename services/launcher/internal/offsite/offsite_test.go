package offsite

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"launcher/internal/objstore"
)

// memS3 é um S3 em memória (PUT, GET, DELETE, listagem v2) com um relógio
// que avança um segundo por escrita -- as datas ordenam as versões.
type memS3 struct {
	mu    sync.Mutex
	clock time.Time
	objs  map[string]memObj // "bucket/chave"
}

type memObj struct {
	data string
	mod  time.Time
}

func newMemS3() *memS3 {
	return &memS3{clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), objs: map[string]memObj{}}
}

func (s *memS3) put(path, data string) {
	s.clock = s.clock.Add(time.Second)
	s.objs[path] = memObj{data, s.clock}
}

func (s *memS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		s.put(path, string(body))
	case r.Method == http.MethodDelete:
		delete(s.objs, path)
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		type content struct {
			Key          string
			Size         int64
			LastModified time.Time
		}
		var out struct {
			XMLName  xml.Name `xml:"ListBucketResult"`
			Contents []content
		}
		prefix := path + "/" + r.URL.Query().Get("prefix")
		var keys []string
		for k := range s.objs {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Contents = append(out.Contents, content{strings.TrimPrefix(k, path+"/"), int64(len(s.objs[k].data)), s.objs[k].mod})
		}
		_ = xml.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet:
		o, ok := s.objs[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "<Error><Code>NoSuchKey</Code></Error>")
			return
		}
		fmt.Fprint(w, o.data)
	}
}

func client(t *testing.T, h http.Handler) *objstore.Client {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := objstore.New(srv.URL, "us-east-1", "k", "s", "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func decrypt(t *testing.T, id *age.X25519Identity, data string) string {
	t.Helper()
	r, err := age.Decrypt(strings.NewReader(data), id)
	if err != nil {
		t.Fatalf("o objeto no destino não se decifra: %v", err)
	}
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestMirror(t *testing.T) {
	ctx := context.Background()
	local, remote := newMemS3(), newMemS3()
	id, _ := age.GenerateX25519Identity()
	m := &Mirror{
		Local:     client(t, local),
		Target:    Target{Store: client(t, remote), Bucket: "fora", Prefix: "basestack/"},
		Recipient: id.Recipient(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		TempDir:   t.TempDir(),
	}
	// O relógio do destino está atrasado uma hora: não pode contar.
	remote.clock = local.clock.Add(-time.Hour)
	for i := 0; i < 10; i++ {
		local.put(fmt.Sprintf("database/archive/%02d", i), fmt.Sprintf("wal %d", i))
	}
	local.put("config/1.tar.age", "cfg")
	local.put("addon-backups/loja/prod/db/1.dump", "dump")

	r, err := m.Once(ctx)
	if err != nil || r.Copied != 12 {
		t.Fatalf("primeira passagem: %+v, %v", r, err)
	}
	if got := decrypt(t, id, remote.objs["fora/basestack/database/archive/03.age"].data); got != "wal 3" {
		t.Errorf("conteúdo decifrado = %q", got)
	}
	if strings.Contains(remote.objs["fora/basestack/addon-backups/loja/prod/db/1.dump.age"].data, "dump") {
		t.Error("o dump foi para o destino em claro")
	}
	if _, ok := remote.objs["fora/basestack/"+StatusKey]; !ok {
		t.Error("sem marca da última passagem")
	}

	// Nada mudou: nada a copiar.
	if r, _ := m.Once(ctx); r.Copied != 0 || r.Deleted != 0 {
		t.Errorf("segunda passagem sem mudanças: %+v", r)
	}
	// O manifesto perdido só faz copiar tudo outra vez.
	delete(remote.objs, "fora/basestack/"+ManifestKey)
	if r, _ := m.Once(ctx); r.Copied != 12 {
		t.Errorf("sem manifesto: %+v", r)
	}

	// Uma versão nova (mais recente cá do que lá) volta a ir; uma
	// rotação pequena apaga lá.
	local.put("database/archive/00", "wal 0 v2")
	delete(local.objs, "database/archive/09")
	if r, err := m.Once(ctx); err != nil || r.Copied != 1 || r.Deleted != 1 {
		t.Errorf("versão nova + rotação: %+v, %v", r, err)
	}
	if got := decrypt(t, id, remote.objs["fora/basestack/database/archive/00.age"].data); got != "wal 0 v2" {
		t.Errorf("versão nova no destino = %q", got)
	}
	if _, ok := remote.objs["fora/basestack/database/archive/09.age"]; ok {
		t.Error("o objeto rodado cá ficou lá")
	}

	st, err := m.Status(ctx)
	if err != nil || st.Missing["database"] != 0 || st.Remote["database"] != 9 || st.LastSync.IsZero() {
		t.Errorf("status = %+v, %v", st, err)
	}
	local.put("database/archive/10", "wal 10")
	if st, _ := m.Status(ctx); st.Missing["database"] != 1 {
		t.Errorf("status com um objeto novo = %+v", st)
	}

	// O storage local perde quase tudo de uma vez: o destino fica intacto.
	for k := range local.objs {
		if strings.HasPrefix(k, "database/") && k != "database/archive/10" {
			delete(local.objs, k)
		}
	}
	delete(local.objs, "config/1.tar.age")
	r, err = m.Once(ctx)
	if err != nil || r.Deleted != 0 || r.Held["database"] != 9 || r.Held["config"] != 1 {
		t.Errorf("perda em massa: %+v, %v", r, err)
	}
	if _, ok := remote.objs["fora/basestack/database/archive/05.age"]; !ok {
		t.Error("a perda local apagou o destino")
	}
}
