package platformapi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"launcher/internal/apikeyclient"
	"launcher/internal/platform"
)

// registryKeys escreve uma chave RSA e um certificado auto-assinado.
func registryKeys(t *testing.T) (keyFile, certFile string, cert *x509.Certificate) {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "base-stack registry tokens"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cert, _ = x509.ParseCertificate(der)
	dir := t.TempDir()
	keyFile, certFile = filepath.Join(dir, "token.key"), filepath.Join(dir, "token.crt")
	kder, _ := x509.MarshalPKCS8PrivateKey(key)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}), 0o600)
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	return keyFile, certFile, cert
}

// askToken pede um token como o docker: Basic auth e scope.
func askToken(t *testing.T, srv *httptest.Server, user, pass string, scopes ...string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/registry/token?service=registry.example.test", nil)
	q := req.URL.Query()
	for _, s := range scopes {
		q.Add("scope", s)
	}
	req.URL.RawQuery = q.Encode()
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// grantedActions verifica a assinatura do token com o certificado e
// devolve as ações dadas para name.
func grantedActions(t *testing.T, cert *x509.Certificate, token, name string) []string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token mal formado: %q", token)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("assinatura inválida: %v", err)
	}
	var header struct {
		X5C []string `json:"x5c"`
	}
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = json.Unmarshal(hb, &header)
	if len(header.X5C) != 1 || header.X5C[0] != base64.StdEncoding.EncodeToString(cert.Raw) {
		t.Errorf("x5c não é o certificado: %v", header.X5C)
	}
	var claims struct {
		Aud    string           `json:"aud"`
		Access []registryAccess `json:"access"`
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(cb, &claims)
	if claims.Aud != "registry.example.test" {
		t.Errorf("aud = %q", claims.Aud)
	}
	for _, a := range claims.Access {
		if a.Name == name {
			return a.Actions
		}
	}
	return nil
}

func TestRegistryTokensFollowProjectRoles(t *testing.T) {
	ctx := context.Background()
	api, srv := setupAPIWith(t, &termDocker{}, &termDocker{})
	keyFile, certFile, cert := registryKeys(t)
	if err := api.WithRegistry(RegistryConfig{Service: "registry.example.test", Issuer: "base-stack", KeyFile: keyFile, CertFile: certFile, PullPassword: "interno"}); err != nil {
		t.Fatal(err)
	}
	api.apiKeys = fakeAPIKeys{
		"ak_dev":      {Active: true, Owner: "u-dev", Scopes: []string{"platform"}, Role: "user"},
		"ak_viewer":   {Active: true, Owner: "u-viewer", Scopes: []string{"platform"}, Role: "user"},
		"ak_outsider": {Active: true, Owner: "u-outsider", Scopes: []string{"platform"}, Role: "user"},
		"ak_admin":    {Active: true, Owner: "u-platform", Scopes: []string{"platform"}, Role: "infra-admin"},
		"ak_noscope":  {Active: true, Owner: "u-dev", Scopes: []string{"read"}, Role: "user"},
		"ak_revoked":  {Active: false},
	}
	tok, err := api.svc.CreateDeployToken(ctx, "loja", platform.CreateDeployTokenInput{Name: "ci"}, "t")
	if err != nil {
		t.Fatal(err)
	}

	const repo = "loja/app"
	both := "repository:" + repo + ":pull,push"
	for _, c := range []struct {
		name, user, pass string
		want             []string
	}{
		{"developer", "dev@x.pt", "ak_dev", []string{"pull", "push"}},
		{"viewer só busca", "viewer@x.pt", "ak_viewer", []string{"pull"}},
		{"quem não é membro", "o@x.pt", "ak_outsider", nil},
		{"admin da plataforma", "a@x.pt", "ak_admin", []string{"pull", "push"}},
		{"token de deploy do projeto", "ci", tok.Token, []string{"pull", "push"}},
		{"credencial interna só busca", platform.RegistryInternalUser, "interno", []string{"pull"}},
		{"sem credenciais", "", "", nil},
	} {
		code, out := askToken(t, srv, c.user, c.pass, both)
		if code != 200 {
			t.Errorf("%s: %d %v", c.name, code, out)
			continue
		}
		if got := grantedActions(t, cert, out["token"].(string), repo); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ações = %v, quero %v", c.name, got, c.want)
		}
	}

	// A credencial interna escreve só as imagens da plataforma.
	_, out := askToken(t, srv, platform.RegistryInternalUser, "interno", "repository:basestack/launcher:pull,push")
	if got := grantedActions(t, cert, out["token"].(string), "basestack/launcher"); !reflect.DeepEqual(got, []string{"pull", "push"}) {
		t.Errorf("interna em basestack/: %v", got)
	}
	// Um admin da plataforma não cria "projetos" fantasma no registry.
	_, out = askToken(t, srv, "a@x.pt", "ak_admin", "repository:naoexiste/app:pull,push")
	if got := grantedActions(t, cert, out["token"].(string), "naoexiste/app"); got != nil {
		t.Errorf("admin num projeto que não existe: %v", got)
	}
	_, out = askToken(t, srv, "a@x.pt", "ak_admin", "repository:basestack/launcher:push")
	if got := grantedActions(t, cert, out["token"].(string), "basestack/launcher"); !reflect.DeepEqual(got, []string{"push"}) {
		t.Errorf("admin nas imagens da plataforma: %v", got)
	}
	// A CLI (basestack upgrade) lê-a quem tem acesso; o resto da
	// plataforma, não.
	_, out = askToken(t, srv, "o@x.pt", "ak_outsider", "repository:basestack/cli:pull,push", "repository:basestack/launcher:pull")
	if got := grantedActions(t, cert, out["token"].(string), "basestack/cli"); !reflect.DeepEqual(got, []string{"pull"}) {
		t.Errorf("basestack/cli para qualquer conta: %v, queria só pull", got)
	}
	if got := grantedActions(t, cert, out["token"].(string), "basestack/launcher"); got != nil {
		t.Errorf("basestack/launcher para qualquer conta: %v", got)
	}
	_, out = askToken(t, srv, "", "", "repository:basestack/cli:pull")
	if got := grantedActions(t, cert, out["token"].(string), "basestack/cli"); got != nil {
		t.Errorf("basestack/cli sem credenciais: %v", got)
	}
	// O catálogo (listar tudo) é só para admins da plataforma.
	_, out = askToken(t, srv, "dev@x.pt", "ak_dev", "registry:catalog:*")
	if got := grantedActions(t, cert, out["token"].(string), "catalog"); got != nil {
		t.Errorf("developer listou o catálogo: %v", got)
	}

	for _, bad := range []struct{ user, pass string }{
		{"dev@x.pt", "password"}, {"dev@x.pt", "ak_revoked"}, {"dev@x.pt", "ak_noscope"},
		{platform.RegistryInternalUser, "errada"}, {"ci", "bsd_naoexiste"},
	} {
		if code, _ := askToken(t, srv, bad.user, bad.pass, both); code != http.StatusUnauthorized {
			t.Errorf("%s/%s: %d, queria 401", bad.user, bad.pass, code)
		}
	}
}

var _ = apikeyclient.Prefix
