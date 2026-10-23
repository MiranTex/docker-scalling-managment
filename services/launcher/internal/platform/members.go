package platform

import (
	"context"
	"errors"
	"fmt"

	"launcher/internal/store"
)

// memberRank ordena os papéis: quem tem um papel pode tudo o que os de
// baixo podem.
var memberRank = map[string]int{store.MemberViewer: 1, store.MemberDeveloper: 2, store.MemberAdmin: 3}

// RoleAtLeast diz se have chega para o que exige want.
func RoleAtLeast(have, want string) bool {
	return memberRank[have] >= memberRank[want] && memberRank[have] > 0
}

func validMemberRole(role string) error {
	if _, ok := memberRank[role]; !ok {
		return invalid("papel %q desconhecido -- use viewer, developer ou admin", role)
	}
	return nil
}

// MemberRole devolve o papel de userID no projeto, ou "" se não for membro
// (ou o projeto não existir -- para quem não é membro é igual).
func (s *Service) MemberRole(ctx context.Context, projectSlug, userID string) (string, error) {
	p, err := s.db.GetProjectBySlug(ctx, store.DefaultAccountID, projectSlug)
	if errors.Is(err, store.ErrProjectNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	m, err := s.db.GetMember(ctx, p.ID, userID)
	if errors.Is(err, store.ErrMemberNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return m.Role, nil
}

// ListProjectsForUser: os projetos de que userID é membro.
func (s *Service) ListProjectsForUser(ctx context.Context, userID string) ([]store.Project, error) {
	return s.db.ListProjectsForUser(ctx, store.DefaultAccountID, userID)
}

func (s *Service) ListMembers(ctx context.Context, projectSlug string) ([]store.Member, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListMembers(ctx, p.ID)
}

// PutMember acrescenta (ou muda o papel de) uma conta já resolvida no auth.
func (s *Service) PutMember(ctx context.Context, projectSlug, userID, email, role, by string) (store.Member, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return store.Member{}, err
	}
	if err := validMemberRole(role); err != nil {
		return store.Member{}, err
	}
	if userID == "" {
		return store.Member{}, invalid("conta desconhecida")
	}
	m := store.Member{ProjectID: p.ID, UserID: userID, Email: email, Role: role, AddedBy: by}
	if err := s.db.PutMember(ctx, m); err != nil {
		return store.Member{}, err
	}
	return s.db.GetMember(ctx, p.ID, userID)
}

func (s *Service) SetMemberRole(ctx context.Context, projectSlug, userID, role string) (store.Member, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return store.Member{}, err
	}
	if err := validMemberRole(role); err != nil {
		return store.Member{}, err
	}
	m, err := s.db.GetMember(ctx, p.ID, userID)
	if errors.Is(err, store.ErrMemberNotFound) {
		return store.Member{}, fmt.Errorf("%w: essa conta não é membro do projeto", ErrNotFound)
	}
	if err != nil {
		return store.Member{}, err
	}
	m.Role = role
	if err := s.db.PutMember(ctx, m); err != nil {
		return store.Member{}, err
	}
	return m, nil
}

func (s *Service) RemoveMember(ctx context.Context, projectSlug, userID string) error {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return err
	}
	err = s.db.DeleteMember(ctx, p.ID, userID)
	if errors.Is(err, store.ErrMemberNotFound) {
		return fmt.Errorf("%w: essa conta não é membro do projeto", ErrNotFound)
	}
	return err
}
