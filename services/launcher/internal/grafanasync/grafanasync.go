// Package grafanasync mantém o Grafana alinhado com os projetos e os seus
// membros: uma organização por projeto (só com os dados desse projeto),
// uma organização "Plataforma" com tudo (para os admins da plataforma), e
// cada conta nas organizações a que tem direito.
//
// O isolamento está nas fontes de dados de cada organização de projeto:
//   - Prometheus pelo prom-label-proxy, com o cabeçalho X-Project fixo --
//     o proxy injeta project="<slug>" em todas as consultas;
//   - Loki com X-Scope-OrgID = slug -- o tenant onde o Promtail põe os
//     logs desse projeto.
//
// Ninguém é Admin numa organização de projeto (no máximo Editor): um Admin
// pode editar as fontes de dados e tirar o filtro.
package grafanasync

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"launcher/internal/store"
)

const (
	// noAccessOrg é a organização 1, onde o Grafana põe toda a conta nova.
	// Fica vazia: sem fontes de dados nem dashboards.
	noAccessOrg   = int64(1)
	noAccessName  = "Sem acesso"
	platformOrg   = "Plataforma"
	projectPrefix = "Projeto: "
	// projectsDashboard é o único dashboard que vai para as organizações de
	// projeto; a Plataforma recebe todos os da pasta.
	projectsDashboard = "projects.json"

	syncEvery = time.Minute
	// userSyncTTL: uma conta que entra várias vezes num minuto não volta a
	// ser sincronizada a cada pedido.
	userSyncTTL = time.Minute
)

type Config struct {
	// URL do Grafana como o launcher o alcança (ex:
	// http://monitoring-grafana-1:3000/grafana -- com o sub-path).
	URL           string
	AdminUser     string
	AdminPassword string
	// DashboardsDir: os JSON de services/monitoring/grafana/dashboards.
	DashboardsDir string
	// URLs das fontes de dados, como o Grafana as alcança.
	PrometheusURL string
	PromProxyURL  string
	LokiURL       string
}

type Syncer struct {
	cfg    Config
	db     *store.DB
	http   *http.Client
	logger *slog.Logger

	mu        sync.Mutex // uma sincronização de cada vez
	kick      chan struct{}
	dashHash  map[string]string    // org/dashboard -> hash do último envio
	userSeen  map[string]time.Time // conta -> última sincronização dela
	userSeenM sync.Mutex
}

func New(cfg Config, db *store.DB, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		cfg: cfg, db: db, logger: logger,
		http:     &http.Client{Timeout: 10 * time.Second},
		kick:     make(chan struct{}, 1),
		dashHash: map[string]string{},
		userSeen: map[string]time.Time{},
	}
}

// Kick pede uma sincronização já (ex: depois de mudar membros).
func (s *Syncer) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run sincroniza periodicamente até ctx terminar.
func (s *Syncer) Run(ctx context.Context) {
	tick := time.NewTicker(syncEvery)
	defer tick.Stop()
	for {
		if err := s.SyncAll(ctx); err != nil {
			s.logger.Warn("grafana: sincronização falhou", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.kick:
		}
	}
}

// --- API do Grafana ---

type apiError struct {
	status int
	body   string
}

func (e apiError) Error() string { return fmt.Sprintf("grafana respondeu %d: %s", e.status, e.body) }

func isStatus(err error, status int) bool {
	var ae apiError
	return errors.As(err, &ae) && ae.status == status
}

// call faz um pedido à API do Grafana como admin, opcionalmente no
// contexto de uma organização.
func (s *Syncer) call(ctx context.Context, method, path string, org int64, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.cfg.URL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.cfg.AdminUser, s.cfg.AdminPassword)
	req.Header.Set("Content-Type", "application/json")
	if org > 0 {
		req.Header.Set("X-Grafana-Org-Id", fmt.Sprint(org))
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("grafana: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return apiError{resp.StatusCode, strings.TrimSpace(string(raw))}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type org struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (s *Syncer) orgs(ctx context.Context) (map[string]int64, error) {
	var list []org
	if err := s.call(ctx, "GET", "/api/orgs?perpage=1000", 0, nil, &list); err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, o := range list {
		out[o.Name] = o.ID
	}
	return out, nil
}

// ensureOrg devolve o id da organização, criando-a se faltar. Quem a cria
// (o admin) fica Admin dela -- é o que permite à sincronização mexer lá.
func (s *Syncer) ensureOrg(ctx context.Context, existing map[string]int64, name string) (int64, error) {
	if id, ok := existing[name]; ok {
		return id, nil
	}
	var resp struct {
		OrgID int64 `json:"orgId"`
	}
	if err := s.call(ctx, "POST", "/api/orgs", 0, map[string]string{"name": name}, &resp); err != nil {
		return 0, fmt.Errorf("criando organização %q: %w", name, err)
	}
	existing[name] = resp.OrgID
	s.logger.Info("grafana: organização criada", "org", name, "id", resp.OrgID)
	return resp.OrgID, nil
}

type datasource struct {
	UID            string            `json:"uid"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	URL            string            `json:"url"`
	Access         string            `json:"access"`
	IsDefault      bool              `json:"isDefault"`
	JSONData       map[string]any    `json:"jsonData,omitempty"`
	SecureJSONData map[string]string `json:"secureJsonData,omitempty"`
}

// ensureDatasource cria ou repõe a fonte de dados (sempre por inteiro --
// se alguém a tiver mudado à mão, volta ao que deve ser).
func (s *Syncer) ensureDatasource(ctx context.Context, orgID int64, ds datasource) error {
	err := s.call(ctx, "PUT", "/api/datasources/uid/"+ds.UID, orgID, ds, nil)
	if isStatus(err, http.StatusNotFound) {
		err = s.call(ctx, "POST", "/api/datasources", orgID, ds, nil)
	}
	if err != nil {
		return fmt.Errorf("fonte de dados %s na org %d: %w", ds.UID, orgID, err)
	}
	return nil
}

// withHeader devolve jsonData/secureJsonData que fixam um cabeçalho em
// todos os pedidos da fonte de dados.
func withHeader(name, value string) (map[string]any, map[string]string) {
	return map[string]any{"httpHeaderName1": name}, map[string]string{"httpHeaderValue1": value}
}

// ensureDashboards envia os dashboards (com o mesmo uid em todas as
// organizações) -- só quando mudaram desde o último envio, para não
// encher o histórico de versões a cada minuto. "Já enviado" confirma-se
// no Grafana: um Grafana recriado volta vazio, com as organizações
// recriadas com os mesmos ids, e a memória deste processo não sabe disso.
//
// constants troca variáveis do dashboard por constantes escondidas -- numa
// organização de projeto, "project" é sempre esse projeto, e não pode
// depender de haver logs no intervalo escolhido (sem logs a lista vinha
// vazia e as consultas ao Loki falhavam).
func (s *Syncer) ensureDashboards(ctx context.Context, orgID int64, files []string, constants map[string]string) error {
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(s.cfg.DashboardsDir, f))
		if err != nil {
			return fmt.Errorf("lendo dashboard %s: %w", f, err)
		}
		var dash map[string]any
		if err := json.Unmarshal(raw, &dash); err != nil {
			return fmt.Errorf("dashboard %s inválido: %w", f, err)
		}
		pinVariables(dash, constants)
		final, _ := json.Marshal(dash)
		sum := sha256.Sum256(final)
		key := fmt.Sprintf("%d/%s", orgID, f)
		if s.dashHash[key] == hex.EncodeToString(sum[:]) {
			uid, _ := dash["uid"].(string)
			err := s.call(ctx, "GET", "/api/dashboards/uid/"+url.PathEscape(uid), orgID, nil, nil)
			if err == nil {
				continue
			}
			if !isStatus(err, http.StatusNotFound) {
				return fmt.Errorf("confirmando dashboard %s na org %d: %w", f, orgID, err)
			}
		}
		dash["id"] = nil
		if err := s.call(ctx, "POST", "/api/dashboards/db", orgID, map[string]any{"dashboard": dash, "overwrite": true}, nil); err != nil {
			return fmt.Errorf("dashboard %s na org %d: %w", f, orgID, err)
		}
		s.dashHash[key] = hex.EncodeToString(sum[:])
	}
	return nil
}

// pinVariables substitui as variáveis indicadas por constantes escondidas.
func pinVariables(dash map[string]any, constants map[string]string) {
	templating, _ := dash["templating"].(map[string]any)
	list, _ := templating["list"].([]any)
	for i, item := range list {
		v, _ := item.(map[string]any)
		name, _ := v["name"].(string)
		value, ok := constants[name]
		if !ok {
			continue
		}
		list[i] = map[string]any{
			"name": name, "label": v["label"], "type": "constant", "query": value, "hide": 2,
			"current": map[string]any{"text": value, "value": value},
		}
	}
}

func (s *Syncer) dashboardFiles() ([]string, error) {
	entries, err := os.ReadDir(s.cfg.DashboardsDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

type orgUser struct {
	UserID int64  `json:"userId"`
	Login  string `json:"login"`
	Role   string `json:"role"`
}

// ensureUser garante a conta no Grafana (login = id da conta na
// plataforma) e devolve o id dela no Grafana. Criada com uma password
// aleatória que ninguém conhece: só se entra pelo portal.
func (s *Syncer) ensureUser(ctx context.Context, login, email string) (int64, error) {
	var found struct {
		ID int64 `json:"id"`
	}
	err := s.call(ctx, "GET", "/api/users/lookup?loginOrEmail="+url.QueryEscape(login), 0, nil, &found)
	if err == nil {
		return found.ID, nil
	}
	if !isStatus(err, http.StatusNotFound) {
		return 0, err
	}
	pw := make([]byte, 24)
	_, _ = rand.Read(pw)
	var created struct {
		ID int64 `json:"id"`
	}
	err = s.call(ctx, "POST", "/api/admin/users", 0, map[string]string{
		"login": login, "email": email, "name": email, "password": hex.EncodeToString(pw),
	}, &created)
	if err != nil {
		return 0, fmt.Errorf("criando conta %s no Grafana: %w", email, err)
	}
	return created.ID, nil
}

// --- o que deve existir ---

// desired é, por organização, que conta (login) tem que papel.
type desired map[int64]map[string]string

type person struct{ login, email string }

func memberGrafanaRole(role string) string {
	if role == store.MemberViewer {
		return "Viewer"
	}
	// developer e admin podem editar dashboards; Admin da organização
	// nunca, porque podia mexer nas fontes de dados.
	return "Editor"
}

// SyncAll converge organizações, fontes de dados, dashboards e membros.
func (s *Syncer) SyncAll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	orgs, err := s.orgs(ctx)
	if err != nil {
		return err
	}
	// A organização 1 recebe toda a conta nova: tem de estar vazia e dizer
	// porquê.
	_ = s.call(ctx, "PUT", fmt.Sprintf("/api/orgs/%d", noAccessOrg), 0, map[string]string{"name": noAccessName}, nil)

	projects, err := s.db.ListProjects(ctx, store.DefaultAccountID)
	if err != nil {
		return err
	}
	files, err := s.dashboardFiles()
	if err != nil {
		return fmt.Errorf("lendo dashboards: %w", err)
	}

	platformID, err := s.ensureOrg(ctx, orgs, platformOrg)
	if err != nil {
		return err
	}
	tenants := []string{"platform"}
	for _, p := range projects {
		tenants = append(tenants, p.Slug)
	}
	lokiJSON, lokiSecure := withHeader("X-Scope-OrgID", strings.Join(tenants, "|"))
	if err := s.ensureDatasource(ctx, platformID, datasource{UID: "prometheus", Name: "Prometheus", Type: "prometheus", URL: s.cfg.PrometheusURL, Access: "proxy", IsDefault: true}); err != nil {
		return err
	}
	if err := s.ensureDatasource(ctx, platformID, datasource{UID: "loki", Name: "Loki", Type: "loki", URL: s.cfg.LokiURL, Access: "proxy", JSONData: lokiJSON, SecureJSONData: lokiSecure}); err != nil {
		return err
	}
	if err := s.ensureDashboards(ctx, platformID, files, nil); err != nil {
		return err
	}

	projectOrg := map[string]int64{} // project id -> org
	for _, p := range projects {
		id, err := s.ensureOrg(ctx, orgs, projectPrefix+p.Slug)
		if err != nil {
			return err
		}
		projectOrg[p.ID] = id
		promJSON, promSecure := withHeader("X-Project", p.Slug)
		promJSON["httpMethod"] = "POST"
		lokiJSON, lokiSecure := withHeader("X-Scope-OrgID", p.Slug)
		if err := s.ensureDatasource(ctx, id, datasource{UID: "prometheus", Name: "Prometheus", Type: "prometheus", URL: s.cfg.PromProxyURL, Access: "proxy", IsDefault: true, JSONData: promJSON, SecureJSONData: promSecure}); err != nil {
			return err
		}
		if err := s.ensureDatasource(ctx, id, datasource{UID: "loki", Name: "Loki", Type: "loki", URL: s.cfg.LokiURL, Access: "proxy", JSONData: lokiJSON, SecureJSONData: lokiSecure}); err != nil {
			return err
		}
		if err := s.ensureDashboards(ctx, id, []string{projectsDashboard}, map[string]string{"project": p.Slug}); err != nil {
			return err
		}
	}

	// Organizações de projetos que já não existem.
	alive := map[int64]bool{}
	for _, id := range projectOrg {
		alive[id] = true
	}
	for name, id := range orgs {
		if strings.HasPrefix(name, projectPrefix) && !alive[id] {
			if err := s.call(ctx, "DELETE", fmt.Sprintf("/api/orgs/%d", id), 0, nil, nil); err != nil {
				s.logger.Warn("grafana: apagando organização de projeto apagado", "org", name, "err", err)
			} else {
				s.logger.Info("grafana: organização apagada", "org", name)
			}
		}
	}

	want, people, err := s.desiredMemberships(ctx, platformID, projectOrg)
	if err != nil {
		return err
	}
	managed := append([]int64{platformID}, values(projectOrg)...)
	return s.applyMemberships(ctx, managed, want, people)
}

func values(m map[string]int64) []int64 {
	out := make([]int64, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// desiredMemberships: admins da plataforma são Admin na Plataforma e em
// todos os projetos (o admin também gere dashboards e vê as fontes de
// dados -- é de confiança); membros têm Viewer/Editor nos seus projetos.
func (s *Syncer) desiredMemberships(ctx context.Context, platformID int64, projectOrg map[string]int64) (desired, map[string]person, error) {
	want := desired{platformID: {}}
	for _, id := range projectOrg {
		want[id] = map[string]string{}
	}
	people := map[string]person{}

	users, err := s.db.ListGrafanaUsers(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, u := range users {
		people[u.UserID] = person{u.UserID, u.Email}
		if u.PlatformAdmin {
			for org := range want {
				want[org][u.UserID] = "Admin"
			}
		}
	}
	members, err := s.db.ListAllMembers(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range members {
		org, ok := projectOrg[m.ProjectID]
		if !ok {
			continue
		}
		if _, ok := people[m.UserID]; !ok {
			people[m.UserID] = person{m.UserID, m.Email}
		}
		if want[org][m.UserID] == "Admin" {
			continue // admin da plataforma que também é membro
		}
		want[org][m.UserID] = memberGrafanaRole(m.Role)
	}
	return want, people, nil
}

// applyMemberships põe cada organização gerida com exatamente as contas e
// papéis desejados. O admin do Grafana (acesso de emergência) nunca é
// tirado.
func (s *Syncer) applyMemberships(ctx context.Context, orgIDs []int64, want desired, people map[string]person) error {
	for _, orgID := range orgIDs {
		var current []orgUser
		if err := s.call(ctx, "GET", fmt.Sprintf("/api/orgs/%d/users", orgID), 0, nil, &current); err != nil {
			return err
		}
		have := map[string]orgUser{}
		for _, u := range current {
			have[u.Login] = u
		}
		for login, role := range want[orgID] {
			u, ok := have[login]
			switch {
			case !ok:
				p := people[login]
				if _, err := s.ensureUser(ctx, p.login, p.email); err != nil {
					return err
				}
				if err := s.call(ctx, "POST", fmt.Sprintf("/api/orgs/%d/users", orgID), 0, map[string]string{"loginOrEmail": login, "role": role}, nil); err != nil {
					return fmt.Errorf("juntando %s à org %d: %w", p.email, orgID, err)
				}
			case u.Role != role:
				if err := s.call(ctx, "PATCH", fmt.Sprintf("/api/orgs/%d/users/%d", orgID, u.UserID), 0, map[string]string{"role": role}, nil); err != nil {
					return err
				}
			}
		}
		for login, u := range have {
			if _, ok := want[orgID][login]; ok || login == s.cfg.AdminUser {
				continue
			}
			if err := s.call(ctx, "DELETE", fmt.Sprintf("/api/orgs/%d/users/%d", orgID, u.UserID), 0, nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// Session regista quem entra no Grafana pelo portal e sincroniza logo as
// organizações dessa conta (no máximo uma vez por minuto por conta), para
// a primeira página já mostrar os projetos certos.
func (s *Syncer) Session(ctx context.Context, userID, email string, platformAdmin bool) error {
	if err := s.db.PutGrafanaUser(ctx, store.GrafanaUser{UserID: userID, Email: email, PlatformAdmin: platformAdmin}); err != nil {
		return err
	}
	s.userSeenM.Lock()
	recent := time.Since(s.userSeen[userID]) < userSyncTTL
	if !recent {
		s.userSeen[userID] = time.Now()
	}
	s.userSeenM.Unlock()
	if recent {
		return nil
	}
	grafanaID, err := s.ensureUser(ctx, userID, email)
	if err != nil {
		return err
	}
	if err := s.SyncAll(ctx); err != nil {
		return err
	}
	return s.setHomeOrg(ctx, grafanaID, userID, platformAdmin)
}

// setHomeOrg põe a conta a abrir numa organização útil: a Plataforma para
// admins, o primeiro projeto para os outros -- em vez da "Sem acesso", onde
// toda a conta nova cai.
func (s *Syncer) setHomeOrg(ctx context.Context, grafanaID int64, userID string, platformAdmin bool) error {
	orgs, err := s.orgs(ctx)
	if err != nil {
		return err
	}
	home := orgs[platformOrg]
	if !platformAdmin {
		home = 0
		projects, err := s.db.ListProjectsForUser(ctx, store.DefaultAccountID, userID)
		if err != nil {
			return err
		}
		if len(projects) > 0 {
			home = orgs[projectPrefix+projects[0].Slug]
		}
	}
	if home == 0 {
		return nil
	}
	return s.call(ctx, "POST", fmt.Sprintf("/api/users/%d/using/%d", grafanaID, home), 0, nil, nil)
}

// ProjectOrg devolve o id da organização de um projeto no Grafana.
func (s *Syncer) ProjectOrg(ctx context.Context, projectSlug string) (int64, error) {
	orgs, err := s.orgs(ctx)
	if err != nil {
		return 0, err
	}
	id, ok := orgs[projectPrefix+projectSlug]
	if !ok {
		return 0, errors.New("a organização deste projeto ainda não existe no Grafana")
	}
	return id, nil
}
