package platformapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// fakeAgent faz o papel do agente: liga-se pelo WebSocket e, do outro lado
// do túnel, serve um Docker falso com a máquina de um servidor Intel.
func fakeAgent(t *testing.T, srv *httptest.Server, nodeToken string) (stop func(), status int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/agent/connect", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + nodeToken}, "X-Agent-Version": {"0.1.0"}},
	})
	if err != nil {
		cancel()
		if resp != nil {
			return func() {}, resp.StatusCode
		}
		t.Fatal(err)
	}
	conn.SetReadLimit(16 << 20)
	session, err := yamux.Server(websocket.NetConn(ctx, conn, websocket.MessageBinary), nil)
	if err != nil {
		t.Fatal(err)
	}
	docker := http.NewServeMux()
	docker.HandleFunc("GET /info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Name":"aplusserver","NCPU":4,"MemTotal":17179869184,"Architecture":"x86_64","OSType":"linux","ServerVersion":"29.2.1"}`))
	})
	docker.HandleFunc("GET /containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	go func() { _ = http.Serve(session, docker) }()
	return func() { _ = session.Close(); cancel() }, http.StatusSwitchingProtocols
}

func TestNodeJoinsThroughTheAgentTunnel(t *testing.T) {
	api, srv := setupAPIWith(t, &termDocker{}, &termDocker{})
	_, certFile, _ := registryKeys(t)
	if err := api.WithAgents(AgentConfig{PublicURL: "https://api.example.test", CACertFile: certFile, RegistryHost: "registry.example.test", AgentImage: "registry.example.test/basestack/agent:dev"}); err != nil {
		t.Fatal(err)
	}
	post := func(token, path string, body any) (int, map[string]any) {
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(body)
		req, _ := http.NewRequest("POST", srv.URL+path, &buf)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		raw, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	// Só um admin da plataforma cria tokens de entrada; o comando já vem
	// com o URL e a impressão digital da CA.
	if code, _ := post("t-dev", "/v1/nodes/join-tokens", nil); code != 403 {
		t.Errorf("developer criou token de entrada: %d", code)
	}
	code, created := post("t-platform", "/v1/nodes/join-tokens", nil)
	if code != 201 {
		t.Fatalf("token de entrada: %d %v", code, created)
	}
	join := created["token"].(string)
	if cmd := created["command"].(string); !strings.Contains(cmd, "basestack node join https://api.example.test --token bsj_") || !strings.Contains(cmd, "--ca sha256:") {
		t.Errorf("comando = %q", cmd)
	}

	// Entrar: o token serve uma vez; o nome "local" é da plataforma.
	if code, _ := post("", "/v1/agent/join", map[string]string{"token": join, "name": "local"}); code != 400 {
		t.Errorf("nó chamado local: %d", code)
	}
	code, joined := post("", "/v1/agent/join", map[string]string{"token": join, "name": "aplusserver"})
	if code != 201 || !strings.HasPrefix(joined["nodeToken"].(string), "bsn_") || joined["agentImage"] != "registry.example.test/basestack/agent:dev" {
		t.Fatalf("join: %d %v", code, joined)
	}
	if code, _ := post("", "/v1/agent/join", map[string]string{"token": join, "name": "outro"}); code != 401 {
		t.Errorf("token reutilizado: %d", code)
	}
	nodeToken := joined["nodeToken"].(string)
	if _, status := fakeAgent(t, srv, "bsn_errado"); status != http.StatusUnauthorized {
		t.Errorf("agente com credencial errada: %d", status)
	}

	// Ligado: o nó aparece online, com o que a máquina dele reporta pelo
	// túnel.
	stop, _ := fakeAgent(t, srv, nodeToken)
	defer stop()
	var node map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		node = findNode(t, srv, "aplusserver")
		if node != nil && node["online"] == true && node["arch"] == "amd64" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if node == nil || node["online"] != true || node["arch"] != "amd64" || node["cpus"] != float64(4) ||
		node["memoryMb"] != float64(16384) || node["kind"] != "agent" || node["agentVersion"] != "0.1.0" || node["dockerVersion"] != "29.2.1" {
		t.Fatalf("nó ligado = %v", node)
	}

	// Remover: o nó sai da lista e a credencial deixa de servir.
	req, _ := http.NewRequest("DELETE", srv.URL+"/v1/nodes/aplusserver", nil)
	req.Header.Set("Authorization", "Bearer t-platform")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 204 {
		t.Fatalf("remover: %v %v", resp, err)
	}
	if findNode(t, srv, "aplusserver") != nil {
		t.Error("o nó continua na lista")
	}
	if _, status := fakeAgent(t, srv, nodeToken); status != http.StatusUnauthorized {
		t.Errorf("credencial de um nó removido: %d", status)
	}
}

func findNode(t *testing.T, srv *httptest.Server, name string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer t-platform")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var nodes []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&nodes)
	for _, n := range nodes {
		if n["name"] == name {
			return n
		}
	}
	return nil
}
