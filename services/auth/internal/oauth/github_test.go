package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchGitHubIdentityPicksPrimaryVerifiedEmail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 42})
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"email": "secundario@example.test", "primary": false, "verified": true},
			{"email": "principal@example.test", "primary": true, "verified": true},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	fetch := fetchGitHubIdentity(server.URL+"/user", server.URL+"/user/emails")
	identity, err := fetch(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("fetchGitHubIdentity: %v", err)
	}
	if identity.ProviderUserID != "42" || identity.Email != "principal@example.test" || !identity.EmailVerified {
		t.Fatalf("identidade inesperada: %+v", identity)
	}
}

func TestFetchGitHubIdentityFailsWithoutPrimaryEmail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 42})
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"email": "secundario@example.test", "primary": false, "verified": true},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	fetch := fetchGitHubIdentity(server.URL+"/user", server.URL+"/user/emails")
	if _, err := fetch(context.Background(), server.Client()); err == nil {
		t.Fatal("esperava erro quando nenhum e-mail é marcado como primary")
	}
}

func TestFetchGitHubIdentityReportsUnverifiedPrimaryEmail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 42})
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"email": "nao-verificado@example.test", "primary": true, "verified": false},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	fetch := fetchGitHubIdentity(server.URL+"/user", server.URL+"/user/emails")
	identity, err := fetch(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("fetchGitHubIdentity: %v", err)
	}
	if identity.EmailVerified {
		t.Fatal("e-mail principal não verificado não devia reportar EmailVerified=true")
	}
}
