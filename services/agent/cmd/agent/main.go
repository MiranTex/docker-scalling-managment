// O agente corre em cada nó remoto da plataforma. Liga-se ao launcher
// (https://api.<domínio>/v1/agent/connect) e mantém a ligação; por cima
// dela vai um yamux em que o launcher abre um canal por pedido, e o agente
// liga cada canal ao socket do Docker desta máquina. O nó não abre portas.
//
// Configuração em /etc/basestack/node.json (escrito pelo "basestack node
// join") e a CA da instalação em /etc/basestack/ca.crt.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

const version = "0.1.2"

type nodeConfig struct {
	APIURL    string `json:"apiUrl"`
	Node      string `json:"node"`
	NodeToken string `json:"nodeToken"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dir := envOr("BASESTACK_NODE_DIR", "/etc/basestack")
	socket := envOr("DOCKER_SOCKET", "/var/run/docker.sock")
	cfg, client, err := load(dir)
	if err != nil {
		logger.Error("configuração do nó", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := serve(ctx, logger, cfg, client, socket)
		if ctx.Err() != nil {
			return
		}
		var unauthorized errUnauthorized
		switch {
		case errors.As(err, &unauthorized):
			// O nó foi removido (ou a credencial não presta): insistir não
			// adianta, mas também não custa -- pode ser um engano a corrigir.
			logger.Error("o launcher recusou a credencial do nó -- foi removido? (basestack node join outra vez)", "err", err)
			backoff = time.Minute
		case time.Since(started) > time.Minute:
			// Esteve ligado: é uma queda nova, recomeça rápido.
			backoff = time.Second
			logger.Warn("ligação ao launcher caiu", "err", err)
		default:
			logger.Warn("sem ligação ao launcher", "err", err, "retry", backoff.String())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

type errUnauthorized struct{ msg string }

func (e errUnauthorized) Error() string { return e.msg }

func load(dir string) (nodeConfig, *http.Client, error) {
	var cfg nodeConfig
	raw, err := os.ReadFile(dir + "/node.json")
	if err != nil {
		return cfg, nil, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, nil, fmt.Errorf("node.json: %w", err)
	}
	if cfg.APIURL == "" || cfg.NodeToken == "" {
		return cfg, nil, errors.New("node.json sem apiUrl ou nodeToken")
	}
	// Confia nas CAs do sistema (domínio público) e na da instalação.
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if ca, err := os.ReadFile(dir + "/ca.crt"); err == nil {
		pool.AppendCertsFromPEM(ca)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	return cfg, client, nil
}

// serve liga-se uma vez e atende canais até a ligação cair.
func serve(ctx context.Context, logger *slog.Logger, cfg nodeConfig, client *http.Client, socket string) error {
	url := strings.Replace(strings.TrimRight(cfg.APIURL, "/"), "https://", "wss://", 1) + "/v1/agent/connect"
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: http.Header{
			"Authorization": {"Bearer " + cfg.NodeToken}, "X-Agent-Version": {version},
			// O IP desta máquina na rede: o launcher pode estar atrás de um
			// NAT (ex: Docker Desktop) e não ver o verdadeiro.
			"X-Node-Address": {localIP(url)},
		},
	})
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errUnauthorized{fmt.Sprintf("%s: 401", url)}
		}
		return err
	}
	conn.SetReadLimit(16 << 20)
	nc := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	defer nc.Close()

	ycfg := yamux.DefaultConfig()
	ycfg.LogOutput = io.Discard
	// Dar depressa pela falta do launcher (o mesmo do lado dele).
	ycfg.KeepAliveInterval = 5 * time.Second
	ycfg.ConnectionWriteTimeout = 5 * time.Second
	session, err := yamux.Server(nc, ycfg)
	if err != nil {
		return err
	}
	defer session.Close()
	logger.Info("ligado ao launcher", "node", cfg.Node, "url", url, "version", version)
	for {
		stream, err := session.Accept()
		if err != nil {
			return err
		}
		go pipe(stream, socket, logger)
	}
}

// pipe liga um canal do launcher ao socket do Docker.
func pipe(stream net.Conn, socket string, logger *slog.Logger) {
	defer stream.Close()
	docker, err := net.Dial("unix", socket)
	if err != nil {
		logger.Error("abrindo o socket do Docker", "err", err)
		return
	}
	defer docker.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(docker, stream); closeWrite(docker); done <- struct{}{} }()
	go func() { _, _ = io.Copy(stream, docker); done <- struct{}{} }()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

// localIP é o IP da interface por onde se chega ao launcher (o agente
// corre com a rede da máquina). UDP não envia nada: só escolhe a rota.
func localIP(apiURL string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(apiURL, "wss://"), "ws://")
	host, _, _ = strings.Cut(host, "/")
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "443")
	}
	conn, err := net.Dial("udp", host)
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
