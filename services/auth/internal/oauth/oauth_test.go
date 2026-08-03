package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"auth/internal/store"
)

// memStore é um fake em memória de Store -- permite testar toda a
// política de account linking (ligar vs. criar vs. recusar) sem precisar
// de Postgres. A camada real (store.DB) é testada à parte, contra
// Postgres de verdade.
type memStore struct {
	mu         sync.Mutex
	nextID     int
	users      map[string]store.User // por ID
	emailIndex map[string]string     // email -> user ID
	identities map[string]string     // "provider|providerUserID" -> user ID
	reclaimed  []string              // IDs pra quem ReclaimUnverifiedAccount foi chamado
}

func newMemStore() *memStore {
	return &memStore{
		users:      map[string]store.User{},
		emailIndex: map[string]string{},
		identities: map[string]string{},
	}
}

func (s *memStore) FindUserByOAuthIdentity(_ context.Context, provider, providerUserID string) (store.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID, ok := s.identities[provider+"|"+providerUserID]
	if !ok {
		return store.User{}, false, nil
	}
	return s.users[userID], true, nil
}

func (s *memStore) FindUserByEmail(_ context.Context, email string) (store.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID, ok := s.emailIndex[email]
	if !ok {
		return store.User{}, false, nil
	}
	return s.users[userID], true, nil
}

func (s *memStore) CreateUserWithoutPassword(_ context.Context, email string, verified bool) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.emailIndex[email]; exists {
		return store.User{}, store.ErrEmailTaken
	}
	s.nextID++
	u := store.User{ID: fmt.Sprintf("user-%d", s.nextID), Email: email}
	if verified {
		now := time.Now()
		u.EmailVerifiedAt = &now
	}
	s.users[u.ID] = u
	s.emailIndex[email] = u.ID
	return u, nil
}

func (s *memStore) ReclaimUnverifiedAccount(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reclaimed = append(s.reclaimed, userID)
	u := s.users[userID]
	now := time.Now()
	u.EmailVerifiedAt = &now
	s.users[userID] = u
	return nil
}

func (s *memStore) LinkOAuthIdentity(_ context.Context, userID, provider, providerUserID, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities[provider+"|"+providerUserID] = userID
	return nil
}

// newTestProvider sobe um IdP falso (httptest.Server) servindo /token e
// /userinfo, e devolve um Provider "test" apontando pra ele -- assim o
// fluxo OAuth2 inteiro (exchange + fetch identity) é exercitado de
// verdade, sem depender de rede nem de credenciais de um provider real.
func newTestProvider(t *testing.T, identity Identity) Provider {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "bearer",
		})
	})

	var userInfoURL string
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sub":            identity.ProviderUserID,
			"email":          identity.Email,
			"email_verified": identity.EmailVerified,
		})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	userInfoURL = server.URL + "/userinfo"

	endpoint := oauth2.Endpoint{AuthURL: server.URL + "/authorize", TokenURL: server.URL + "/token"}
	return NewProvider("test", endpoint, "client-id", "client-secret", "http://localhost/callback", []string{"openid"}, fetchGoogleIdentity(userInfoURL))
}

func TestLoginCreatesNewUserOnFirstSocialLogin(t *testing.T) {
	ctx := context.Background()
	provider := newTestProvider(t, Identity{ProviderUserID: "ext-1", Email: "ana@example.test", EmailVerified: true})
	m := NewManager(newMemStore(), provider)

	user, err := m.Login(ctx, "test", "any-code")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if user.Email != "ana@example.test" || user.ID == "" {
		t.Fatalf("utilizador inesperado: %+v", user)
	}
}

func TestLoginIsIdempotentForSameIdentity(t *testing.T) {
	ctx := context.Background()
	provider := newTestProvider(t, Identity{ProviderUserID: "ext-1", Email: "ana@example.test", EmailVerified: true})
	m := NewManager(newMemStore(), provider)

	first, err := m.Login(ctx, "test", "code-1")
	if err != nil {
		t.Fatalf("primeiro Login: %v", err)
	}
	second, err := m.Login(ctx, "test", "code-2")
	if err != nil {
		t.Fatalf("segundo Login: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("logins da mesma identidade devolveram contas diferentes: %q != %q", first.ID, second.ID)
	}
}

func TestLoginLinksToExistingVerifiedAccountWithoutReclaiming(t *testing.T) {
	ctx := context.Background()
	memStore := newMemStore()
	existing, err := memStore.CreateUserWithoutPassword(ctx, "ana@example.test", true)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	provider := newTestProvider(t, Identity{ProviderUserID: "ext-1", Email: "ana@example.test", EmailVerified: true})
	m := NewManager(memStore, provider)

	user, err := m.Login(ctx, "test", "any-code")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if user.ID != existing.ID {
		t.Fatalf("devia ligar à conta existente %q, devolveu %q", existing.ID, user.ID)
	}
	if len(memStore.reclaimed) != 0 {
		t.Fatalf("uma conta já verificada não devia ser reclamada, reclaimed=%v", memStore.reclaimed)
	}
}

// TestLoginReclaimsUnverifiedSquattedAccount é o teste da correção da
// vulnerabilidade: um atacante pré-registou (por senha) o e-mail de outra
// pessoa, essa conta nunca foi verificada, e agora a vítima de verdade
// entra via um provider que CONFIRMA o e-mail -- a conta tem de ser
// reclamada (credencial de senha do atacante derrubada), não só ligada
// como se já pertencesse legitimamente a quem está a entrar agora.
func TestLoginReclaimsUnverifiedSquattedAccount(t *testing.T) {
	ctx := context.Background()
	memStore := newMemStore()
	squatted, err := memStore.CreateUserWithoutPassword(ctx, "vitima@example.test", false)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	provider := newTestProvider(t, Identity{ProviderUserID: "ext-legitimo", Email: "vitima@example.test", EmailVerified: true})
	m := NewManager(memStore, provider)

	user, err := m.Login(ctx, "test", "any-code")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if user.ID != squatted.ID {
		t.Fatalf("devia ligar à conta squatted %q, devolveu %q", squatted.ID, user.ID)
	}
	if len(memStore.reclaimed) != 1 || memStore.reclaimed[0] != squatted.ID {
		t.Fatalf("esperava ReclaimUnverifiedAccount chamado para %q, reclaimed=%v", squatted.ID, memStore.reclaimed)
	}
}

// TestLoginRefusesToLinkUnverifiedEmail é o teste de segurança mais
// importante deste pacote: um provider que não confirma o e-mail não pode
// fazer login social assumir silenciosamente a conta de outra pessoa só
// porque reportou o mesmo endereço.
func TestLoginRefusesToLinkUnverifiedEmail(t *testing.T) {
	ctx := context.Background()
	memStore := newMemStore()
	if _, err := memStore.CreateUserWithoutPassword(ctx, "vitima@example.test", false); err != nil {
		t.Fatalf("setup: %v", err)
	}

	provider := newTestProvider(t, Identity{ProviderUserID: "atacante-ext-id", Email: "vitima@example.test", EmailVerified: false})
	m := NewManager(memStore, provider)

	if _, err := m.Login(ctx, "test", "any-code"); !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("esperava ErrEmailNotVerified, got %v", err)
	}
}

func TestLoginRejectsUnknownProvider(t *testing.T) {
	m := NewManager(newMemStore())
	if _, err := m.Login(context.Background(), "nao-existe", "code"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("esperava ErrUnknownProvider, got %v", err)
	}
}

func TestAuthCodeURLRejectsUnknownProvider(t *testing.T) {
	m := NewManager(newMemStore())
	if _, err := m.AuthCodeURL("nao-existe", "state"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("esperava ErrUnknownProvider, got %v", err)
	}
}

func TestAuthCodeURLEmbedsState(t *testing.T) {
	provider := newTestProvider(t, Identity{})
	m := NewManager(newMemStore(), provider)

	url, err := m.AuthCodeURL("test", "opaque-state-value")
	if err != nil {
		t.Fatalf("AuthCodeURL: %v", err)
	}
	if !strings.Contains(url, "opaque-state-value") {
		t.Fatalf("URL de autorização não contém o state: %s", url)
	}
}
