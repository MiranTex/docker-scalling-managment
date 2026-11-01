package platformapi

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"

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

func (a *API) updateAddon(w http.ResponseWriter, r *http.Request, who principal) {
	var in struct {
		BackupEnabled  *bool `json:"backupEnabled"`
		PublicEndpoint *bool `json:"publicEndpoint"`
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
	if in.PublicEndpoint != nil {
		p, e, addon := r.PathValue("project"), r.PathValue("env"), r.PathValue("addon")
		if _, err := a.svc.SetStoragePublic(r.Context(), p, e, addon, *in.PublicEndpoint, who.Subject); err != nil {
			a.fail(w, err)
			return
		}
		a.audit.Log(who.Subject, "addon.public_endpoint", p+"/"+e+"/"+addon, fmt.Sprint(*in.PublicEndpoint))
	}
	w.WriteHeader(http.StatusNoContent)
}

func addonPath(r *http.Request) (project, env, addon string) {
	return r.PathValue("project"), r.PathValue("env"), r.PathValue("addon")
}

func (a *API) listBuckets(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, addon := addonPath(r)
	buckets, err := a.svc.ListBuckets(r.Context(), p, e, addon)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buckets)
}

func (a *API) createBucket(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.BucketInput
	if !decode(w, r, &in) {
		return
	}
	p, e, addon := addonPath(r)
	b, err := a.svc.CreateBucket(r.Context(), p, e, addon, in, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "bucket.create", p+"/"+e+"/"+addon+"/"+in.Name, fmt.Sprintf("público=%v", in.Public))
	writeJSON(w, http.StatusCreated, b)
}

func (a *API) updateBucket(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.BucketUpdate
	if !decode(w, r, &in) {
		return
	}
	p, e, addon := addonPath(r)
	b, err := a.svc.UpdateBucket(r.Context(), p, e, addon, r.PathValue("bucket"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "bucket.update", p+"/"+e+"/"+addon+"/"+b.Name, fmt.Sprintf("público=%v cors=%v", b.Public, b.CORSOrigins))
	writeJSON(w, http.StatusOK, b)
}

func (a *API) deleteBucket(w http.ResponseWriter, r *http.Request, who principal) {
	p, e, addon := addonPath(r)
	if err := a.svc.DeleteBucket(r.Context(), p, e, addon, r.PathValue("bucket")); err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "bucket.delete", p+"/"+e+"/"+addon+"/"+r.PathValue("bucket"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) addBucketDomain(w http.ResponseWriter, r *http.Request, who principal) {
	var in struct {
		Hostname string `json:"hostname"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, e, addon := addonPath(r)
	d, err := a.svc.AddBucketDomain(r.Context(), p, e, addon, r.PathValue("bucket"), in.Hostname, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "bucket.domain", p+"/"+e+"/"+addon+"/"+r.PathValue("bucket"), "acrescentado "+d.Hostname)
	writeJSON(w, http.StatusCreated, d)
}

func (a *API) verifyBucketDomain(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, addon := addonPath(r)
	d, err := a.svc.VerifyBucketDomain(r.Context(), p, e, addon, r.PathValue("bucket"), r.PathValue("hostname"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) deleteBucketDomain(w http.ResponseWriter, r *http.Request, who principal) {
	p, e, addon := addonPath(r)
	if err := a.svc.DeleteBucketDomain(r.Context(), p, e, addon, r.PathValue("bucket"), r.PathValue("hostname")); err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "bucket.domain", p+"/"+e+"/"+addon+"/"+r.PathValue("bucket"), "removido "+r.PathValue("hostname"))
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

func (a *API) setAddonState(state string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, who principal) {
		p, e, addon := addonPath(r)
		if err := a.svc.SetAddonState(r.Context(), p, e, addon, state); err != nil {
			a.fail(w, err)
			return
		}
		a.audit.Log(who.Subject, "addon."+map[string]string{"stopped": "stop", "running": "start"}[state], p+"/"+e+"/"+addon, "")
		w.WriteHeader(http.StatusNoContent)
	}
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

func bucketTarget(r *http.Request) string {
	return r.PathValue("project") + "/" + r.PathValue("env") + "/" + r.PathValue("addon") + "/" + r.PathValue("bucket")
}

func (a *API) listFiles(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, addon := addonPath(r)
	files, err := a.svc.ListFiles(r.Context(), p, e, addon, r.PathValue("bucket"), r.URL.Query().Get("prefix"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// downloadFile serve o ficheiro como anexo, em streaming.
func (a *API) downloadFile(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, addon := addonPath(r)
	key := r.PathValue("key")
	body, size, contentType, err := a.svc.OpenFile(r.Context(), p, e, addon, r.PathValue("bucket"), key)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer body.Close()
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(key)}))
	_, _ = io.Copy(w, body)
}

// uploadFile: o corpo é o ficheiro, com Content-Length (o storage precisa
// do tamanho) e o Content-Type com que fica guardado.
func (a *API) uploadFile(w http.ResponseWriter, r *http.Request, who principal) {
	p, e, addon := addonPath(r)
	key := r.PathValue("key")
	body := http.MaxBytesReader(w, r.Body, platform.MaxUploadBytes+1)
	if err := a.svc.UploadFile(r.Context(), p, e, addon, r.PathValue("bucket"), key, body, r.ContentLength, r.Header.Get("Content-Type")); err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "bucket.file_upload", bucketTarget(r)+"/"+key, fmt.Sprintf("%d bytes", r.ContentLength))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteFile(w http.ResponseWriter, r *http.Request, who principal) {
	p, e, addon := addonPath(r)
	key := r.PathValue("key")
	if err := a.svc.DeleteFile(r.Context(), p, e, addon, r.PathValue("bucket"), key); err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "bucket.file_delete", bucketTarget(r)+"/"+key, "")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) fileLink(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, addon := addonPath(r)
	link, err := a.svc.FileLink(r.Context(), p, e, addon, r.PathValue("bucket"), r.URL.Query().Get("key"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": link})
}
