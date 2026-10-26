package platformapi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"launcher/internal/apikeyclient"
	"launcher/internal/platform"
	"launcher/internal/store"
)

// Servidor de tokens do registry da plataforma (o "registry" oficial do
// Docker com auth=token). O docker pede aqui, com Basic auth, um token
// para cada operação ("repository:teste/app:pull,push"); o launcher decide
// pelo papel no projeto -- o primeiro segmento do nome da imagem -- e
// assina um JWT que o registry verifica com o certificado da instalação.
//
// Password aceite: uma API key pessoal (ak_, scope platform), um token de
// deploy (bsd_, só o seu projeto) ou a credencial interna do launcher
// (utilizador basestack-internal: lê tudo, escreve só "basestack/...", as
// imagens da própria plataforma).

const registryTokenTTL = 5 * time.Minute

// RegistryConfig liga o servidor de tokens. KeyFile/CertFile em PEM: a
// chave RSA que assina e o certificado que o registry tem como confiável.
type RegistryConfig struct {
	Service      string // registry.<domínio> (o "aud" dos tokens)
	Issuer       string
	KeyFile      string
	CertFile     string
	PullPassword string
}

type registryTokens struct {
	cfg   RegistryConfig
	key   *rsa.PrivateKey
	chain []string // x5c: o certificado em DER/base64
}

// WithRegistry carrega a chave e o certificado; sem isto a rota responde
// 404 (registry desligado).
func (a *API) WithRegistry(cfg RegistryConfig) error {
	keyPEM, err := os.ReadFile(cfg.KeyFile)
	if err != nil {
		return err
	}
	certPEM, err := os.ReadFile(cfg.CertFile)
	if err != nil {
		return err
	}
	kb, _ := pem.Decode(keyPEM)
	cb, _ := pem.Decode(certPEM)
	if kb == nil || cb == nil {
		return errors.New("registry: chave ou certificado não estão em PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(kb.Bytes); err == nil {
		key, _ = k.(*rsa.PrivateKey)
	} else if k, err := x509.ParsePKCS1PrivateKey(kb.Bytes); err == nil {
		key = k
	}
	if key == nil {
		return errors.New("registry: a chave de assinatura tem de ser RSA")
	}
	a.registry = &registryTokens{cfg: cfg, key: key, chain: []string{base64.StdEncoding.EncodeToString(cb.Bytes)}}
	return nil
}

// registryGrantee é quem pede o token.
type registryGrantee struct {
	subject       string
	internal      bool
	platformAdmin bool
	deployProject string // token de deploy: só este projeto
}

type registryAccess struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

func (a *API) registryToken(w http.ResponseWriter, r *http.Request) {
	if a.registry == nil {
		writeError(w, http.StatusNotFound, "not_found", "o registry da plataforma não está ligado")
		return
	}
	g, ok := a.registryAuthenticate(w, r)
	if !ok {
		return
	}
	var access []registryAccess
	for _, raw := range r.URL.Query()["scope"] {
		for _, scope := range strings.Fields(raw) {
			typ, rest, ok1 := strings.Cut(scope, ":")
			i := strings.LastIndex(rest, ":")
			if !ok1 || i < 0 {
				continue
			}
			name, wanted := rest[:i], strings.Split(rest[i+1:], ",")
			granted := a.registryAllowed(r.Context(), g, typ, name, wanted)
			if len(granted) > 0 {
				access = append(access, registryAccess{Type: typ, Name: name, Actions: granted})
				if slices.Contains(granted, "push") && !g.internal {
					a.audit.Log(g.subject, "registry.push", name, "autorizado")
				}
			}
		}
	}
	token, err := a.registry.sign(g.subject, access)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token, "access_token": token,
		"expires_in": int(registryTokenTTL / time.Second), "issued_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// registryAuthenticate lê o Basic auth. Sem credenciais o token sai sem
// acessos -- o registry responde 401 e o docker pede "docker login".
func (a *API) registryAuthenticate(w http.ResponseWriter, r *http.Request) (registryGrantee, bool) {
	user, pass, ok := r.BasicAuth()
	deny := func(msg string) (registryGrantee, bool) {
		w.Header().Set("WWW-Authenticate", `Basic realm="base-stack registry"`)
		writeError(w, http.StatusUnauthorized, "unauthorized", msg)
		return registryGrantee{}, false
	}
	switch {
	case !ok:
		return registryGrantee{subject: "anonymous"}, true
	case user == platform.RegistryInternalUser:
		want := a.registry.cfg.PullPassword
		if want == "" || subtle.ConstantTimeCompare([]byte(pass), []byte(want)) != 1 {
			return deny("credencial interna inválida")
		}
		return registryGrantee{subject: user, internal: true}, true
	case strings.HasPrefix(pass, apikeyclient.Prefix):
		if a.apiKeys == nil {
			return deny("API keys não estão ligadas")
		}
		res, err := a.apiKeys.Introspect(r.Context(), pass)
		if err != nil {
			a.logger.Error("registry: validando API key", "err", err)
			writeError(w, http.StatusBadGateway, "auth_unavailable", "não foi possível validar a API key")
			return registryGrantee{}, false
		}
		if !res.Active || !res.HasScope(platformScope) {
			return deny("API key inválida, expirada, revogada ou sem o scope \"" + platformScope + "\"")
		}
		return registryGrantee{subject: res.Owner, platformAdmin: isPlatformRole(res.Role)}, true
	case strings.HasPrefix(pass, platform.DeployTokenPrefix):
		project, err := a.svc.DeployTokenProject(r.Context(), pass)
		if errors.Is(err, platform.ErrUnauthorized) {
			return deny("token de deploy inválido ou revogado")
		}
		if err != nil {
			a.fail(w, err)
			return registryGrantee{}, false
		}
		return registryGrantee{subject: "deploy-token:" + project, deployProject: project}, true
	default:
		return deny("usa como password uma API key (ak_..., scope platform) ou um token de deploy (bsd_...)")
	}
}

// registryAllowed é a interseção do pedido com o que o papel permite:
// viewer busca (pull); developer e admin também enviam (push).
func (a *API) registryAllowed(ctx context.Context, g registryGrantee, typ, name string, wanted []string) []string {
	var can []string
	project, _, _ := strings.Cut(name, "/")
	switch {
	case typ == "registry" && name == "catalog":
		if g.platformAdmin {
			can = []string{"*"}
		}
	case typ != "repository" || !strings.Contains(name, "/"):
		// Só imagens com projeto: <projeto>/<nome>.
	case g.internal:
		can = []string{"pull"}
		if project == "basestack" {
			can = append(can, "push")
		}
	case g.platformAdmin:
		// Tudo, mas só em projetos que existem (e nas imagens da própria
		// plataforma) -- senão um engano no nome criava um "projeto"
		// fantasma no registry.
		if project == "basestack" || a.projectExists(ctx, project) {
			can = []string{"pull", "push"}
		}
	case g.deployProject != "":
		if project == g.deployProject {
			can = []string{"pull", "push"}
		}
	case g.subject != "anonymous":
		role, err := a.svc.MemberRole(ctx, project, g.subject)
		if err != nil || role == "" {
			break
		}
		can = []string{"pull"}
		if platform.RoleAtLeast(role, store.MemberDeveloper) {
			can = append(can, "push")
		}
	}
	var out []string
	for _, w := range wanted {
		if slices.Contains(can, w) || slices.Contains(can, "*") {
			out = append(out, w)
		}
	}
	return out
}

func (a *API) projectExists(ctx context.Context, slug string) bool {
	_, err := a.svc.GetProject(ctx, slug)
	return err == nil
}

func (t *registryTokens) sign(subject string, access []registryAccess) (string, error) {
	if access == nil {
		access = []registryAccess{}
	}
	now := time.Now()
	jti := make([]byte, 16)
	_, _ = rand.Read(jti)
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "x5c": t.chain})
	claims, _ := json.Marshal(map[string]any{
		"iss": t.cfg.Issuer, "sub": subject, "aud": t.cfg.Service,
		"iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(), "exp": now.Add(registryTokenTTL).Unix(),
		"jti": hex.EncodeToString(jti), "access": access,
	})
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, t.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("assinando token do registry: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
