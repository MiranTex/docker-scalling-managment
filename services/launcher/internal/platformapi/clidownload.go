package platformapi

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Instalar a CLI numa máquina que ainda não tem nenhuma:
//
//	curl -fsSL http://api.<domínio>/v1/cli/install.sh | sh
//
// O script deteta o sistema e a arquitetura e descarrega daqui o basestack
// e o bs -- que o launcher tira da pasta /dist da imagem basestack/cli no
// registry da plataforma (a mesma que o "basestack upgrade" usa).

var (
	cliOS    = map[string]bool{"linux": true, "darwin": true}
	cliArch  = map[string]bool{"amd64": true, "arm64": true}
	cliNames = map[string]bool{"basestack": true, "bs": true}
)

// cliCache guarda os binários já extraídos, por digest da imagem.
type cliCache struct {
	mu  sync.Mutex
	dir string
}

var binCache = &cliCache{dir: filepath.Join(os.TempDir(), "basestack-cli")}

// cliImage: a imagem basestack/cli da versão da plataforma (a mesma tag
// que a imagem do agente).
func (a *API) cliImage() (repo, tag string, ok bool) {
	if a.agents == nil || a.registry == nil || a.registry.cfg.URL == "" {
		return "", "", false
	}
	ref := strings.TrimPrefix(a.agents.cfg.AgentImage, a.registry.cfg.Service+"/")
	name, tag, found := strings.Cut(ref, ":")
	if !found || !strings.HasSuffix(name, "/agent") {
		return "", "", false
	}
	return strings.TrimSuffix(name, "/agent") + "/cli", tag, true
}

func (a *API) cliDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	osName, arch := r.URL.Query().Get("os"), r.URL.Query().Get("arch")
	if !cliNames[name] || !cliOS[osName] || !cliArch[arch] {
		writeError(w, http.StatusBadRequest, "invalid", "use /v1/cli/download/<basestack|bs>?os=<linux|darwin>&arch=<amd64|arm64>")
		return
	}
	repo, tag, ok := a.cliImage()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "esta plataforma não tem o registry ou os nós ligados -- sem CLI para descarregar")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	file := name + "-" + osName + "-" + arch
	path, sum, err := a.cliBinary(ctx, repo, tag, file)
	if err != nil {
		a.logger.Error("cli: extraindo binário", "file", file, "err", err)
		writeError(w, http.StatusBadGateway, "unavailable", "não foi possível obter "+file+" do registry da plataforma: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	w.Header().Set("X-Content-SHA256", sum)
	http.ServeFile(w, r, path)
}

// cliBinary devolve o ficheiro (em cache) de /dist/<file> na imagem.
func (a *API) cliBinary(ctx context.Context, repo, tag, file string) (string, string, error) {
	t := a.registry
	scope := registryAccess{Type: "repository", Name: repo, Actions: []string{"pull"}}
	m, _, err := t.manifest(ctx, repo, tag, scope)
	if err != nil {
		return "", "", err
	}
	// Um índice (várias arquiteturas): o /dist é igual em todas.
	if len(m.Manifests) > 0 {
		child := ""
		for _, c := range m.Manifests {
			if c.Platform.OS != "unknown" {
				child = c.Digest
				break
			}
		}
		if child == "" {
			return "", "", errors.New("índice sem imagens")
		}
		if m, _, err = t.manifest(ctx, repo, child, scope); err != nil {
			return "", "", err
		}
	}
	if len(m.Layers) == 0 {
		return "", "", errors.New("imagem sem camadas")
	}
	// A cache é por imagem (a config identifica-a) e por ficheiro.
	key := strings.TrimPrefix(m.Config.Digest, "sha256:")
	dir := filepath.Join(binCache.dir, key)
	path := filepath.Join(dir, file)
	binCache.mu.Lock()
	defer binCache.mu.Unlock()
	if sum, err := os.ReadFile(path + ".sha256"); err == nil {
		return path, string(sum), nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	// O /dist é a última camada (o último COPY); procura-se de trás para a
	// frente.
	for i := len(m.Layers) - 1; i >= 0; i-- {
		sum, found, err := extractFromLayer(ctx, t, repo, scope, m.Layers[i].Digest, "dist/"+file, path)
		if err != nil {
			return "", "", err
		}
		if found {
			if err := os.WriteFile(path+".sha256", []byte(sum), 0o644); err != nil {
				return "", "", err
			}
			return path, sum, nil
		}
	}
	return "", "", fmt.Errorf("%s não está na imagem (é de uma versão antiga, sem /dist?)", file)
}

func extractFromLayer(ctx context.Context, t *registryTokens, repo string, scope registryAccess, digest, want, dst string) (string, bool, error) {
	resp, err := t.open(ctx, "/v2/"+repo+"/blobs/"+digest, scope, nil)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", false, fmt.Errorf("camada %s: %w", digest, err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if strings.TrimPrefix(h.Name, "/") != want {
			continue
		}
		tmp := dst + ".tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return "", false, err
		}
		hash := sha256.New()
		if _, err := io.Copy(io.MultiWriter(f, hash), tr); err != nil {
			f.Close()
			return "", false, err
		}
		if err := f.Close(); err != nil {
			return "", false, err
		}
		return hex.EncodeToString(hash.Sum(nil)), true, os.Rename(tmp, dst)
	}
}

// cliInstallScript: o install.sh, com o endereço da API por onde foi
// pedido.
func (a *API) cliInstallScript(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	api := scheme + "://" + r.Host
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = fmt.Fprintf(w, installScript, api)
}

const installScript = `#!/bin/sh
# Instala o basestack e o bs da plataforma em %[1]s
#
#   curl -fsSL %[1]s/v1/cli/install.sh | sh
#   curl -fsSL %[1]s/v1/cli/install.sh | sh -s -- --only-bs
#
# Opções: --only-bs (só o bs, para quem só usa os projetos)
#         --dir <pasta> (por defeito ~/.local/bin)
#         --os <linux|darwin>  --arch <amd64|arm64> (por defeito, os desta máquina)
set -eu

API="%[1]s"
WHAT="basestack bs"
DIR="$HOME/.local/bin"
OS=""
ARCH=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only-bs) WHAT="bs" ;;
    --dir) DIR="$2"; shift ;;
    --os) OS="$2"; shift ;;
    --arch) ARCH="$2"; shift ;;
    -h|--help) echo "opções: --only-bs  --dir <pasta>  --os <linux|darwin>  --arch <amd64|arm64>"; exit 0 ;;
    *) echo "opção desconhecida: $1" >&2; exit 2 ;;
  esac
  shift
done

[ -n "$OS" ] || OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in linux|darwin) ;; *) echo "sistema não suportado: $OS (linux ou darwin)" >&2; exit 1 ;; esac
[ -n "$ARCH" ] || ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "arquitetura não suportada: $ARCH (amd64 ou arm64)" >&2; exit 1 ;;
esac

mkdir -p "$DIR"
for b in $WHAT; do
  echo "==> $b ($OS/$ARCH)"
  curl -fsSL "$API/v1/cli/download/$b?os=$OS&arch=$ARCH" -o "$DIR/$b.download"
  chmod +x "$DIR/$b.download"
  mv "$DIR/$b.download" "$DIR/$b"
done
# O bs também é o credential helper do docker para o registry da plataforma.
case " $WHAT " in *" bs "*) ln -sf bs "$DIR/docker-credential-basestack" ;; esac

echo
echo "Instalado em $DIR: $WHAT"
case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "Atenção: $DIR não está no PATH -- junta ao teu ~/.profile:  export PATH=\"$DIR:\$PATH\"" ;;
esac
echo
echo "A seguir:"
echo "  bs login $API                 (entrar pelo browser)"
case " $WHAT " in *" basestack "*) echo "  basestack node join ...       (para esta máquina ser um nó: o comando vem do portal, em Nós)" ;; esac
`
