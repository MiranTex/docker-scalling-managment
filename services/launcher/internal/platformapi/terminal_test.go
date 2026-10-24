package platformapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"launcher/internal/dockerclient"
	"launcher/internal/platform"
	"launcher/internal/secretbox"
	"launcher/internal/store/storetest"
)

// termDocker tem uma réplica a correr e uma "shell" que faz eco.
type termDocker struct {
	netDocker
	mu      sync.Mutex
	replica dockerclient.Container
	noShell bool
	resized []int
}

func (d *termDocker) ListContainers(context.Context, bool, map[string][]string) ([]dockerclient.Container, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.replica.ID == "" {
		return nil, nil
	}
	return []dockerclient.Container{d.replica}, nil
}

func (d *termDocker) CreateExec(context.Context, string, dockerclient.ExecConfig) (string, error) {
	return "exec-1", nil
}
func (d *termDocker) StartExecDetached(context.Context, string) error { return nil }
func (d *termDocker) InspectExec(context.Context, string) (*dockerclient.ExecInspect, error) {
	if d.noShell {
		return &dockerclient.ExecInspect{ExitCode: 127}, nil
	}
	return &dockerclient.ExecInspect{}, nil
}
func (d *termDocker) StartExec(context.Context, string, bool) (io.ReadWriteCloser, error) {
	r, w := io.Pipe()
	return struct {
		io.Reader
		io.Writer
		io.Closer
	}{r, w, closers{r, w}}, nil
}
func (d *termDocker) ResizeExec(_ context.Context, _ string, rows, cols int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resized = []int{rows, cols}
	return nil
}

type closers []io.Closer

func (c closers) Close() error {
	for _, x := range c {
		x.Close()
	}
	return nil
}

func setupTerminalAPI(t *testing.T) (*API, *termDocker, *httptest.Server) {
	docker := &termDocker{}
	api, srv := setupAPIWith(t, docker, docker)
	return api, docker, srv
}

// setupAPIWith monta o projeto loja/prod com o serviço web (réplica web-1
// em term) sobre o Docker falso dado.
func setupAPIWith(t *testing.T, d platform.Docker, docker *termDocker) (*API, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(base64.StdEncoding.EncodeToString(key))
	svc := platform.New(storetest.New(t), d, box, platform.Config{PostgresImage: "postgres:17-alpine"}, nil)
	users := fakeUsers{"dev@x.pt": "u-dev", "viewer@x.pt": "u-viewer"}
	mux := http.NewServeMux()
	api := New(svc, accountTokens{}, nil, users, nil)
	api.Register(mux)

	for _, step := range []struct {
		path string
		body any
	}{
		{"/v1/projects", map[string]string{"slug": "loja"}},
		{"/v1/projects/loja/members", map[string]string{"email": "dev@x.pt", "role": "developer"}},
		{"/v1/projects/loja/members", map[string]string{"email": "viewer@x.pt", "role": "viewer"}},
		{"/v1/projects/loja/environments", map[string]string{"slug": "prod"}},
		{"/v1/projects/loja/environments/prod/services", map[string]any{"slug": "web", "kind": "web", "port": 80}},
	} {
		if rec := call(t, mux, "POST", step.path, "t-platform", step.body); rec.Code != 201 {
			t.Fatalf("POST %s = %d %s", step.path, rec.Code, rec.Body)
		}
	}
	p, e, _ := svc.Scope(ctx, "loja", "prod")
	view, _ := svc.GetService(ctx, "loja", "prod", "web")
	docker.replica = dockerclient.Container{ID: "cid-web-1", Names: []string{"/web-1"}, State: "running", Labels: map[string]string{
		platform.LabelService: view.ID, platform.LabelProjectID: p.ID, platform.LabelEnvironmentID: e.ID,
	}}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return api, srv
}

func askTerminal(t *testing.T, srv *httptest.Server, token string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/projects/loja/environments/prod/services/web/terminal", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func dialTerminal(ctx context.Context, srv *httptest.Server, path string, origin string) (*websocket.Conn, *http.Response, error) {
	opts := &websocket.DialOptions{}
	if origin != "" {
		opts.HTTPHeader = http.Header{"Origin": {origin}}
	}
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, opts)
}

func TestTerminalTicketAndSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, docker, srv := setupTerminalAPI(t)

	if code, _ := askTerminal(t, srv, "t-viewer", map[string]any{}); code != http.StatusForbidden {
		t.Errorf("viewer pediu terminal: %d, queria 403", code)
	}
	if code, _ := askTerminal(t, srv, "t-outsider", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("quem não é membro: %d, queria 404", code)
	}
	code, out := askTerminal(t, srv, "t-dev", map[string]any{})
	if code != http.StatusCreated || out["container"] != "web-1" {
		t.Fatalf("developer: %d %v", code, out)
	}
	path := out["url"].(string)

	conn, _, err := dialTerminal(ctx, srv, path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","rows":40,"cols":120}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo ola\n")); err != nil {
		t.Fatal(err)
	}
	_, got, err := conn.Read(ctx)
	if err != nil || string(got) != "echo ola\n" {
		t.Fatalf("leu %q, err = %v", got, err)
	}
	docker.mu.Lock()
	resized := docker.resized
	docker.mu.Unlock()
	if len(resized) != 2 || resized[0] != 40 || resized[1] != 120 {
		t.Errorf("resize = %v", resized)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	// O bilhete é de uso único.
	if _, resp, err := dialTerminal(ctx, srv, path, ""); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bilhete reutilizado: resp = %v, err = %v", resp, err)
	}

	// Sem shell, o portal fica a saber antes de abrir (e oferece debug).
	docker.noShell = true
	if code, out := askTerminal(t, srv, "t-dev", map[string]any{}); code != http.StatusConflict || out["error"] != "no_shell" {
		t.Errorf("sem shell: %d %v", code, out)
	}
}

func TestTerminalRejectsForeignOrigin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api, _, srv := setupTerminalAPI(t)
	api.WithTerminalOrigins([]string{"portal.example"})

	_, out := askTerminal(t, srv, "t-dev", map[string]any{})
	if _, resp, err := dialTerminal(ctx, srv, out["url"].(string), "http://evil.example"); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("origem estranha: resp = %v, err = %v", resp, err)
	}
	_, out = askTerminal(t, srv, "t-dev", map[string]any{})
	conn, _, err := dialTerminal(ctx, srv, out["url"].(string), "http://portal.example")
	if err != nil {
		t.Fatalf("origem do portal: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalClosesWhenIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api, _, srv := setupTerminalAPI(t)
	api.term.idle = 200 * time.Millisecond

	_, out := askTerminal(t, srv, "t-dev", map[string]any{})
	conn, _, err := dialTerminal(ctx, srv, out["url"].(string), "")
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Errorf("fechou com %v", err)
			}
			break
		}
		if !strings.Contains(string(msg), "inatividade") {
			t.Errorf("mensagem inesperada %q", msg)
		}
	}
}

func TestTerminalSessionLimit(t *testing.T) {
	term := newTerminals()
	target := platform.TerminalTarget{}
	target.Project.Slug = "loja"
	for i := 0; i < maxSessionsPerUser; i++ {
		if _, err := term.redeem(term.issue(terminalTicket{subject: "u", target: target}), "loja", false); err != nil {
			t.Fatalf("sessão %d: %v", i+1, err)
		}
	}
	if _, err := term.redeem(term.issue(terminalTicket{subject: "u", target: target}), "loja", false); err == nil {
		t.Error("passou do limite de sessões")
	}
	term.release(terminalTicket{subject: "u"})
	if _, err := term.redeem(term.issue(terminalTicket{subject: "u", target: target}), "loja", false); err != nil {
		t.Errorf("depois de fechar uma: %v", err)
	}
	// Um bilhete de outro projeto não abre neste.
	if _, err := term.redeem(term.issue(terminalTicket{subject: "v", target: target}), "outro", false); err == nil {
		t.Error("bilhete aceite noutro projeto")
	}
}
