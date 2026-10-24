package grafanasync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"launcher/internal/store"
	"launcher/internal/store/storetest"
)

// fakeGrafana guarda em memória o que a sincronização cria, com as rotas
// da API do Grafana que ela usa.
type fakeGrafana struct {
	mu          sync.Mutex
	orgs        map[int64]string
	nextOrg     int64
	users       map[string]int64 // login -> id
	nextUser    int64
	members     map[int64]map[string]string // org -> login -> role
	datasources map[int64]map[string]datasource
	dashboards  map[int64]int             // org -> envios
	dashUIDs    map[int64]map[string]bool // org -> uids existentes
	dashBodies  map[int64]map[string]any  // org -> último dashboard enviado
}

func newFakeGrafana() *fakeGrafana {
	return &fakeGrafana{
		orgs: map[int64]string{1: "Main Org."}, nextOrg: 2,
		users: map[string]int64{"admin": 1}, nextUser: 2,
		members:     map[int64]map[string]string{1: {"admin": "Admin"}},
		datasources: map[int64]map[string]datasource{},
		dashboards:  map[int64]int{},
		dashUIDs:    map[int64]map[string]bool{},
	}
}

func (f *fakeGrafana) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, p, ok := r.BasicAuth(); !ok || u != "admin" || p != "pw" {
		http.Error(w, "unauthorized", 401)
		return
	}
	orgHdr, _ := strconv.ParseInt(r.Header.Get("X-Grafana-Org-Id"), 10, 64)
	path := strings.TrimPrefix(r.URL.Path, "/grafana")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	switch {
	case r.Method == "GET" && path == "/api/orgs":
		var out []org
		for id, n := range f.orgs {
			out = append(out, org{id, n})
		}
		reply(out)
	case r.Method == "POST" && path == "/api/orgs":
		id := f.nextOrg
		f.nextOrg++
		f.orgs[id] = body["name"].(string)
		f.members[id] = map[string]string{"admin": "Admin"} // quem cria fica Admin
		reply(map[string]int64{"orgId": id})
	case len(parts) == 3 && parts[1] == "orgs" && r.Method == "PUT":
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		f.orgs[id] = body["name"].(string)
	case len(parts) == 3 && parts[1] == "orgs" && r.Method == "DELETE":
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		delete(f.orgs, id)
		delete(f.members, id)
	case strings.HasPrefix(path, "/api/datasources"):
		if f.members[orgHdr]["admin"] == "" {
			http.Error(w, "not a member", 403)
			return
		}
		raw, _ := json.Marshal(body)
		var ds datasource
		_ = json.Unmarshal(raw, &ds)
		if f.datasources[orgHdr] == nil {
			f.datasources[orgHdr] = map[string]datasource{}
		}
		if r.Method == "PUT" {
			if _, ok := f.datasources[orgHdr][ds.UID]; !ok {
				http.Error(w, "not found", 404)
				return
			}
		}
		f.datasources[orgHdr][ds.UID] = ds
	case path == "/api/dashboards/db":
		f.dashboards[orgHdr]++
		if f.dashUIDs[orgHdr] == nil {
			f.dashUIDs[orgHdr] = map[string]bool{}
		}
		if d, ok := body["dashboard"].(map[string]any); ok {
			f.dashUIDs[orgHdr][d["uid"].(string)] = true
			if f.dashBodies == nil {
				f.dashBodies = map[int64]map[string]any{}
			}
			f.dashBodies[orgHdr] = d
		}
	case strings.HasPrefix(path, "/api/dashboards/uid/"):
		if !f.dashUIDs[orgHdr][strings.TrimPrefix(path, "/api/dashboards/uid/")] {
			http.Error(w, "not found", 404)
		}
	case path == "/api/users/lookup":
		if id, ok := f.users[r.URL.Query().Get("loginOrEmail")]; ok {
			reply(map[string]int64{"id": id})
			return
		}
		http.Error(w, "not found", 404)
	case len(parts) == 5 && parts[1] == "users" && parts[3] == "using":
		// mudar a organização de entrada de uma conta: nada a guardar aqui.
	case path == "/api/admin/users":
		// Como o Grafana: só estes campos, todos texto.
		for k, v := range body {
			if _, isText := v.(string); !isText || !map[string]bool{"login": true, "email": true, "name": true, "password": true}[k] {
				http.Error(w, `{"message":"bad request data"}`, 400)
				return
			}
		}
		id := f.nextUser
		f.nextUser++
		f.users[body["login"].(string)] = id
		reply(map[string]int64{"id": id})
	case len(parts) == 4 && parts[1] == "orgs" && parts[3] == "users":
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		if r.Method == "GET" {
			var out []orgUser
			for login, role := range f.members[id] {
				out = append(out, orgUser{UserID: f.users[login], Login: login, Role: role})
			}
			reply(out)
			return
		}
		f.members[id][body["loginOrEmail"].(string)] = body["role"].(string)
	case len(parts) == 5 && parts[1] == "orgs" && parts[3] == "users":
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		uid, _ := strconv.ParseInt(parts[4], 10, 64)
		for login, u := range f.users {
			if u != uid {
				continue
			}
			if r.Method == "DELETE" {
				delete(f.members[id], login)
			} else {
				f.members[id][login] = body["role"].(string)
			}
		}
	default:
		http.Error(w, fmt.Sprintf("rota não simulada: %s %s", r.Method, path), 500)
	}
}

func (f *fakeGrafana) orgID(name string) int64 {
	for id, n := range f.orgs {
		if n == name {
			return id
		}
	}
	return 0
}

func TestSyncOrgsDatasourcesAndMembers(t *testing.T) {
	ctx := context.Background()
	db := storetest.New(t)
	grafana := newFakeGrafana()
	srv := httptest.NewServer(grafana)
	defer srv.Close()
	dir := t.TempDir()
	for _, f := range []string{"projects.json", "traefik.json"} {
		_ = os.WriteFile(filepath.Join(dir, f), []byte(`{"uid":"`+f+`","title":"x"}`), 0o644)
	}
	s := New(Config{URL: srv.URL + "/grafana", AdminUser: "admin", AdminPassword: "pw", DashboardsDir: dir,
		PrometheusURL: "http://prometheus:9090", PromProxyURL: "http://prom-label-proxy:8080", LokiURL: "http://loki:3100"}, db, nil)

	loja := store.Project{ID: "p-loja", AccountID: store.DefaultAccountID, Slug: "loja", Name: "Loja", CreatedBy: "t"}
	blog := store.Project{ID: "p-blog", AccountID: store.DefaultAccountID, Slug: "blog", Name: "Blog", CreatedBy: "t"}
	for _, p := range []store.Project{loja, blog} {
		if err := db.CreateProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []store.Member{
		{ProjectID: "p-loja", UserID: "u-ana", Email: "ana@x.pt", Role: "viewer", AddedBy: "t"},
		{ProjectID: "p-loja", UserID: "u-rui", Email: "rui@x.pt", Role: "developer", AddedBy: "t"},
		{ProjectID: "p-blog", UserID: "u-rui", Email: "rui@x.pt", Role: "admin", AddedBy: "t"},
	} {
		if err := db.PutMember(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	// A Joana é admin da plataforma: já entrou uma vez pelo portal.
	if err := s.Session(ctx, "u-joana", "joana@x.pt", true); err != nil {
		t.Fatal(err)
	}

	if grafana.orgs[1] != "Sem acesso" {
		t.Errorf("org 1 = %q", grafana.orgs[1])
	}
	platform, lojaOrg, blogOrg := grafana.orgID("Plataforma"), grafana.orgID("Projeto: loja"), grafana.orgID("Projeto: blog")
	if platform == 0 || lojaOrg == 0 || blogOrg == 0 {
		t.Fatalf("organizações = %v", grafana.orgs)
	}

	// Fontes de dados: o projeto só chega aos seus dados.
	prom, loki := grafana.datasources[lojaOrg]["prometheus"], grafana.datasources[lojaOrg]["loki"]
	if prom.URL != "http://prom-label-proxy:8080" || prom.SecureJSONData["httpHeaderValue1"] != "loja" || prom.JSONData["httpHeaderName1"] != "X-Project" {
		t.Errorf("prometheus da loja = %+v", prom)
	}
	if loki.SecureJSONData["httpHeaderValue1"] != "loja" || loki.JSONData["httpHeaderName1"] != "X-Scope-OrgID" {
		t.Errorf("loki da loja = %+v", loki)
	}
	if p := grafana.datasources[platform]["prometheus"]; p.URL != "http://prometheus:9090" {
		t.Errorf("prometheus da plataforma = %+v", p)
	}
	if l := grafana.datasources[platform]["loki"]; !strings.Contains(l.SecureJSONData["httpHeaderValue1"], "platform") || !strings.Contains(l.SecureJSONData["httpHeaderValue1"], "loja") {
		t.Errorf("loki da plataforma = %+v", l)
	}
	if grafana.dashboards[platform] != 2 || grafana.dashboards[lojaOrg] != 1 {
		t.Errorf("dashboards = %v (plataforma recebe todos, projetos só o de projetos)", grafana.dashboards)
	}

	// Papéis.
	want := map[int64]map[string]string{
		platform: {"admin": "Admin", "u-joana": "Admin"},
		lojaOrg:  {"admin": "Admin", "u-joana": "Admin", "u-ana": "Viewer", "u-rui": "Editor"},
		blogOrg:  {"admin": "Admin", "u-joana": "Admin", "u-rui": "Editor"},
	}
	for org, roles := range want {
		if fmt.Sprint(grafana.members[org]) != fmt.Sprint(roles) {
			t.Errorf("membros da org %s = %v, queria %v", grafana.orgs[org], grafana.members[org], roles)
		}
	}

	// Mudanças: a Ana sai, o blog é apagado; e voltar a sincronizar não
	// reenvia dashboards que não mudaram.
	if err := db.DeleteMember(ctx, "p-loja", "u-ana"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProject(ctx, "p-blog"); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := grafana.members[lojaOrg]["u-ana"]; ok {
		t.Error("a Ana devia ter saído da organização da loja")
	}
	if grafana.orgID("Projeto: blog") != 0 {
		t.Error("a organização do blog devia ter sido apagada")
	}
	if grafana.dashboards[lojaOrg] != 1 {
		t.Errorf("dashboards reenviados sem mudança: %d", grafana.dashboards[lojaOrg])
	}
	if grafana.members[platform]["admin"] != "Admin" {
		t.Error("o admin de emergência nunca pode ser tirado")
	}
}

// Um Grafana recriado perde tudo e volta a dar os mesmos ids às
// organizações: os dashboards têm de ser reenviados, mesmo que este
// processo se lembre de já os ter enviado.
func TestDashboardsResentAfterGrafanaLosesThem(t *testing.T) {
	ctx := context.Background()
	db := storetest.New(t)
	grafana := newFakeGrafana()
	srv := httptest.NewServer(grafana)
	defer srv.Close()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "projects.json"), []byte(`{"uid":"base-stack-projects","title":"x"}`), 0o644)
	s := New(Config{URL: srv.URL + "/grafana", AdminUser: "admin", AdminPassword: "pw", DashboardsDir: dir}, db, nil)
	if err := db.CreateProject(ctx, store.Project{ID: "p1", AccountID: store.DefaultAccountID, Slug: "loja", Name: "Loja", CreatedBy: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	org := grafana.orgID("Projeto: loja")
	if !grafana.dashUIDs[org]["base-stack-projects"] {
		t.Fatal("dashboard não enviado na primeira sincronização")
	}

	// O Grafana perde tudo (recriado sem volume).
	grafana.mu.Lock()
	grafana.dashUIDs = map[int64]map[string]bool{}
	grafana.mu.Unlock()

	if err := s.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	if !grafana.dashUIDs[org]["base-stack-projects"] {
		t.Error("o dashboard devia ter sido reenviado depois de o Grafana o perder")
	}
}

// Na organização de um projeto, "project" é uma constante com o slug e
// env/service são listas com o que o projeto tem -- não listas tiradas dos
// logs, que ficam vazias se o projeto não tiver logs no intervalo.
func TestProjectVariablesPinnedFromPlatform(t *testing.T) {
	ctx := context.Background()
	db := storetest.New(t)
	grafana := newFakeGrafana()
	srv := httptest.NewServer(grafana)
	defer srv.Close()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "projects.json"), []byte(`{"uid":"base-stack-projects","templating":{"list":[
		{"name":"project","label":"Projeto","type":"query","query":"label_values(project)"},
		{"name":"env","type":"query","query":"label_values({project=~\"$project\"}, env)","allValue":".*"},
		{"name":"service","type":"query","query":"label_values({project=~\"$project\"}, service)","allValue":".*"},
		{"name":"other","type":"query","query":"x"}]}}`), 0o644)
	s := New(Config{URL: srv.URL + "/grafana", AdminUser: "admin", AdminPassword: "pw", DashboardsDir: dir}, db, nil)
	if err := db.CreateProject(ctx, store.Project{ID: "p1", AccountID: store.DefaultAccountID, Slug: "loja", Name: "Loja", CreatedBy: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProject(ctx, store.Project{ID: "p2", AccountID: store.DefaultAccountID, Slug: "blog", Name: "Blog", CreatedBy: "t"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.Environment{
		{ID: "e1", ProjectID: "p1", Slug: "prod", Network: "bs-loja-prod", CreatedBy: "t"},
		{ID: "e2", ProjectID: "p1", Slug: "dev", Network: "bs-loja-dev", CreatedBy: "t"},
		{ID: "e3", ProjectID: "p2", Slug: "stage", Network: "bs-blog-stage", CreatedBy: "t"},
	} {
		if err := db.CreateEnvironment(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, sv := range []store.Service{
		{ID: "s1", EnvironmentID: "e1", Slug: "web", Kind: "web", ScalingMode: "fixed", Replicas: 1, DesiredState: "running", CreatedBy: "t"},
		{ID: "s2", EnvironmentID: "e2", Slug: "web", Kind: "web", ScalingMode: "fixed", Replicas: 1, DesiredState: "running", CreatedBy: "t"},
		{ID: "s3", EnvironmentID: "e3", Slug: "api", Kind: "web", ScalingMode: "fixed", Replicas: 1, DesiredState: "running", CreatedBy: "t"},
	} {
		if err := db.CreateService(ctx, sv); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateAddon(ctx, store.Addon{ID: "a1", EnvironmentID: "e1", Slug: "db", Kind: "postgres", Image: "postgres:17-alpine", EnvVar: "DATABASE_URL", CreatedBy: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	vars := grafana.dashBodies[grafana.orgID("Projeto: loja")]["templating"].(map[string]any)["list"].([]any)
	project := vars[0].(map[string]any)
	if project["type"] != "constant" || project["query"] != "loja" || project["hide"] != float64(2) {
		t.Errorf("variável project na org do projeto = %v", project)
	}
	pinned := func(v any) (string, any) {
		m := v.(map[string]any)
		return fmt.Sprint(m["type"], " ", m["query"]), m["allValue"]
	}
	for i, want := range []string{"custom dev,prod", "custom db,web"} {
		if got, all := pinned(vars[i+1]); got != want || all != ".*" {
			t.Errorf("variável %d na org do projeto = %q (allValue %v), quero %q", i+1, got, all, want)
		}
	}
	if other, _ := pinned(vars[3]); other != "query x" {
		t.Errorf("as outras variáveis ficam como estão: %v", other)
	}
	platform := grafana.dashBodies[grafana.orgID("Plataforma")]["templating"].(map[string]any)["list"].([]any)
	for i, want := range []string{"query label_values(project)", "custom dev,prod,stage", "custom api,db,web"} {
		if got, _ := pinned(platform[i]); got != want {
			t.Errorf("Plataforma, variável %d = %q, quero %q", i, got, want)
		}
	}
}
