package httpapi

import (
	"net/http"
	"testing"

	"auth/internal/store"
)

// Registo fechado: só o primeiro administrador (o e-mail do bootstrap,
// enquanto não há super-admin) se regista -- e fica logo super-admin.
func TestClosedRegistrationOnlyLetsTheFirstAdminIn(t *testing.T) {
	deps := newTestDeps()
	deps.handler.WithRegistration(false, "admin@x.pt")
	mux := deps.handler.Routes()

	if rec := doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "qualquer@x.pt", Password: "senha-forte"}); rec.Code != http.StatusForbidden {
		t.Errorf("registo de outra pessoa: %d, queria 403", rec.Code)
	}
	if rec := doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ADMIN@x.pt", Password: "senha-forte"}); rec.Code != http.StatusCreated {
		t.Fatalf("primeiro admin: %d %s", rec.Code, rec.Body)
	}
	if u, _, _ := deps.users.FindUserByEmail(t.Context(), "ADMIN@x.pt"); u.Role != store.RoleSuperAdmin {
		t.Errorf("o primeiro admin ficou com role %q", u.Role)
	}
	// Já há super-admin: o e-mail do bootstrap também já não se regista.
	if rec := doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "admin@x.pt", Password: "outra-senha"}); rec.Code != http.StatusForbidden {
		t.Errorf("bootstrap depois de haver super-admin: %d, queria 403", rec.Code)
	}
}

func TestAdminSetsPasswordAndUserChangesOwn(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	admin := loginAndGetAccessTokenWithRole(t, mux, deps.users, "admin@x.pt", store.RoleSuperAdmin)
	user := loginAndGetAccessToken(t, mux, "ana@x.pt")
	ana, _, _ := deps.users.FindUserByEmail(t.Context(), "ana@x.pt")

	// Só um super-admin define a password de outra conta.
	if rec := doAuthedRequest(t, mux, http.MethodPut, "/v1/admin/users/"+ana.ID+"/password", user, setPasswordRequest{Password: "nova-senha-1"}); rec.Code != http.StatusForbidden {
		t.Errorf("utilizador normal a definir passwords: %d", rec.Code)
	}
	if rec := doAuthedRequest(t, mux, http.MethodPut, "/v1/admin/users/"+ana.ID+"/password", admin, setPasswordRequest{Password: "curta"}); rec.Code != http.StatusBadRequest {
		t.Errorf("password curta: %d", rec.Code)
	}
	if rec := doAuthedRequest(t, mux, http.MethodPut, "/v1/admin/users/"+ana.ID+"/password", admin, setPasswordRequest{Password: "nova-senha-1"}); rec.Code != http.StatusNoContent {
		t.Fatalf("admin define password: %d %s", rec.Code, rec.Body)
	}
	if !deps.users.revokedSessions[ana.ID] {
		t.Error("as sessões da conta não foram revogadas")
	}
	if rec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "ana@x.pt", Password: "nova-senha-1"}); rec.Code != http.StatusOK {
		t.Errorf("login com a password nova: %d", rec.Code)
	}
	if rec := doAuthedRequest(t, mux, http.MethodPut, "/v1/admin/users/nao-existe/password", admin, setPasswordRequest{Password: "nova-senha-1"}); rec.Code != http.StatusNotFound {
		t.Errorf("conta inexistente: %d", rec.Code)
	}

	// A própria pessoa troca a sua, com a atual.
	if rec := doAuthedRequest(t, mux, http.MethodPost, "/v1/me/password", user, changePasswordRequest{CurrentPassword: "errada", NewPassword: "nova-senha-2"}); rec.Code != http.StatusForbidden {
		t.Errorf("com a password atual errada: %d", rec.Code)
	}
	if rec := doAuthedRequest(t, mux, http.MethodPost, "/v1/me/password", user, changePasswordRequest{CurrentPassword: "nova-senha-1", NewPassword: "nova-senha-2"}); rec.Code != http.StatusNoContent {
		t.Fatalf("trocar a própria: %d %s", rec.Code, rec.Body)
	}
	if rec := doRequest(t, mux, http.MethodPost, "/v1/login", loginRequest{Email: "ana@x.pt", Password: "nova-senha-2"}); rec.Code != http.StatusOK {
		t.Errorf("login com a password trocada: %d", rec.Code)
	}
}
