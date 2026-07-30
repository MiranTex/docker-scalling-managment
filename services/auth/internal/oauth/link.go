package oauth

import (
	"context"
	"errors"
	"fmt"

	"auth/internal/store"
)

// Store é a persistência necessária pelo Manager -- implementada por
// *store.DB em produção.
type Store interface {
	FindUserByOAuthIdentity(ctx context.Context, provider, providerUserID string) (store.User, bool, error)
	FindUserByEmail(ctx context.Context, email string) (store.User, bool, error)
	CreateUserWithoutPassword(ctx context.Context, email string) (store.User, error)
	LinkOAuthIdentity(ctx context.Context, userID, provider, providerUserID, email string) error
}

// ErrUnknownProvider é devolvido quando o nome de provider pedido não foi
// registado neste Manager.
var ErrUnknownProvider = errors.New("oauth: provider desconhecido")

// ErrEmailNotVerified é devolvido quando o provider reporta um e-mail que
// já pertence a uma conta existente, mas sem confirmar que esse e-mail é
// verificado -- por segurança, recusamos ligar automaticamente (ver
// Manager.Login para o raciocínio completo).
var ErrEmailNotVerified = errors.New("oauth: provider não confirma que o e-mail é verificado")

// Manager orquestra login social: troca o código OAuth2 por uma
// identidade, e decide se essa identidade já é uma conta conhecida, deve
// ser ligada a uma conta existente, ou deve virar uma conta nova.
type Manager struct {
	providers map[string]Provider
	store     Store
}

// NewManager cria um Manager com os providers informados, indexados pelo
// próprio Provider.Name.
func NewManager(store Store, providers ...Provider) *Manager {
	m := &Manager{providers: make(map[string]Provider, len(providers)), store: store}
	for _, p := range providers {
		m.providers[p.Name] = p
	}
	return m
}

// AuthCodeURL devolve o URL de autorização do provider nomeado.
func (m *Manager) AuthCodeURL(providerName, state string) (string, error) {
	p, ok := m.providers[providerName]
	if !ok {
		return "", ErrUnknownProvider
	}
	return p.AuthCodeURL(state), nil
}

// Login troca code pelo token de acesso do provider, busca a identidade
// do utilizador, e devolve a conta correspondente -- criando-a ou
// ligando-a a uma já existente conforme a política abaixo:
//
//  1. Identidade já ligada a uma conta? Devolve essa conta (caminho comum
//     depois do primeiro login).
//  2. Já existe uma conta com o mesmo e-mail? Só liga a ela se o provider
//     confirma que o e-mail é verificado (EmailVerified) -- senão,
//     qualquer um poderia reivindicar o e-mail de outra pessoa que não é
//     dele e assumir a conta dela. Sem confirmação, devolve
//     ErrEmailNotVerified em vez de arriscar.
//  3. Nenhuma das anteriores: cria uma conta nova com esse e-mail (sem
//     senha) e liga a identidade a ela.
func (m *Manager) Login(ctx context.Context, providerName, code string) (store.User, error) {
	p, ok := m.providers[providerName]
	if !ok {
		return store.User{}, ErrUnknownProvider
	}

	identity, err := p.exchangeAndFetch(ctx, code)
	if err != nil {
		return store.User{}, err
	}

	if user, found, err := m.store.FindUserByOAuthIdentity(ctx, identity.Provider, identity.ProviderUserID); err != nil {
		return store.User{}, fmt.Errorf("oauth: buscando conta já ligada: %w", err)
	} else if found {
		return user, nil
	}

	if user, found, err := m.store.FindUserByEmail(ctx, identity.Email); err != nil {
		return store.User{}, fmt.Errorf("oauth: buscando conta por e-mail: %w", err)
	} else if found {
		if !identity.EmailVerified {
			return store.User{}, ErrEmailNotVerified
		}
		if err := m.store.LinkOAuthIdentity(ctx, user.ID, identity.Provider, identity.ProviderUserID, identity.Email); err != nil {
			return store.User{}, fmt.Errorf("oauth: ligando identidade a conta existente: %w", err)
		}
		return user, nil
	}

	user, err := m.store.CreateUserWithoutPassword(ctx, identity.Email)
	if err != nil {
		return store.User{}, fmt.Errorf("oauth: criando conta nova: %w", err)
	}
	if err := m.store.LinkOAuthIdentity(ctx, user.ID, identity.Provider, identity.ProviderUserID, identity.Email); err != nil {
		return store.User{}, fmt.Errorf("oauth: ligando identidade a conta nova: %w", err)
	}
	return user, nil
}
