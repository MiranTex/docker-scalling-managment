// Package apikey implementa chaves de longa duração para autenticação
// máquina-a-máquina (M2M) — diferente de um JWT (curto, pensado pra um
// utilizador logado numa sessão), uma API key é pra um serviço chamar
// outro sem envolver login/sessão nenhum: só apresenta a chave a cada
// chamada.
//
// Mesma ideia de segurança do refresh token: o segredo em si só existe
// uma vez (no momento em que é criado); daí em diante só o hash é
// persistido e comparado.
package apikey

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"auth/internal/idgen"
	"auth/internal/opaquetoken"
)

// Prefix é o prefixo visível de toda API key emitida por este pacote —
// igual ao "sk_"/"ghp_" que Stripe/GitHub usam: não é segredo (não ajuda
// ninguém a adivinhar a chave), mas ajuda a reconhecer de relance o tipo
// de credencial num log ou num commit acidental, e permite rejeitar de
// cara uma string que claramente não é uma API key deste serviço.
const Prefix = "ak_"

// randomSecretBytes segue o mesmo raciocínio do refresh: 256 bits é bem
// acima do que força bruta online consegue tentar.
const randomSecretBytes = 32

// Key é uma API key tal como persistida — nunca guarda o segredo em si.
type Key struct {
	ID        string
	Owner     string
	KeyHash   string
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

// Store é a persistência necessária pelo Manager.
type Store interface {
	Create(ctx context.Context, k Key) error
	FindByHash(ctx context.Context, keyHash string) (Key, bool, error)
	// Revoke marca a chave id como revogada, mas só se owner bater --
	// devolve found=false tanto se a chave não existir quanto se existir
	// mas pertencer a outro dono, de propósito: quem chama não deve
	// conseguir distinguir os dois casos (evita confirmar pra um atacante
	// que um ID de chave alheio existe).
	Revoke(ctx context.Context, id, owner string, revokedAt time.Time) (found bool, err error)
	// ListByOwner devolve todas as chaves (ativas ou não) pertencentes a
	// owner, mais recentes primeiro -- nunca inclui o segredo em si (só o
	// hash é persistido, e nem esse é devolvido por aqui).
	ListByOwner(ctx context.Context, owner string) ([]Key, error)
}

// ErrInvalid é devolvido por Verify quando a chave apresentada não existe,
// está revogada, ou expirou.
var ErrInvalid = errors.New("apikey: chave inválida, revogada ou expirada")

// ErrNotFound é devolvido por Revoke quando a chave não existe ou não
// pertence ao dono informado.
var ErrNotFound = errors.New("apikey: chave não encontrada")

// Manager emite, valida e revoga API keys.
type Manager struct {
	store Store
}

// NewManager cria um Manager que persiste em store.
func NewManager(store Store) *Manager {
	return &Manager{store: store}
}

// Issue cria uma nova API key para owner (tipicamente um user ID, mas pode
// ser o nome de um serviço para chaves puramente M2M não ligadas a uma
// conta), com os scopes informados. ttl nil significa sem expiração.
// Devolve o segredo em texto plano — a única vez que ele existe fora do
// hash guardado.
func (m *Manager) Issue(ctx context.Context, owner string, scopes []string, ttl *time.Duration) (plaintext string, _ Key, err error) {
	id, err := idgen.New()
	if err != nil {
		return "", Key{}, fmt.Errorf("apikey: gerando id: %w", err)
	}
	secret, err := opaquetoken.New(randomSecretBytes)
	if err != nil {
		return "", Key{}, fmt.Errorf("apikey: gerando segredo: %w", err)
	}
	plaintext = Prefix + secret

	k := Key{
		ID:        id,
		Owner:     owner,
		KeyHash:   opaquetoken.Hash(plaintext),
		Scopes:    scopes,
		CreatedAt: time.Now(),
	}
	if ttl != nil {
		expiresAt := k.CreatedAt.Add(*ttl)
		k.ExpiresAt = &expiresAt
	}

	if err := m.store.Create(ctx, k); err != nil {
		return "", Key{}, fmt.Errorf("apikey: persistindo chave: %w", err)
	}
	return plaintext, k, nil
}

// Verify confirma que presented é uma API key válida, não revogada e não
// expirada, devolvendo os seus metadados (dono, scopes) -- é isto que um
// serviço de recursos chama para autenticar uma chamada M2M.
func (m *Manager) Verify(ctx context.Context, presented string) (Key, error) {
	if !strings.HasPrefix(presented, Prefix) {
		return Key{}, ErrInvalid
	}

	found, ok, err := m.store.FindByHash(ctx, opaquetoken.Hash(presented))
	if err != nil {
		return Key{}, fmt.Errorf("apikey: procurando chave: %w", err)
	}
	if !ok {
		return Key{}, ErrInvalid
	}
	if found.RevokedAt != nil {
		return Key{}, ErrInvalid
	}
	if found.ExpiresAt != nil && time.Now().After(*found.ExpiresAt) {
		return Key{}, ErrInvalid
	}
	return found, nil
}

// Revoke desativa a chave id, desde que pertença a owner.
func (m *Manager) Revoke(ctx context.Context, id, owner string) error {
	found, err := m.store.Revoke(ctx, id, owner, time.Now())
	if err != nil {
		return fmt.Errorf("apikey: revogando: %w", err)
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// List devolve as chaves de owner ("as minhas chaves") -- inclui
// revogadas e expiradas, deixando quem chama decidir o que mostrar (o
// endpoint HTTP marca cada uma como active/revoked/expired).
func (m *Manager) List(ctx context.Context, owner string) ([]Key, error) {
	keys, err := m.store.ListByOwner(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("apikey: listando chaves: %w", err)
	}
	return keys, nil
}
