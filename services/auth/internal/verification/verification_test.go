package verification

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu            sync.Mutex
	tokens        map[string]Verification // por TokenHash
	verifiedUsers map[string]bool
}

func newMemStore() *memStore {
	return &memStore{tokens: map[string]Verification{}, verifiedUsers: map[string]bool{}}
}

func (s *memStore) Create(_ context.Context, v Verification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[v.TokenHash] = v
	return nil
}

func (s *memStore) FindByHash(_ context.Context, tokenHash string) (Verification, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.tokens[tokenHash]
	return v, ok, nil
}

func (s *memStore) Consume(_ context.Context, id string, consumedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, v := range s.tokens {
		if v.ID == id {
			v.ConsumedAt = &consumedAt
			s.tokens[hash] = v
			return nil
		}
	}
	return nil
}

func (s *memStore) MarkUserVerified(_ context.Context, userID string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifiedUsers[userID] = true
	return nil
}

func TestIssueAndVerify(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	m := NewManager(store, time.Hour)

	token, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	userID, err := m.Verify(ctx, token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("userID = %q, want user-1", userID)
	}
	if !store.verifiedUsers["user-1"] {
		t.Fatal("MarkUserVerified não foi chamado")
	}
}

func TestVerifyRejectsUnknownToken(t *testing.T) {
	m := NewManager(newMemStore(), time.Hour)
	if _, err := m.Verify(context.Background(), "nao-existe"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid, got %v", err)
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), -time.Hour) // já nasce expirado

	token, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := m.Verify(ctx, token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid pra token expirado, got %v", err)
	}
}

func TestVerifyRejectsAlreadyConsumedToken(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newMemStore(), time.Hour)

	token, err := m.Issue(ctx, "user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := m.Verify(ctx, token); err != nil {
		t.Fatalf("primeiro Verify: %v", err)
	}
	if _, err := m.Verify(ctx, token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("esperava ErrInvalid ao reusar um token já consumido, got %v", err)
	}
}

func TestIssueGeneratesUniqueTokens(t *testing.T) {
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
		t.Fatal("dois Issue devolveram o mesmo token")
	}
}
