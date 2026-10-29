package platformapi

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fakeRegistry imita a API v2 do registry: só responde se o token estiver
// assinado pela chave da instalação e tiver o scope do caminho pedido.
func fakeRegistry(t *testing.T, cert *x509.Certificate) *httptest.Server {
	t.Helper()
	blobs := map[string]string{
		"sha256:cfg-arm":   `{"os":"linux","architecture":"arm64","created":"2026-10-20T10:00:00Z"}`,
		"sha256:cfg-amd":   `{"os":"linux","architecture":"amd64","created":"2026-10-21T10:00:00Z"}`,
		"sha256:cfg-other": `{"os":"linux","architecture":"amd64"}`,
	}
	single := func(cfg string, layers ...int) string {
		ls := make([]string, len(layers))
		for i, n := range layers {
			ls[i] = `{"size":` + itoa(n) + `}`
		}
		return `{"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"` + cfg + `","size":10},"layers":[` + strings.Join(ls, ",") + `]}`
	}
	manifests := map[string]string{
		"loja/web/1.0": single("sha256:cfg-arm", 100, 200),
		"loja/web/2.0": `{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[
			{"digest":"sha256:m-amd","platform":{"os":"linux","architecture":"amd64"}},
			{"digest":"sha256:m-arm","platform":{"os":"linux","architecture":"arm64","variant":"v8"}},
			{"digest":"sha256:m-att","platform":{"os":"unknown","architecture":"unknown"}}]}`,
		"loja/web/sha256:m-amd": single("sha256:cfg-amd", 1000),
		"loja/web/sha256:m-arm": single("sha256:cfg-arm", 2000),
		"blog/app/latest":       single("sha256:cfg-other", 1),
	}
	tags := map[string][]string{"loja/web": {"2.0", "1.0"}, "loja/vazio": nil, "blog/app": {"latest"}}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v2/")
		want := registryAccess{Type: "registry", Name: "catalog", Actions: []string{"*"}}
		if path != "_catalog" {
			for _, sep := range []string{"/tags/", "/manifests/", "/blobs/"} {
				if i := strings.Index(path, sep); i > 0 {
					want = registryAccess{Type: "repository", Name: path[:i], Actions: []string{"pull"}}
				}
			}
		}
		if !tokenGrants(cert, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), want) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case path == "_catalog":
			_, _ = w.Write([]byte(`{"repositories":["blog/app","loja/vazio","loja/web"]}`))
		case strings.HasSuffix(path, "/tags/list"):
			repo := strings.TrimSuffix(path, "/tags/list")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags[repo]})
		case strings.Contains(path, "/manifests/"):
			repo, ref, _ := strings.Cut(path, "/manifests/")
			body, ok := manifests[repo+"/"+ref]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Docker-Content-Digest", "sha256:digest-of-"+ref)
			_, _ = w.Write([]byte(body))
		case strings.Contains(path, "/blobs/"):
			_, digest, _ := strings.Cut(path, "/blobs/")
			_, _ = w.Write([]byte(blobs[digest]))
		default:
			http.NotFound(w, r)
		}
	}))
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// tokenGrants verifica a assinatura e se o token dá exatamente want.
func tokenGrants(cert *x509.Certificate, token string, want registryAccess) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	var claims struct {
		Access []registryAccess `json:"access"`
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(cb, &claims)
	return slices.ContainsFunc(claims.Access, func(a registryAccess) bool { return reflect.DeepEqual(a, want) })
}

func TestProjectImagesListOnlyTheProject(t *testing.T) {
	api, srv := setupAPIWith(t, &termDocker{}, &termDocker{})
	keyFile, certFile, cert := registryKeys(t)
	reg := fakeRegistry(t, cert)
	defer reg.Close()
	if err := api.WithRegistry(RegistryConfig{Service: "registry.example.test", Issuer: "base-stack", KeyFile: keyFile, CertFile: certFile, URL: reg.URL}); err != nil {
		t.Fatal(err)
	}

	get := func(token string) (int, []Image) {
		req, _ := http.NewRequest("GET", srv.URL+"/v1/projects/loja/images", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct{ Images []Image }
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.Images
	}

	if code, _ := get("t-outsider"); code != http.StatusNotFound {
		t.Errorf("quem não é membro: %d, queria 404", code)
	}
	code, images := get("t-viewer")
	if code != 200 {
		t.Fatalf("viewer: %d", code)
	}
	// Só loja/web: blog/app é de outro projeto e loja/vazio não tem tags.
	if len(images) != 1 || images[0].Repository != "loja/web" || images[0].Reference != "registry.example.test/loja/web" {
		t.Fatalf("imagens = %+v", images)
	}
	tags := images[0].Tags
	if len(tags) != 2 || tags[0].Tag != "1.0" || tags[1].Tag != "2.0" {
		t.Fatalf("tags = %+v", tags)
	}
	if one := tags[0]; !reflect.DeepEqual(one.Platforms, []string{"linux/arm64"}) || one.Size != 310 ||
		one.Digest != "sha256:digest-of-1.0" || one.Created == nil || one.Created.Day() != 20 {
		t.Errorf("1.0 = %+v", one)
	}
	// Multi-arquitetura: as duas plataformas (sem a atestação), tamanhos somados.
	if two := tags[1]; !reflect.DeepEqual(two.Platforms, []string{"linux/amd64", "linux/arm64/v8"}) || two.Size != 3020 || two.Created == nil {
		t.Errorf("2.0 = %+v", two)
	}

	// Sem registry ligado, a lista vem vazia (o portal mostra isso).
	api.registry = nil
	if code, images := get("t-viewer"); code != 200 || len(images) != 0 {
		t.Errorf("sem registry: %d %v", code, images)
	}
}
