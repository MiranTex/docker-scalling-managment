package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestParseTarget(t *testing.T) {
	got, err := parseTarget("gymapp/dev/web")
	if err != nil || got != (target{"gymapp", "dev", "web"}) {
		t.Errorf("parseTarget = %+v, %v", got, err)
	}
	for _, bad := range []string{"gymapp/dev", "gymapp//web", "a/b/c/d", ""} {
		if _, err := parseTarget(bad); err == nil {
			t.Errorf("parseTarget(%q) aceitou", bad)
		}
	}
	if p := got.envPath("addons", "db", "tunnel"); p != "/v1/projects/gymapp/environments/dev/addons/db/tunnel" {
		t.Errorf("envPath = %s", p)
	}
}

func TestReorderPutsFlagsFirst(t *testing.T) {
	got := reorder([]string{"gymapp/dev/web", "--replica", "web-1", "--debug"})
	want := []string{"--replica", "web-1", "--debug", "gymapp/dev/web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reorder = %v, quero %v", got, want)
	}
}

func TestConnectionURL(t *testing.T) {
	pg := credentials{Kind: "postgres", User: "app", Password: "a/b+c", Database: "app", Port: 5432}
	if got := pg.connectionURL(15432); got != "postgres://app:a%2Fb+c@127.0.0.1:15432/app?sslmode=disable" {
		t.Errorf("postgres = %s", got)
	}
	redis := credentials{Kind: "redis", User: "default", Password: "pw", Database: "0", Port: 6379}
	if got := redis.connectionURL(6379); got != "redis://default:pw@127.0.0.1:6379/0" {
		t.Errorf("redis = %s", got)
	}
}

// fakeLauncher dá bilhetes de túnel e faz eco no WebSocket.
func fakeLauncher(t *testing.T, tickets *atomic.Int64) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/projects/loja/environments/prod/addons/db/tunnel", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ak_teste" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		tickets.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "/v1/projects/loja/tunnel/ws?ticket=x", "container": "bs-loja-prod-db"})
	})
	mux.HandleFunc("GET /v1/projects/loja/tunnel/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		nc := websocket.NetConn(r.Context(), conn, websocket.MessageBinary)
		_, _ = io.Copy(nc, nc)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestForwarderOneTunnelPerConnection(t *testing.T) {
	var tickets atomic.Int64
	srv := fakeLauncher(t, &tickets)
	c := newClient(config{APIURL: srv.URL, Token: "ak_teste"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &forwarder{client: c, target: target{"loja", "prod", "db"}, logf: func(string, ...any) {}}
	go f.serve(ctx, ln)
	defer ln.Close()

	for i, msg := range []string{"SELECT 1;", "SELECT 2;"} {
		local, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_ = local.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := local.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(msg))
		if _, err := io.ReadFull(local, buf); err != nil || string(buf) != msg {
			t.Fatalf("ligação %d: leu %q, err = %v", i+1, buf, err)
		}
		local.Close()
	}
	if n := tickets.Load(); n != 2 {
		t.Errorf("bilhetes pedidos = %d, quero um por ligação (2)", n)
	}
}

func TestAPIErrorsCarryTheMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"no_shell","message":"esta imagem não tem shell"}`)
	}))
	defer srv.Close()
	err := newClient(config{APIURL: srv.URL, Token: "x"}).call("POST", "/v1/x", nil, nil)
	apiErr, ok := err.(*apiError)
	if !ok || apiErr.Code != "no_shell" || apiErr.Status != 409 || !strings.Contains(err.Error(), "shell") {
		t.Errorf("err = %#v", err)
	}
}
