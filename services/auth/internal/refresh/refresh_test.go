package refresh

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// memStore é um fake em memória de Store — permite testar toda a lógica de
// rotação/deteção de reuso do Manager sem precisar de Postgres. A
// implementação real (Postgres) é testada à parte, contra uma base de
// dados de verdade (ver internal/store).
type memStore struct {
	mu     sync.Mutex
	tokens map[string]Token // por TokenHash
}

func newMemStore() *memStore {
	return &memStore{tokens: make(map[string]Token)}
}

func (s *memStore) Create(_ context.Context, t Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[t.TokenHash] = t
	return nil
}

func (s *memStore) FindByHash(_ context.Context, tokenHash string) (Token, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[tokenHash]
	return t, ok, nil
}

func (s *memStore) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, t := range s.tokens {
		if t.ID == id {
			t.RevokedAt = &revokedAt
			s.tokens[hash] = t
			return nil
		}
	}
	return nil
}

func (s *memStore) RevokeFamily(_ context.Context, familyID string, revokedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, t := range s.tokens {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			t.RevokedAt = &revokedAt
			s.tokens[hash] = t
		}
	}
	return nil
}

func TestIssueAndRotate(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), time.Hour)

	first, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	second, userID, err := m.Rotate(ctx, first)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("userID = %q, want user-1", userID)
	}
	if second == first {
		t.Fatal("Rotate devolveu o mesmo token, esperava um novo")
	}
}

func TestRotateRejectsUnknownToken(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), time.Hour)

	if _, _, err := m.Rotate(ctx, "token-que-nunca-existiu"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid, got %v", err)
	}
}

func TestRotateRejectsExpiredToken(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), -time.Hour) // já nasce expirado

	first, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, _, err := m.Rotate(ctx, first); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid pra token expirado, got %v", err)
	}
}

// TestRotateDetectsReuseAndRevokesFamily é o teste mais importante deste
// pacote: simula um token roubado. O dono legítimo já rodou o token (usou
// `second`), mas um atacante que tinha copiado `first` antes disso tenta
// usá-lo agora -- deve ser rejeitado com ErrReuseDetected E a sessão
// inteira (incluindo `second`, o token legítimo atual) deve morrer junto.
func TestRotateDetectsReuseAndRevokesFamily(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), time.Hour)

	first, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	second, _, err := m.Rotate(ctx, first)
	if err != nil {
		t.Fatalf("Rotate legítimo: %v", err)
	}

	// Atacante reapresenta o token antigo (já revogado pela rotação acima).
	if _, _, err := m.Rotate(ctx, first); !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("esperava ErrReuseDetected, got %v", err)
	}

	// A sessão inteira devia ter morrido -- inclusive o token legítimo
	// atual, que ninguém tinha usado indevidamente.
	if _, _, err := m.Rotate(ctx, second); !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("token legítimo da mesma família devia estar revogado após deteção de reuso, got %v", err)
	}
}

func TestRevokeIsIdempotentAndEndsSession(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), time.Hour)

	first, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := m.Revoke(ctx, first); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Chamar de novo não deve dar erro (idempotente).
	if err := m.Revoke(ctx, first); err != nil {
		t.Fatalf("segundo Revoke: %v", err)
	}
	// E o token revogado não deve mais rodar.
	if _, _, err := m.Rotate(ctx, first); err == nil {
		t.Fatal("token revogado por logout ainda conseguiu rodar")
	}
}

func TestRevokeOfUnknownTokenIsNotAnError(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), time.Hour)

	if err := m.Revoke(ctx, "nunca existiu"); err != nil {
		t.Fatalf("Revoke de token desconhecido deveria ser no-op, got %v", err)
	}
}

func TestIssueGeneratesUniqueSecrets(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), time.Hour)

	a, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	b, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if a == b {
		t.Fatal("dois Issue devolveram o mesmo segredo")
	}
}
