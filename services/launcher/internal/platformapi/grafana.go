package platformapi

import (
	"context"
	"net/http"
)

// Grafana é a sincronização com o Grafana (ver internal/grafanasync). nil
// desliga as rotas /v1/grafana.
type Grafana interface {
	Session(ctx context.Context, userID, email string, platformAdmin bool) error
	ProjectOrg(ctx context.Context, projectSlug string) (int64, error)
	Kick()
}

// WithGrafana liga a sincronização com o Grafana.
func (a *API) WithGrafana(g Grafana) *API {
	a.grafana = g
	return a
}

func (a *API) kickGrafana() {
	if a.grafana != nil {
		a.grafana.Kick()
	}
}

// grafanaSession é chamado pelo portal antes de servir /grafana: diz quem
// é o utilizador a partir de um access token VERIFICADO (o portal não
// verifica assinaturas) e do email que o auth tem para ele, e deixa o
// Grafana com as organizações certas para essa conta.
func (a *API) grafanaSession(w http.ResponseWriter, r *http.Request, who principal) {
	if a.grafana == nil || a.users == nil {
		writeError(w, http.StatusNotFound, "not_found", "o Grafana não está ligado a esta plataforma")
		return
	}
	if who.bearer == "" {
		writeError(w, http.StatusBadRequest, "invalid", "o Grafana precisa de uma sessão (access token)")
		return
	}
	id, email, role, err := a.users.Me(r.Context(), who.bearer)
	if err != nil || id != who.Subject {
		writeError(w, http.StatusBadGateway, "auth_unavailable", "não foi possível confirmar a conta no auth")
		return
	}
	if err := a.grafana.Session(r.Context(), id, email, isPlatformRole(role)); err != nil {
		a.logger.Error("grafana: sessão", "user", id, "err", err)
		writeError(w, http.StatusBadGateway, "grafana_unavailable", "não foi possível preparar o Grafana: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"login": id, "email": email})
}

func (a *API) projectGrafana(w http.ResponseWriter, r *http.Request, _ principal) {
	if a.grafana == nil {
		writeError(w, http.StatusNotFound, "not_found", "o Grafana não está ligado a esta plataforma")
		return
	}
	org, err := a.grafana.ProjectOrg(r.Context(), r.PathValue("project"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"orgId": org})
}
