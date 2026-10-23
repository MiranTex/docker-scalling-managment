package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"launcher/internal/store"
)

// DeployTokenPrefix distingue um token de deploy de um JWT logo no
// cabeçalho, sem tentar validá-lo como JWT primeiro.
const DeployTokenPrefix = "bsd_"

// ErrUnauthorized: token de deploy desconhecido ou revogado.
var ErrUnauthorized = errors.New("token de deploy inválido ou revogado")

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type CreateDeployTokenInput struct {
	Name string `json:"name"`
	// Environment restringe o token a um ambiente (slug); vazio = todos.
	Environment string `json:"environment"`
}

// NewDeployToken é a resposta da criação: o único momento em que o token
// em claro existe fora de quem o pediu.
type NewDeployToken struct {
	store.DeployToken
	Token string `json:"token"`
}

func (s *Service) CreateDeployToken(ctx context.Context, projectSlug string, in CreateDeployTokenInput, by string) (NewDeployToken, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return NewDeployToken{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 100 {
		return NewDeployToken{}, invalid("dá um nome ao token (ex: github-actions)")
	}
	var envID *string
	if in.Environment != "" {
		e, err := s.db.GetEnvironmentBySlug(ctx, p.ID, in.Environment)
		if err != nil {
			return NewDeployToken{}, mapStoreErr(err)
		}
		envID = &e.ID
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return NewDeployToken{}, err
	}
	token := DeployTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	t := store.DeployToken{
		ID: newID(), ProjectID: p.ID, EnvironmentID: envID, Name: name,
		TokenHash: hashToken(token), Prefix: token[:len(DeployTokenPrefix)+6], CreatedBy: by,
	}
	if err := s.db.CreateDeployToken(ctx, t); err != nil {
		return NewDeployToken{}, err
	}
	return NewDeployToken{DeployToken: t, Token: token}, nil
}

func (s *Service) ListDeployTokens(ctx context.Context, projectSlug string) ([]store.DeployToken, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListDeployTokens(ctx, p.ID)
}

func (s *Service) RevokeDeployToken(ctx context.Context, projectSlug, id string) error {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return err
	}
	err = s.db.RevokeDeployToken(ctx, p.ID, id)
	if errors.Is(err, store.ErrDeployTokenNotFound) {
		return ErrNotFound
	}
	return err
}

// AuthorizeDeployToken valida o token e confirma que ele pode agir no
// projeto/ambiente pedidos (slugs do URL). Devolve o token, para quem
// chama saber em nome de quem regista o deploy.
func (s *Service) AuthorizeDeployToken(ctx context.Context, token, projectSlug, envSlug string) (store.DeployToken, error) {
	t, err := s.db.FindActiveDeployToken(ctx, hashToken(token))
	if errors.Is(err, store.ErrDeployTokenNotFound) {
		return store.DeployToken{}, ErrUnauthorized
	}
	if err != nil {
		return store.DeployToken{}, err
	}
	p, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		// Não revela se o projeto existe a quem tem um token de outro.
		return store.DeployToken{}, ErrForbidden
	}
	if t.ProjectID != p.ID || (t.EnvironmentID != nil && *t.EnvironmentID != e.ID) {
		return store.DeployToken{}, ErrForbidden
	}
	return t, nil
}

// ErrForbidden: o token é válido mas não para este projeto/ambiente.
var ErrForbidden = errors.New("este token de deploy não tem acesso a este projeto/ambiente")
