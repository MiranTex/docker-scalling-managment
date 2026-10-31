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
	"strings"
	"sync/atomic"

	"github.com/coder/websocket"
)

type credentials struct {
	Kind     string `json:"kind"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	Port     int    `json:"port"`
	Region   string `json:"region,omitempty"`
}

// connectionURL é o que se cola no cliente (psql, DBeaver, a app local).
func (c credentials) connectionURL(port int) string {
	u := url.URL{Host: "127.0.0.1:" + strconv.Itoa(port), User: url.UserPassword(c.User, c.Password), Path: "/" + c.Database}
	switch c.Kind {
	case "redis":
		u.Scheme = "redis"
	case "storage":
		u.Scheme = "http"
	default:
		u.Scheme, u.RawQuery = "postgres", "sslmode=disable"
	}
	return u.String()
}

// clientSettings é o que se mete num cliente: o URL e, para quem prefere
// campos (ex: DBeaver, "Connect by: Host"), cada valor à parte.
func (c credentials) clientSettings(port int) string {
	var b strings.Builder
	if c.Kind == "storage" {
		// Os clientes do S3 (aws cli, rclone, Cyberduck) pedem os campos à
		// parte, não um URL.
		fmt.Fprintf(&b, "  Endpoint:   http://127.0.0.1:%d\n  Bucket:     %s\n  Região:     %s\n", port, c.Database, c.Region)
		fmt.Fprintf(&b, "  Access key: %s\n  Secret key: %s\n", c.User, c.Password)
		fmt.Fprintf(&b, "  Ex:         AWS_ACCESS_KEY_ID=%s AWS_SECRET_ACCESS_KEY=%s aws --endpoint-url http://127.0.0.1:%d s3 ls s3://%s\n", c.User, c.Password, port, c.Database)
		return b.String()
	}
	fmt.Fprintf(&b, "  URL:        %s\n", c.connectionURL(port))
	if c.Kind != "redis" {
		fmt.Fprintf(&b, "  JDBC:       jdbc:postgresql://127.0.0.1:%d/%s  (DBeaver, DataGrip)\n", port, c.Database)
	}
	fmt.Fprintf(&b, "  Host:       127.0.0.1\n  Porta:      %d\n", port)
	if c.Kind == "redis" {
		fmt.Fprintf(&b, "  Base:       %s\n", c.Database)
	} else {
		fmt.Fprintf(&b, "  Base dados: %s\n", c.Database)
	}
	fmt.Fprintf(&b, "  Utilizador: %s\n  Password:   %s\n", c.User, c.Password)
	return b.String()
}

func cmdPortForward(args []string) error {
	fs := flag.NewFlagSet("port-forward", flag.ContinueOnError)
	port := fs.Int("port", 0, "porta local (por defeito a do addon: 5432, 6379 ou 8333)")
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

	fmt.Printf("A encaminhar 127.0.0.1:%d -> %s\n\n", *port, fs.Arg(0))
	fmt.Print(creds.clientSettings(*port))
	fmt.Println("\nDeixa isto a correr enquanto usas o cliente. Ctrl+C para fechar.")

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
