package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"

	"github.com/coder/websocket"
)

type credentials struct {
	Kind     string `json:"kind"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	Port     int    `json:"port"`
}

// connectionURL é o que se cola no cliente (psql, DBeaver, a app local).
func (c credentials) connectionURL(port int) string {
	u := url.URL{Host: "127.0.0.1:" + strconv.Itoa(port), User: url.UserPassword(c.User, c.Password), Path: "/" + c.Database}
	if c.Kind == "redis" {
		u.Scheme = "redis"
	} else {
		u.Scheme, u.RawQuery = "postgres", "sslmode=disable"
	}
	return u.String()
}

func cmdPortForward(args []string) error {
	fs := flag.NewFlagSet("port-forward", flag.ContinueOnError)
	port := fs.Int("port", 0, "porta local (por defeito a do addon: 5432 ou 6379)")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("uso: bs port-forward <projeto>/<ambiente>/<addon> [--port N]")
	}
	t, err := parseTarget(fs.Arg(0))
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	c := newClient(cfg)

	var creds credentials
	if err := c.call("GET", t.envPath("addons", t.name, "credentials"), nil, &creds); err != nil {
		return err
	}
	if *port == 0 {
		*port = creds.Port
	}
	// Só em 127.0.0.1: o túnel tem o teu acesso, não é para a rede local.
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		return fmt.Errorf("não consegui abrir a porta %d (já em uso?) -- escolhe outra com --port: %w", *port, err)
	}
	*port = ln.Addr().(*net.TCPAddr).Port

	fmt.Printf("A encaminhar 127.0.0.1:%d -> %s\n", *port, fs.Arg(0))
	fmt.Printf("  %s\n", creds.connectionURL(*port))
	fmt.Println("Ctrl+C para fechar.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); ln.Close() }()
	err = (&forwarder{client: c, target: t, logf: func(f string, a ...any) { fmt.Printf(f+"\n", a...) }}).serve(ctx, ln)
	if ctx.Err() != nil {
		fmt.Println("\nTúnel fechado.")
		return nil
	}
	return err
}

// forwarder abre um túnel (bilhete + WebSocket) por cada ligação local --
// clientes como o DBeaver abrem várias ao mesmo tempo.
type forwarder struct {
	client *client
	target target
	logf   func(string, ...any)
	open   atomic.Int64
}

func (f *forwarder) serve(ctx context.Context, ln net.Listener) error {
	for {
		local, err := ln.Accept()
		if err != nil {
			return err
		}
		go f.handle(ctx, local)
	}
}

func (f *forwarder) handle(ctx context.Context, local net.Conn) {
	defer local.Close()
	var tk ticket
	if err := f.client.call("POST", f.target.envPath("addons", f.target.name, "tunnel"), nil, &tk); err != nil {
		f.logf("ligação recusada: %v", err)
		return
	}
	conn, _, err := websocket.Dial(ctx, f.client.socketURL(tk), nil)
	if err != nil {
		f.logf("ligação recusada: %v", err)
		return
	}
	remote := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	defer remote.Close()
	f.logf("ligação aberta (%d ativa(s))", f.open.Add(1))
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	<-done
	f.logf("ligação fechada (%d ativa(s))", f.open.Add(-1))
}
