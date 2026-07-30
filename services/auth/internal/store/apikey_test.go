package store

import (
	"context"
	"testing"
	"time"

	"auth/internal/apikey"
)

func TestAPIKeyLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	manager := apikey.NewManager(db.APIKeyStore())

	plaintext, created, err := manager.Issue(ctx, "user-1", []string{"read:x", "write:x"}, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	found, err := manager.Verify(ctx, plaintext)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if found.Owner != "user-1" || len(found.Scopes) != 2 {
		t.Fatalf("Verify devolveu dados inesperados: %+v", found)
	}

	if err := manager.Revoke(ctx, created.ID, "user-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := manager.Verify(ctx, plaintext); err != apikey.ErrInvalid {
		t.Fatalf("esperava ErrInvalid após revogação, got %v", err)
	}
}

func TestAPIKeyRevokeRejectsWrongOwner(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	manager := apikey.NewManager(db.APIKeyStore())

	_, created, err := manager.Issue(ctx, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := manager.Revoke(ctx, created.ID, "user-2"); err != apikey.ErrNotFound {
		t.Fatalf("esperava ErrNotFound, got %v", err)
	}
}

func TestAPIKeyWithoutScopesRoundTripsAsEmpty(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	manager := apikey.NewManager(db.APIKeyStore())

	plaintext, _, err := manager.Issue(ctx, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	found, err := manager.Verify(ctx, plaintext)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(found.Scopes) != 0 {
		t.Fatalf("esperava scopes vazios, got %v", found.Scopes)
	}
}

func TestAPIKeyExpiry(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	manager := apikey.NewManager(db.APIKeyStore())

	past := -time.Hour
	plaintext, _, err := manager.Issue(ctx, "user-1", nil, &past)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := manager.Verify(ctx, plaintext); err != apikey.ErrInvalid {
		t.Fatalf("esperava ErrInvalid pra chave expirada, got %v", err)
	}
}
