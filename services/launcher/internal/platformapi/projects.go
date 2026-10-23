package platformapi

import (
	"context"
	"net/http"

	"launcher/internal/platform"
	"launcher/internal/store"
)

// listProjects: um admin da plataforma vê todos; qualquer outra conta só
// os projetos de que é membro.
func (a *API) listProjects(w http.ResponseWriter, r *http.Request, who principal) {
	list := a.svc.ListProjects
	if !who.PlatformAdmin {
		list = func(ctx context.Context) ([]store.Project, error) { return a.svc.ListProjectsForUser(ctx, who.Subject) }
	}
	projects, err := list(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (a *API) createProject(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.CreateProjectInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.svc.CreateProject(r.Context(), in, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// projectDetail é o projeto com os seus ambientes -- é o que a página de
// um projeto precisa, numa chamada só.
type projectDetail struct {
	store.Project
	Environments []store.Environment `json:"environments"`
	// APIURL é onde o CI chama a API (ver platform.APIURL).
	APIURL string `json:"apiUrl,omitempty"`
	// MyRole é o que quem pede pode fazer aqui: "platform-admin" ou o seu
	// papel de membro -- para a UI esconder o que não pode usar (quem
	// decide é sempre a API).
	MyRole string `json:"myRole"`
}

func (a *API) getProject(w http.ResponseWriter, r *http.Request, who principal) {
	slug := r.PathValue("project")
	p, err := a.svc.GetProject(r.Context(), slug)
	if err != nil {
		a.fail(w, err)
		return
	}
	envs, err := a.svc.ListEnvironments(r.Context(), slug)
	if err != nil {
		a.fail(w, err)
		return
	}
	myRole := who.Role
	if who.PlatformAdmin {
		myRole = "platform-admin"
	}
	writeJSON(w, http.StatusOK, projectDetail{Project: p, Environments: envs, APIURL: a.svc.APIURL(), MyRole: myRole})
}

func (a *API) updateProject(w http.ResponseWriter, r *http.Request, _ principal) {
	var in platform.UpdateProjectInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.svc.UpdateProject(r.Context(), r.PathValue("project"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// deleteProject: ?purge=true apaga também os volumes de dados (BDs dos
// addons). Sem isso os dados ficam no host, para recuperar se for engano.
func (a *API) deleteProject(w http.ResponseWriter, r *http.Request, _ principal) {
	if err := a.svc.DeleteProject(r.Context(), r.PathValue("project"), r.URL.Query().Get("purge") == "true"); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) createEnvironment(w http.ResponseWriter, r *http.Request, who principal) {
	var in struct {
		Slug string `json:"slug"`
	}
	if !decode(w, r, &in) {
		return
	}
	e, err := a.svc.CreateEnvironment(r.Context(), r.PathValue("project"), in.Slug, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (a *API) deleteEnvironment(w http.ResponseWriter, r *http.Request, _ principal) {
	if err := a.svc.DeleteEnvironment(r.Context(), r.PathValue("project"), r.PathValue("env"), r.URL.Query().Get("purge") == "true"); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listSecrets(w http.ResponseWriter, r *http.Request, _ principal) {
	secrets, err := a.svc.ListSecrets(r.Context(), r.PathValue("project"), r.PathValue("env"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, secrets)
}

// putSecret é write-only: o valor nunca volta a sair por esta API.
func (a *API) putSecret(w http.ResponseWriter, r *http.Request, who principal) {
	var in struct {
		Value string `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.svc.PutSecret(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("name"), in.Value, who.Subject); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteSecret(w http.ResponseWriter, r *http.Request, _ principal) {
	if err := a.svc.DeleteSecret(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("name")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) getNetwork(w http.ResponseWriter, r *http.Request, _ principal) {
	n, err := a.svc.GetEnvironmentNetwork(r.Context(), r.PathValue("project"), r.PathValue("env"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}
