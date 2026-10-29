package platformapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"

	"launcher/internal/platform"
)

// Os agentes dos nós remotos (ver platform/agents.go). Rotas públicas em
// /v1/agent -- o Traefik só as serve por HTTPS:
//
//	GET  /v1/agent/ca       a CA da instalação (o join confere a impressão digital)
//	POST /v1/agent/join     token de entrada -> credencial do nó
//	GET  /v1/agent/connect  WebSocket do agente (Bearer bsn_...)
//	POST /v1/agent/leave    o nó sai sozinho (Bearer bsn_...)

// AgentConfig liga os nós remotos. CACertFile é a CA da instalação (tls/
// ca.crt); PublicURL é https://api.<domínio>, por onde os nós chegam.
type AgentConfig struct {
	PublicURL    string
	CACertFile   string
	RegistryHost string
	AgentImage   string
}

type agentSettings struct {
	cfg    AgentConfig
	caPEM  []byte
	caHash string // sha256:<hex> do DER da CA
}

func (a *API) WithAgents(cfg AgentConfig) error {
	raw, err := os.ReadFile(cfg.CACertFile)
	if err != nil {
		return err
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		return errors.New("agentes: a CA não está em PEM")
	}
	sum := sha256.Sum256(b.Bytes)
	a.agents = &agentSettings{cfg: cfg, caPEM: raw, caHash: "sha256:" + hex.EncodeToString(sum[:])}
	return nil
}

func (a *API) agentsEnabled(w http.ResponseWriter) bool {
	if a.agents == nil {
		writeError(w, http.StatusNotFound, "not_found", "os nós remotos não estão ligados nesta instalação")
		return false
	}
	return true
}

// createJoinToken: o token e o comando pronto a correr no nó novo.
func (a *API) createJoinToken(w http.ResponseWriter, r *http.Request, who principal) {
	if !a.agentsEnabled(w) {
		return
	}
	t, err := a.svc.CreateJoinToken(r.Context(), who.Subject)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "node.join-token", "", "criado, expira "+t.ExpiresAt.Format(time.RFC3339))
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": t.Token, "expiresAt": t.ExpiresAt, "caHash": a.agents.caHash,
		"command": fmt.Sprintf("basestack node join %s --token %s --ca %s", a.agents.cfg.PublicURL, t.Token, a.agents.caHash),
	})
}

func (a *API) deleteNode(w http.ResponseWriter, r *http.Request, who principal) {
	name := r.PathValue("node")
	if err := a.svc.RemoveNode(r.Context(), name); err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "node.remove", name, "ok")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) checkNode(w http.ResponseWriter, r *http.Request, _ principal) {
	out, err := a.svc.CheckNode(r.Context(), r.PathValue("node"))
	if err != nil {
		if errors.Is(err, platform.ErrNotFound) {
			a.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "output": out, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

func (a *API) agentCA(w http.ResponseWriter, r *http.Request) {
	if !a.agentsEnabled(w) {
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write(a.agents.caPEM)
}

func (a *API) agentJoin(w http.ResponseWriter, r *http.Request) {
	if !a.agentsEnabled(w) {
		return
	}
	var body struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	addr := clientIP(r)
	nodeToken, err := a.svc.JoinNode(r.Context(), body.Token, body.Name, addr)
	if errors.Is(err, platform.ErrUnauthorizedNode) {
		a.audit.Log("node:"+body.Name, "node.join", addr, "recusado")
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log("node:"+body.Name, "node.join", addr, "ok")
	writeJSON(w, http.StatusCreated, map[string]any{
		"node": body.Name, "nodeToken": nodeToken, "apiUrl": a.agents.cfg.PublicURL,
		"registry": a.agents.cfg.RegistryHost, "agentImage": a.agents.cfg.AgentImage,
	})
}

// agentConnect: o agente abre aqui o WebSocket e fica ligado; por cima vai
// o túnel até ao Docker do nó.
func (a *API) agentConnect(w http.ResponseWriter, r *http.Request) {
	if !a.agentsEnabled(w) {
		return
	}
	node, err := a.svc.AuthenticateNode(r.Context(), bearer(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	// O yamux escreve tramas até à janela (256 KB) numa mensagem só.
	conn.SetReadLimit(16 << 20)
	ctx := r.Context()
	nc := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	defer nc.Close()
	if err := a.svc.ServeAgent(ctx, node.Name, nc, r.Header.Get("X-Agent-Version"), clientIP(r)); err != nil && ctx.Err() == nil {
		a.logger.Info("ligação do agente terminou", "node", node.Name, "err", err)
	}
}

func (a *API) agentLeave(w http.ResponseWriter, r *http.Request) {
	if !a.agentsEnabled(w) {
		return
	}
	node, err := a.svc.AuthenticateNode(r.Context(), bearer(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	if err := a.svc.RemoveNode(r.Context(), node.Name); err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log("node:"+node.Name, "node.leave", clientIP(r), "ok")
	w.WriteHeader(http.StatusNoContent)
}

// clientIP: o IP de quem pede, através do Traefik.
func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		ip, _, _ := strings.Cut(f, ",")
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
