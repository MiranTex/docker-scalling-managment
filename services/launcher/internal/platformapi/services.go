package platformapi

import (
	"net/http"

	"launcher/internal/platform"
)

func svcPath(r *http.Request) (string, string, string) {
	return r.PathValue("project"), r.PathValue("env"), r.PathValue("svc")
}

func (a *API) listServices(w http.ResponseWriter, r *http.Request, _ principal) {
	services, err := a.svc.ListServices(r.Context(), r.PathValue("project"), r.PathValue("env"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, services)
}

func (a *API) createService(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.ServiceInput
	if !decode(w, r, &in) {
		return
	}
	svc, err := a.svc.CreateService(r.Context(), r.PathValue("project"), r.PathValue("env"), in, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, svc)
}

func (a *API) getService(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, s := svcPath(r)
	svc, err := a.svc.GetService(r.Context(), p, e, s)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (a *API) updateService(w http.ResponseWriter, r *http.Request, _ principal) {
	var in platform.ServiceInput
	if !decode(w, r, &in) {
		return
	}
	p, e, s := svcPath(r)
	svc, err := a.svc.UpdateService(r.Context(), p, e, s, in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (a *API) deleteService(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, s := svcPath(r)
	if err := a.svc.DeleteService(r.Context(), p, e, s); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setServiceState(state string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, _ principal) {
		p, e, s := svcPath(r)
		if err := a.svc.SetServiceState(r.Context(), p, e, s, state); err != nil {
			a.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) listRevisions(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, s := svcPath(r)
	revs, err := a.svc.ListRevisions(r.Context(), p, e, s)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, revs)
}

func (a *API) listReplicas(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, s := svcPath(r)
	replicas, err := a.svc.ListReplicas(r.Context(), p, e, s)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, replicas)
}

func (a *API) listDeployments(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, s := svcPath(r)
	deps, err := a.svc.ListDeployments(r.Context(), p, e, s)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deps)
}

func (a *API) getDeployment(w http.ResponseWriter, r *http.Request, _ principal) {
	p, e, s := svcPath(r)
	dep, err := a.svc.GetDeployment(r.Context(), p, e, s, r.PathValue("deployment"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dep)
}

func (a *API) createDeployment(w http.ResponseWriter, r *http.Request, who principal) {
	var in platform.DeployInput
	if !decode(w, r, &in) {
		return
	}
	p, e, s := svcPath(r)
	dep, err := a.svc.Deploy(r.Context(), p, e, s, in, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

func (a *API) runCommand(w http.ResponseWriter, r *http.Request, who principal) {
	var in struct {
		Cmd []string `json:"cmd"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, e, s := svcPath(r)
	job, err := a.svc.RunCommand(r.Context(), p, e, s, in.Cmd, who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) listRegistries(w http.ResponseWriter, r *http.Request, _ principal) {
	regs, err := a.svc.ListRegistries(r.Context(), r.PathValue("project"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, regs)
}

func (a *API) putRegistry(w http.ResponseWriter, r *http.Request, who principal) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.svc.PutRegistry(r.Context(), r.PathValue("project"), r.PathValue("registry"), in.Username, in.Password, who.Subject); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteRegistry(w http.ResponseWriter, r *http.Request, _ principal) {
	if err := a.svc.DeleteRegistry(r.Context(), r.PathValue("project"), r.PathValue("registry")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
