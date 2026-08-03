package apikey

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu   sync.Mutex
	keys map[string]Key // por KeyHash
}

func newMemStore() *memStore {
	return &memStore{keys: make(map[string]Key)}
}

func (s *memStore) Create(_ context.Context, k Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[k.KeyHash] = k
	return nil
}

func (s *memStore) FindByHash(_ context.Context, keyHash string) (Key, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[keyHash]
	return k, ok, nil
}

func (s *memStore) Revoke(_ context.Context, id, owner string, revokedAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, k := range s.keys {
		if k.ID == id && k.Owner == owner {
			k.RevokedAt = &revokedAt
			s.keys[hash] = k
			return true, nil
		}
	}
	return false, nil
}

func (s *memStore) ListByOwner(_ context.Context, owner string) ([]Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []Key
	for _, k := range s.keys {
		if k.Owner == owner {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func TestIssueAndVerify(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore())

	plaintext, created, err := m.Issue(ctx, "user-1", []string{"read:x", "write:x"}, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if plaintext == "" || created.ID == "" {
		t.Fatalf("Issue devolveu valores vazios: plaintext=%q key=%+v", plaintext, created)
	}

	found, err := m.Verify(ctx, plaintext)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if found.Owner != "user-1" || len(found.Scopes) != 2 {
		t.Fatalf("Verify devolveu dados inesperados: %+v", found)
	}
}

func TestVerifyRejectsUnknownKey(t *testing.T) {
	m := NewManager(newMemStore())
	if _, err := m.Verify(context.Background(), Prefix+"nao-existe"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid, got %v", err)
	}
}

func TestVerifyRejectsWrongPrefix(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore())
	plaintext, _, err := m.Issue(ctx, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Uma string qualquer sem o prefixo "ak_" nunca deve nem chegar a
	// bater no store -- é um caminho comum pra alguém confundir um
	// access token JWT com uma API key.
	withoutPrefix := plaintext[len(Prefix):]
	if _, err := m.Verify(ctx, withoutPrefix); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid pra chave sem prefixo, got %v", err)
	}
}

func TestVerifyRejectsExpiredKey(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore())
	past := -time.Hour
	plaintext, _, err := m.Issue(ctx, "user-1", nil, &past)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := m.Verify(ctx, plaintext); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid pra chave expirada, got %v", err)
	}
}

func TestVerifyAcceptsKeyWithoutExpiry(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore())
	plaintext, _, err := m.Issue(ctx, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := m.Verify(ctx, plaintext); err != nil {
		t.Fatalf("chave sem expiração devia ser válida indefinidamente: %v", err)
	}
}

func TestRevokeThenVerifyFails(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore())
	plaintext, created, err := m.Issue(ctx, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := m.Revoke(ctx, created.ID, "user-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := m.Verify(ctx, plaintext); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid após revogação, got %v", err)
	}
}

func TestListReturnsOnlyOwnerKeys(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore())
	if _, _, err := m.Issue(ctx, "user-1", []string{"read:x"}, nil); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, _, err := m.Issue(ctx, "user-1", []string{"write:x"}, nil); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, _, err := m.Issue(ctx, "user-2", nil, nil); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	keys, err := m.List(ctx, "user-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("esperava 2 chaves de user-1, veio %d: %+v", len(keys), keys)
	}
}

func TestRevokeRejectsWrongOwner(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore())
	_, created, err := m.Issue(ctx, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := m.Revoke(ctx, created.ID, "user-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("esperava ErrNotFound ao revogar chave de outro dono, got %v", err)
	}
}

func TestRevokeUnknownID(t *testing.T) {
	m := NewManager(newMemStore())
	if err := m.Revoke(context.Background(), "nao-existe", "user-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("esperava ErrNotFound, got %v", err)
	}
}

func TestIssueGeneratesUniqueKeys(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore())
	a, _, err := m.Issue(ctx, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	b, _, err := m.Issue(ctx, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if a == b {
		t.Fatal("duas chaves emitidas vieram idênticas")
	}
}
