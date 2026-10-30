package platform

import (
	"context"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"launcher/internal/store"
)

// Proteção com password à frente de um serviço web (basic auth do
// Traefik): para mostrar uma fase da app a um cliente sem a abrir a toda a
// gente. Não é autenticação da app -- é o Traefik que pede utilizador e
// password, em todos os hosts do serviço (o por defeito e os domínios
// próprios), antes de o pedido chegar às réplicas.

const basicAuthMinPassword = 8

// SetBasicAuth liga a proteção (ou troca o utilizador e a password).
func (s *Service) SetBasicAuth(ctx context.Context, projectSlug, envSlug, svcSlug, user, password string) error {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return err
	}
	if svc.Kind != store.ServiceWeb {
		return invalid("só serviços web podem ser protegidos com password")
	}
	user = strings.TrimSpace(user)
	if user == "" || len(user) > 64 || strings.ContainsAny(user, ": \t\n") {
		return invalid("utilizador inválido -- sem espaços nem \":\", até 64 caracteres")
	}
	if len(password) < basicAuthMinPassword || len(password) > 72 {
		return invalid("a password tem de ter entre %d e 72 caracteres", basicAuthMinPassword)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.db.SetServiceBasicAuth(ctx, svc.ID, user, string(hash)); err != nil {
		return err
	}
	s.syncRouting(ctx)
	return nil
}

// ClearBasicAuth tira a proteção: o serviço volta a estar aberto.
func (s *Service) ClearBasicAuth(ctx context.Context, projectSlug, envSlug, svcSlug string) error {
	_, _, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return err
	}
	if err := s.db.SetServiceBasicAuth(ctx, svc.ID, "", ""); err != nil {
		return err
	}
	s.syncRouting(ctx)
	return nil
}
