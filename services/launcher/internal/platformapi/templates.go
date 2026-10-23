package platformapi

import (
	"net/http"
	"strconv"

	"launcher/internal/platform"
)

func (a *API) listTemplates(w http.ResponseWriter, r *http.Request, _ principal) {
	list, err := a.svc.ListTemplates(r.Context(), r.PathValue("project"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) createTemplate(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.CreateTemplateInput
	if !decode(w, r, &in) {
		return
	}
	t, err := a.svc.CreateTemplate(r.Context(), r.PathValue("project"), in, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (a *API) getTemplate(w http.ResponseWriter, r *http.Request, _ principal) {
	t, err := a.svc.GetTemplate(r.Context(), r.PathValue("project"), r.PathValue("template"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *API) updateTemplateInfo(w http.ResponseWriter, r *http.Request, _ principal) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.svc.UpdateTemplateInfo(r.Context(), r.PathValue("project"), r.PathValue("template"), in.Name, in.Description); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// archiveTemplate: DELETE arquiva, não apaga -- há serviços que apontam
// para as versões dele.
func (a *API) archiveTemplate(w http.ResponseWriter, r *http.Request, _ principal) {
	if err := a.svc.ArchiveTemplate(r.Context(), r.PathValue("project"), r.PathValue("template")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) addTemplateVersion(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.TemplateVersionInput
	if !decode(w, r, &in) {
		return
	}
	v, err := a.svc.AddTemplateVersion(r.Context(), r.PathValue("project"), r.PathValue("template"), in, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// previewTemplateUpdate: ?to=N (vazio = a versão mais recente).
func (a *API) previewTemplateUpdate(w http.ResponseWriter, r *http.Request, _ principal) {
	to, _ := strconv.Atoi(r.URL.Query().Get("to"))
	p, e, s := svcPath(r)
	upd, err := a.svc.PreviewTemplateUpdate(r.Context(), p, e, s, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, upd)
}

func (a *API) applyTemplateUpdate(w http.ResponseWriter, r *http.Request, _ principal) {
	var in struct {
		To int `json:"to"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, e, s := svcPath(r)
	svc, err := a.svc.ApplyTemplateUpdate(r.Context(), p, e, s, in.To)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (a *API) saveAsTemplate(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.SaveAsTemplateInput
	if !decode(w, r, &in) {
		return
	}
	p, e, s := svcPath(r)
	t, err := a.svc.SaveServiceAsTemplate(r.Context(), p, e, s, in, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}
