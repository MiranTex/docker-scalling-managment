package platformapi

import (
	"net/http"
	"strconv"
)

func (a *API) listNodes(w http.ResponseWriter, r *http.Request, _ principal) {
	nodes, err := a.svc.ListNodes(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

// updateNode: por agora só {"schedulable": bool} -- tirar um nó da
// colocação de réplicas novas (manutenção) e voltar a pô-lo.
func (a *API) updateNode(w http.ResponseWriter, r *http.Request, who principal) {
	var body struct {
		Schedulable *bool `json:"schedulable"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Schedulable == nil {
		writeError(w, http.StatusBadRequest, "invalid", "falta \"schedulable\"")
		return
	}
	name := r.PathValue("node")
	n, err := a.svc.SetNodeSchedulable(r.Context(), name, *body.Schedulable)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "node.schedulable", name, strconv.FormatBool(*body.Schedulable))
	writeJSON(w, http.StatusOK, n)
}
