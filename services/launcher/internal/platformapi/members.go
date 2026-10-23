package platformapi

import (
	"errors"
	"net/http"
	"strings"

	"launcher/internal/authusers"
)

func (a *API) listMembers(w http.ResponseWriter, r *http.Request, _ principal) {
	members, err := a.svc.ListMembers(r.Context(), r.PathValue("project"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, members)
}

// addMember resolve o email no auth com o access token de quem pede, e
// acrescenta essa conta ao projeto (ou muda-lhe o papel, se já for membro).
func (a *API) addMember(w http.ResponseWriter, r *http.Request, who principal) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Email) == "" {
		writeError(w, http.StatusBadRequest, "invalid", "email é obrigatório")
		return
	}
	if a.users == nil || who.bearer == "" {
		writeError(w, http.StatusBadRequest, "invalid", "adicionar membros precisa de uma sessão (access token), não de uma API key")
		return
	}
	userID, email, err := a.users.Resolve(r.Context(), who.bearer, in.Email)
	if errors.Is(err, authusers.ErrNoAccount) {
		writeError(w, http.StatusNotFound, "no_account", err.Error())
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	m, err := a.svc.PutMember(r.Context(), r.PathValue("project"), userID, email, in.Role, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.kickGrafana()
	writeJSON(w, http.StatusCreated, m)
}

func (a *API) setMemberRole(w http.ResponseWriter, r *http.Request, _ principal) {
	var in struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	m, err := a.svc.SetMemberRole(r.Context(), r.PathValue("project"), r.PathValue("user"), in.Role)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.kickGrafana()
	writeJSON(w, http.StatusOK, m)
}

func (a *API) removeMember(w http.ResponseWriter, r *http.Request, _ principal) {
	if err := a.svc.RemoveMember(r.Context(), r.PathValue("project"), r.PathValue("user")); err != nil {
		a.fail(w, err)
		return
	}
	a.kickGrafana()
	w.WriteHeader(http.StatusNoContent)
}
