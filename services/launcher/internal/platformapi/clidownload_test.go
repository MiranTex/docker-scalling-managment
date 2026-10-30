package platformapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func gzTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// cliRegistry serve a imagem basestack/cli:dev: um índice, uma imagem com
// duas camadas (a dos binários desta arquitetura e a /dist), e conta os
// blobs pedidos.
func cliRegistry(t *testing.T, blobs *int32) *httptest.Server {
	t.Helper()
	bin := gzTar(t, map[string]string{"usr/local/bin/basestack": "nativo"})
	dist := gzTar(t, map[string]string{"dist/basestack-darwin-arm64": "binario-mac", "dist/bs-linux-amd64": "bs-linux"})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/basestack/cli/manifests/dev":
			_, _ = w.Write([]byte(`{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[
				{"digest":"sha256:att","platform":{"os":"unknown","architecture":"unknown"}},
				{"digest":"sha256:img","platform":{"os":"linux","architecture":"amd64"}}]}`))
		case "/v2/basestack/cli/manifests/sha256:img":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"config":    map[string]any{"digest": "sha256:cfg" + strings.ReplaceAll(t.Name(), "/", ""), "size": 1},
				"layers":    []map[string]any{{"digest": "sha256:bin"}, {"digest": "sha256:dist"}},
			})
		case "/v2/basestack/cli/blobs/sha256:bin":
			atomic.AddInt32(blobs, 1)
			_, _ = w.Write(bin)
		case "/v2/basestack/cli/blobs/sha256:dist":
			atomic.AddInt32(blobs, 1)
			_, _ = w.Write(dist)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCLIDownloadFromThePlatformRegistry(t *testing.T) {
	api, srv := setupAPIWith(t, &termDocker{}, &termDocker{})
	keyFile, certFile, _ := registryKeys(t)
	var blobs int32
	reg := cliRegistry(t, &blobs)
	defer reg.Close()
	if err := api.WithRegistry(RegistryConfig{Service: "registry.example.test", Issuer: "base-stack", KeyFile: keyFile, CertFile: certFile, URL: reg.URL}); err != nil {
		t.Fatal(err)
	}
	if err := api.WithAgents(AgentConfig{PublicURL: "https://api.example.test", CACertFile: certFile, AgentImage: "registry.example.test/basestack/agent:dev"}); err != nil {
		t.Fatal(err)
	}
	binCache.dir = t.TempDir()

	get := func(path string) (int, string, http.Header) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), resp.Header
	}

	code, body, h := get("/v1/cli/download/basestack?os=darwin&arch=arm64")
	sum := sha256.Sum256([]byte("binario-mac"))
	if code != 200 || body != "binario-mac" || h.Get("X-Content-SHA256") != hex.EncodeToString(sum[:]) {
		t.Fatalf("download: %d %q %v", code, body, h)
	}
	// A segunda vez sai da cache, sem voltar ao registry.
	before := atomic.LoadInt32(&blobs)
	if code, body, _ := get("/v1/cli/download/basestack?os=darwin&arch=arm64"); code != 200 || body != "binario-mac" || atomic.LoadInt32(&blobs) != before {
		t.Errorf("da cache: %d %q, blobs %d -> %d", code, body, before, blobs)
	}
	if code, body, _ := get("/v1/cli/download/bs?os=linux&arch=amd64"); code != 200 || body != "bs-linux" {
		t.Errorf("bs linux: %d %q", code, body)
	}
	for _, bad := range []string{"/v1/cli/download/rm?os=linux&arch=amd64", "/v1/cli/download/bs?os=windows&arch=amd64", "/v1/cli/download/bs?os=linux&arch=386"} {
		if code, _, _ := get(bad); code != 400 {
			t.Errorf("%s: %d, queria 400", bad, code)
		}
	}

	// O install.sh leva o endereço por onde foi pedido.
	code, script, _ := get("/v1/cli/install.sh")
	if code != 200 || !strings.Contains(script, `API="`+srv.URL+`"`) || !strings.Contains(script, "uname -m") {
		t.Errorf("install.sh: %d\n%s", code, script)
	}
	_ = os.RemoveAll(binCache.dir)
}
