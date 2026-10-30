package platformapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Leitura do registry da plataforma para o portal: as imagens de um
// projeto, com as tags e, por tag, as arquiteturas, o tamanho e a data. O
// launcher fala diretamente com o registry (pela rede interna) com tokens
// que ele próprio assina -- quem pode ver já foi decidido pela rota
// (viewer do projeto).

// Image é um repositório do projeto (<projeto>/<nome>).
type Image struct {
	Repository string     `json:"repository"`
	Reference  string     `json:"reference"` // registry.<domínio>/<projeto>/<nome>
	Tags       []ImageTag `json:"tags"`
}

type ImageTag struct {
	Tag       string     `json:"tag"`
	Digest    string     `json:"digest"`
	Platforms []string   `json:"platforms"` // ex: linux/arm64
	Size      int64      `json:"size"`      // camadas comprimidas + config
	Created   *time.Time `json:"created,omitempty"`
}

const (
	mediaOCIIndex       = "application/vnd.oci.image.index.v1+json"
	mediaDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	mediaDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
)

func (a *API) listImages(w http.ResponseWriter, r *http.Request, _ principal) {
	project := r.PathValue("project")
	if a.registry == nil || a.registry.cfg.URL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "images": []Image{}})
		return
	}
	images, err := a.registry.projectImages(r.Context(), project)
	if err != nil {
		a.logger.Error("registry: listando imagens", "project", project, "err", err)
		writeError(w, http.StatusBadGateway, "registry_unavailable", "não foi possível ler o registry da plataforma")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "host": a.registry.cfg.Service, "images": images})
}

func (t *registryTokens) projectImages(ctx context.Context, project string) ([]Image, error) {
	repos, err := t.catalog(ctx)
	if err != nil {
		return nil, err
	}
	images := []Image{}
	for _, repo := range repos {
		if !strings.HasPrefix(repo, project+"/") {
			continue
		}
		img, err := t.image(ctx, repo)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", repo, err)
		}
		if len(img.Tags) > 0 { // repositórios sem tags (tudo apagado) não interessam
			images = append(images, img)
		}
	}
	return images, nil
}

func (t *registryTokens) catalog(ctx context.Context) ([]string, error) {
	scope := registryAccess{Type: "registry", Name: "catalog", Actions: []string{"*"}}
	var all []string
	next := "/v2/_catalog?n=1000"
	for next != "" {
		var page struct {
			Repositories []string `json:"repositories"`
		}
		resp, err := t.get(ctx, next, scope, nil, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Repositories...)
		next = nextPage(resp.Header.Get("Link"))
	}
	slices.Sort(all)
	return all, nil
}

// nextPage lê o Link: </v2/_catalog?last=x&n=1000>; rel="next".
func nextPage(link string) string {
	start, end := strings.Index(link, "<"), strings.Index(link, ">")
	if start < 0 || end < start || !strings.Contains(link, `rel="next"`) {
		return ""
	}
	return link[start+1 : end]
}

func (t *registryTokens) image(ctx context.Context, repo string) (Image, error) {
	scope := registryAccess{Type: "repository", Name: repo, Actions: []string{"pull"}}
	img := Image{Repository: repo, Reference: t.cfg.Service + "/" + repo, Tags: []ImageTag{}}
	var list struct {
		Tags []string `json:"tags"`
	}
	if _, err := t.get(ctx, "/v2/"+repo+"/tags/list", scope, nil, &list); err != nil {
		return img, err
	}
	slices.Sort(list.Tags)
	for _, tag := range list.Tags {
		it, err := t.tag(ctx, repo, tag, scope)
		if err != nil {
			return img, err
		}
		img.Tags = append(img.Tags, it)
	}
	return img, nil
}

type manifest struct {
	MediaType string `json:"mediaType"`
	Config    struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"config"`
	Layers []struct {
		Digest    string `json:"digest"`
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
	} `json:"layers"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant"`
		} `json:"platform"`
	} `json:"manifests"`
}

// tag resolve uma tag: uma imagem simples ou um índice multi-arquitetura
// (aí soma o tamanho de cada plataforma e usa a data da primeira).
func (t *registryTokens) tag(ctx context.Context, repo, tag string, scope registryAccess) (ImageTag, error) {
	it := ImageTag{Tag: tag, Platforms: []string{}}
	m, digest, err := t.manifest(ctx, repo, tag, scope)
	if err != nil {
		return it, err
	}
	it.Digest = digest
	if len(m.Manifests) == 0 {
		platform, created, err := t.describe(ctx, repo, m, scope)
		if err != nil {
			return it, err
		}
		it.Platforms, it.Size, it.Created = []string{platform}, size(m), created
		return it, nil
	}
	for _, child := range m.Manifests {
		p := child.Platform
		if p.OS == "unknown" { // atestações (provenance/SBOM) do buildx
			continue
		}
		cm, _, err := t.manifest(ctx, repo, child.Digest, scope)
		if err != nil {
			return it, err
		}
		name := p.OS + "/" + p.Architecture
		if p.Variant != "" {
			name += "/" + p.Variant
		}
		it.Platforms = append(it.Platforms, name)
		it.Size += size(cm)
		if it.Created == nil {
			_, it.Created, _ = t.describe(ctx, repo, cm, scope)
		}
	}
	return it, nil
}

func size(m manifest) int64 {
	n := m.Config.Size
	for _, l := range m.Layers {
		n += l.Size
	}
	return n
}

func (t *registryTokens) manifest(ctx context.Context, repo, ref string, scope registryAccess) (manifest, string, error) {
	var m manifest
	accept := []string{mediaOCIIndex, mediaDockerList, mediaOCIManifest, mediaDockerManifest}
	resp, err := t.get(ctx, "/v2/"+repo+"/manifests/"+url.PathEscape(ref), scope, accept, &m)
	if err != nil {
		return m, "", err
	}
	return m, resp.Header.Get("Docker-Content-Digest"), nil
}

// describe lê a config da imagem: plataforma e data de criação.
func (t *registryTokens) describe(ctx context.Context, repo string, m manifest, scope registryAccess) (string, *time.Time, error) {
	var cfg struct {
		OS           string    `json:"os"`
		Architecture string    `json:"architecture"`
		Variant      string    `json:"variant"`
		Created      time.Time `json:"created"`
	}
	if _, err := t.get(ctx, "/v2/"+repo+"/blobs/"+m.Config.Digest, scope, nil, &cfg); err != nil {
		return "", nil, err
	}
	platform := cfg.OS + "/" + cfg.Architecture
	if cfg.Variant != "" {
		platform += "/" + cfg.Variant
	}
	var created *time.Time
	if !cfg.Created.IsZero() {
		created = &cfg.Created
	}
	return platform, created, nil
}

// open faz um pedido ao registry com um token só para este scope; quem
// chama fecha o corpo.
func (t *registryTokens) open(ctx context.Context, path string, scope registryAccess, accept []string) (*http.Response, error) {
	token, err := t.sign("basestack-portal", []registryAccess{scope})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(t.cfg.URL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for _, a := range accept {
		req.Header.Add("Accept", a)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return resp, nil
}

// get é o open de uma resposta JSON, descodificada para out.
func (t *registryTokens) get(ctx context.Context, path string, scope registryAccess, accept []string, out any) (*http.Response, error) {
	resp, err := t.open(ctx, path, scope, accept)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, errors.Join(fmt.Errorf("GET %s: resposta inválida", path), err)
	}
	return resp, nil
}
