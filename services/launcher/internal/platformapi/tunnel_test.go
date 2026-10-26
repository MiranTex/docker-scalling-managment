package platformapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"launcher/internal/dockerclient"
)

// addonDocker deixa criar um addon e responde ao inspect dele.
type addonDocker struct {
	*termDocker
	labels map[string]map[string]string // nome -> labels
}

func (d *addonDocker) CreateVolume(context.Context, string, map[string]string) error { return nil }
func (d *addonDocker) InspectImage(context.Context, string) (*dockerclient.ImageInspect, error) {
	return &dockerclient.ImageInspect{}, nil
}
func (d *addonDocker) CreateContainer(_ context.Context, name string, req dockerclient.CreateContainerRequest) (string, error) {
	d.labels[name] = req.Labels
	return "cid-" + name, nil
}
func (d *addonDocker) StartContainer(context.Context, string) error        { return nil }
func (d *addonDocker) RemoveContainer(context.Context, string, bool) error { return nil }
func (d *addonDocker) InspectContainer(_ context.Context, name string) (*dockerclient.ContainerInspect, error) {
	var info dockerclient.ContainerInspect
	info.ID = "cid-" + name
	info.Config.Labels = d.labels[name]
	info.State.Running = d.labels[name] != nil
	return &info, nil
}

// echoServer aceita ligações TCP e devolve o que recebe.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return ln.Addr().String()
}

func TestAddonTunnelAndCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	docker := &addonDocker{termDocker: &termDocker{}, labels: map[string]map[string]string{}}
	api, srv := setupAPIWith(t, docker, docker.termDocker)
	mux := srv.Config.Handler.(*http.ServeMux)
	if rec := call(t, mux, "POST", "/v1/projects/loja/environments/prod/addons", "t-platform", map[string]string{"slug": "db", "kind": "postgres"}); rec.Code != 201 {
		t.Fatalf("criar addon: %d %s", rec.Code, rec.Body)
	}

	// Credenciais: só developers (e acima), e com a password do DATABASE_URL.
	if rec := call(t, mux, "GET", "/v1/projects/loja/environments/prod/addons/db/credentials", "t-viewer", nil); rec.Code != http.StatusForbidden {
		t.Errorf("viewer leu as credenciais: %d", rec.Code)
	}
	rec := call(t, mux, "GET", "/v1/projects/loja/environments/prod/addons/db/credentials", "t-dev", nil)
	var creds map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &creds)
	if rec.Code != 200 || creds["user"] != "app" || creds["database"] != "app" || creds["port"] != float64(5432) || creds["password"] == "" {
		t.Fatalf("credenciais: %d %s", rec.Code, rec.Body)
	}

	// Túnel: o launcher liga ao container do addon na porta do Postgres.
	echo := echoServer(t)
	var dialed string
	api.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return (&net.Dialer{}).DialContext(ctx, network, echo)
	}
	if rec := call(t, mux, "POST", "/v1/projects/loja/environments/prod/addons/db/tunnel", "t-viewer", nil); rec.Code != http.StatusForbidden {
		t.Errorf("viewer abriu túnel: %d", rec.Code)
	}
	rec = call(t, mux, "POST", "/v1/projects/loja/environments/prod/addons/db/tunnel", "t-dev", nil)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 201 {
		t.Fatalf("túnel: %d %s", rec.Code, rec.Body)
	}
	path := out["url"].(string)

	// Um bilhete de túnel não abre um terminal.
	if _, resp, err := dialTerminal(ctx, srv, strings.Replace(path, "/tunnel/", "/terminal/", 1), ""); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bilhete de túnel no terminal: resp = %v, err = %v", resp, err)
	}
	rec = call(t, mux, "POST", "/v1/projects/loja/environments/prod/addons/db/tunnel", "t-dev", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	conn, _, err := dialTerminal(ctx, srv, out["url"].(string), "")
	if err != nil {
		t.Fatal(err)
	}
	nc := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	defer nc.Close()
	if _, err := nc.Write([]byte("SELECT 1;")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 9)
	if _, err := io.ReadFull(nc, buf); err != nil || string(buf) != "SELECT 1;" {
		t.Fatalf("eco pelo túnel: %q, err = %v", buf, err)
	}
	if dialed != "bs-loja-prod-db:5432" {
		t.Errorf("o launcher ligou a %q", dialed)
	}
}
