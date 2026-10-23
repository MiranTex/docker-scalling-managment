package platform

import (
	"context"
	"regexp"
	"strings"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// registryRe: só o host[:porta] do registry -- sem esquema nem caminho.
var registryRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

func normalizeRegistry(r string) string {
	r = strings.ToLower(strings.TrimSpace(r))
	if r == "index.docker.io" || r == "registry-1.docker.io" {
		return "docker.io"
	}
	return r
}

func (s *Service) ListRegistries(ctx context.Context, projectSlug string) ([]store.RegistryCredential, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListRegistryCredentials(ctx, p.ID)
}

func (s *Service) PutRegistry(ctx context.Context, projectSlug, registry, username, password, by string) error {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return err
	}
	registry = normalizeRegistry(registry)
	if !registryRe.MatchString(registry) {
		return invalid("registry %q inválido -- só o host, ex: ghcr.io ou registry.exemplo.pt:5000", registry)
	}
	if username == "" || password == "" {
		return invalid("utilizador e password/token são obrigatórios")
	}
	if s.box == nil {
		return ErrSecretsDisabled
	}
	ct, nonce, err := s.box.Seal([]byte(password))
	if err != nil {
		return err
	}
	return s.db.PutRegistryCredential(ctx, store.RegistryCredential{ProjectID: p.ID, Registry: registry, Username: username, Ciphertext: ct, Nonce: nonce, UpdatedBy: by})
}

func (s *Service) DeleteRegistry(ctx context.Context, projectSlug, registry string) error {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return err
	}
	return mapStoreErr(s.db.DeleteRegistryCredential(ctx, p.ID, normalizeRegistry(registry)))
}

// registryAuthFor devolve as credenciais do projeto para o registry da
// imagem, ou nil se não houver (pull anónimo).
func (s *Service) registryAuthFor(ctx context.Context, projectID, image string) (*dockerclient.RegistryAuth, error) {
	if projectID == "" || s.box == nil {
		return nil, nil
	}
	host := dockerclient.RegistryHost(image)
	creds, err := s.db.ListRegistryCredentials(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, c := range creds {
		if c.Registry != host {
			continue
		}
		pw, err := s.box.Open(c.Ciphertext, c.Nonce)
		if err != nil {
			return nil, err
		}
		server := host
		if host == "docker.io" {
			server = "https://index.docker.io/v1/"
		}
		return &dockerclient.RegistryAuth{Username: c.Username, Password: string(pw), ServerAddress: server}, nil
	}
	return nil, nil
}
