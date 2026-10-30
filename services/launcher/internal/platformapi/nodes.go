package platformapi

import (
	"net/http"
	"strconv"

	"launcher/internal/platform"
)

func (a *API) listNodes(w http.ResponseWriter, r *http.Request, _ principal) {
	nodes, err := a.svc.ListNodes(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

// updateNode: {"schedulable": bool} e/ou {"labels": {...}} -- tirar um nó da
// colocação de réplicas novas (manutenção) e voltar a pô-lo.
func (a *API) updateNode(w http.ResponseWriter, r *http.Request, who principal) {
	var body struct {
		Schedulable *bool              `json:"schedulable"`
		Labels      *map[string]string `json:"labels"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Schedulable == nil && body.Labels == nil {
		writeError(w, http.StatusBadRequest, "invalid", "falta \"schedulable\" ou \"labels\"")
		return
	}
	name := r.PathValue("node")
	var n platform.NodeView
	var err error
	if body.Labels != nil {
		if n, err = a.svc.SetNodeLabels(r.Context(), name, *body.Labels); err != nil {
			a.fail(w, err)
			return
		}
		a.audit.Log(who.Subject, "node.labels", name, platform.FormatLabels(*body.Labels))
	}
	if body.Schedulable != nil {
		if n, err = a.svc.SetNodeSchedulable(r.Context(), name, *body.Schedulable); err != nil {
			a.fail(w, err)
			return
		}
		a.audit.Log(who.Subject, "node.schedulable", name, strconv.FormatBool(*body.Schedulable))
	}
	writeJSON(w, http.StatusOK, n)
}

// drainNode: tira as réplicas do nó, uma a uma, para outros (em fundo).
func (a *API) drainNode(w http.ResponseWriter, r *http.Request, who principal) {
	name := r.PathValue("node")
	if err := a.svc.DrainNode(r.Context(), name); err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "node.drain", name, "iniciado")
	w.WriteHeader(http.StatusAccepted)
}
