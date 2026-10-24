package platformapi

import (
	"net/http"

	"launcher/internal/platform"
)

func (a *API) listAddons(w http.ResponseWriter, r *http.Request, _ principal) {
	addons, err := a.svc.ListAddons(r.Context(), r.PathValue("project"), r.PathValue("env"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, addons)
}

func (a *API) createAddon(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.CreateAddonInput
	if !decode(w, r, &in) {
		return
	}
	addon, err := a.svc.CreateAddon(r.Context(), r.PathValue("project"), r.PathValue("env"), in, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.kickGrafana()
	writeJSON(w, http.StatusCreated, addon)
}

func (a *API) updateAddon(w http.ResponseWriter, r *http.Request, _ principal) {
	var in struct {
		BackupEnabled *bool `json:"backupEnabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.BackupEnabled != nil {
		if err := a.svc.SetAddonBackups(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("addon"), *in.BackupEnabled); err != nil {
			a.fail(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteAddon: ?purge=true apaga também os volumes de dados e backups.
func (a *API) deleteAddon(w http.ResponseWriter, r *http.Request, _ principal) {
	if err := a.svc.DeleteAddon(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("addon"), r.URL.Query().Get("purge") == "true"); err != nil {
		a.fail(w, err)
		return
	}
	a.kickGrafana()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listBackups(w http.ResponseWriter, r *http.Request, _ principal) {
	backups, err := a.svc.ListAddonBackups(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("addon"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, backups)
}

func (a *API) createBackup(w http.ResponseWriter, r *http.Request, who principal) {
	job, err := a.svc.BackupAddon(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("addon"), who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) restoreBackup(w http.ResponseWriter, r *http.Request, who principal) {
	var in struct {
		File string `json:"file"`
	}
	if !decode(w, r, &in) {
		return
	}
	job, err := a.svc.RestoreAddon(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("addon"), in.File, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request, _ principal) {
	jobs, err := a.svc.ListJobs(r.Context(), r.PathValue("project"), r.PathValue("env"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request, _ principal) {
	job, err := a.svc.GetJob(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("job"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *API) projectUsage(w http.ResponseWriter, r *http.Request, _ principal) {
	u, err := a.svc.ProjectUsage(r.Context(), r.PathValue("project"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (a *API) listVolumes(w http.ResponseWriter, r *http.Request, _ principal) {
	vols, err := a.svc.ListVolumes(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vols)
}

func (a *API) deleteVolume(w http.ResponseWriter, r *http.Request, _ principal) {
	if err := a.svc.DeleteVolume(r.Context(), r.PathValue("name")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) cancelJob(w http.ResponseWriter, r *http.Request, who principal) {
	if err := a.svc.CancelJob(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("job"), who.Subject); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
