package platformapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"launcher/internal/platform"
)

// Terminal no browser: o portal pede um bilhete pela API normal (com o
// papel developer, como os jobs -- a shell vê os segredos do ambiente) e
// abre com ele o WebSocket. O bilhete é a autenticação do WebSocket: o
// browser não consegue pôr um Authorization num WebSocket, e assim o
// access token nunca vai no URL.
//
// Os túneis (tunnel.go) usam os mesmos bilhetes, com limites próprios.
const (
	ticketTTL          = 30 * time.Second
	terminalIdle       = 15 * time.Minute
	maxSessionsPerUser = 3
	tunnelIdle         = 30 * time.Minute
	maxTunnelsPerUser  = 20
)

type terminalTicket struct {
	subject string
	target  platform.TerminalTarget
	debug   bool
	// tunnel: o endereço (host:porta) de um túnel; vazio = terminal.
	tunnel  string
	expires time.Time
}

type terminals struct {
	mu       sync.Mutex
	tickets  map[string]terminalTicket
	sessions map[string]int // conta (+ "/tunnel") -> abertos
	// origins: de onde o browser pode abrir o WebSocket (host[:porta] do
	// portal). Vazio = só da mesma origem; a CLI não manda Origin.
	origins    []string
	idle       time.Duration
	tunnelIdle time.Duration
}

func newTerminals() *terminals {
	return &terminals{tickets: map[string]terminalTicket{}, sessions: map[string]int{}, idle: terminalIdle, tunnelIdle: tunnelIdle}
}

// sessionKey separa os limites de terminais e de túneis.
func sessionKey(tk terminalTicket) string {
	if tk.tunnel != "" {
		return tk.subject + "/tunnel"
	}
	return tk.subject
}

// WithTerminalOrigins diz de que origens (host[:porta] do portal) o
// browser pode abrir o terminal.
func (a *API) WithTerminalOrigins(origins []string) *API {
	a.term.origins = origins
	return a
}

func (t *terminals) issue(tk terminalTicket) string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	id := hex.EncodeToString(buf)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, v := range t.tickets {
		if now.After(v.expires) {
			delete(t.tickets, k)
		}
	}
	tk.expires = now.Add(ticketTTL)
	t.tickets[id] = tk
	return id
}

// redeem gasta o bilhete (uso único) e reserva uma sessão para a conta.
// tunnel diz que tipo de bilhete o endpoint aceita.
func (t *terminals) redeem(id, project string, tunnel bool) (terminalTicket, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tk, ok := t.tickets[id]
	delete(t.tickets, id)
	if !ok || time.Now().After(tk.expires) || tk.target.Project.Slug != project || (tk.tunnel != "") != tunnel {
		return terminalTicket{}, errors.New("bilhete inválido ou expirado")
	}
	key := sessionKey(tk)
	switch {
	case tunnel && t.sessions[key] >= maxTunnelsPerUser:
		return terminalTicket{}, fmt.Errorf("já tens %d túneis abertos -- fecha alguns primeiro", maxTunnelsPerUser)
	case !tunnel && t.sessions[key] >= maxSessionsPerUser:
		return terminalTicket{}, fmt.Errorf("já tens %d terminais abertos -- fecha um primeiro", maxSessionsPerUser)
	}
	t.sessions[key]++
	return tk, nil
}

func (t *terminals) release(tk terminalTicket) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := sessionKey(tk)
	if t.sessions[key]--; t.sessions[key] <= 0 {
		delete(t.sessions, key)
	}
}

type terminalRequest struct {
	// Replica: o nome do container; vazio = a primeira a correr.
	Replica string `json:"replica"`
	Debug   bool   `json:"debug"`
}

func (a *API) serviceTerminal(w http.ResponseWriter, r *http.Request, who principal) {
	var in terminalRequest
	if !decode(w, r, &in) {
		return
	}
	p, e, s := svcPath(r)
	target, err := a.svc.ServiceTerminalTarget(r.Context(), p, e, s, in.Replica)
	a.issueTerminal(w, r, who, target, in.Debug, err)
}

func (a *API) addonTerminal(w http.ResponseWriter, r *http.Request, who principal) {
	var in terminalRequest
	if !decode(w, r, &in) {
		return
	}
	target, err := a.svc.AddonTerminalTarget(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("addon"))
	a.issueTerminal(w, r, who, target, in.Debug, err)
}

// issueTerminal confirma já aqui que a shell existe, para o portal poder
// oferecer o modo debug em vez de abrir um terminal que morre logo.
func (a *API) issueTerminal(w http.ResponseWriter, r *http.Request, who principal, target platform.TerminalTarget, debug bool, err error) {
	if err == nil && !debug {
		err = a.svc.CheckShell(r.Context(), target)
	}
	if errors.Is(err, platform.ErrNoShell) {
		writeError(w, http.StatusConflict, "no_shell", err.Error())
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	ticket := a.term.issue(terminalTicket{subject: who.Subject, target: target, debug: debug})
	writeJSON(w, http.StatusCreated, map[string]any{
		"url": a.socketURL(target.Project.Slug, "terminal", ticket), "container": target.Container, "debug": debug,
		"expiresInSeconds": int(ticketTTL / time.Second),
	})
}

// socketURL é o endereço do WebSocket de um bilhete: absoluto (ws[s]://
// api.<domínio>) quando há API pública, senão só o caminho.
func (a *API) socketURL(project, kind, ticket string) string {
	path := "/v1/projects/" + url.PathEscape(project) + "/" + kind + "/ws?ticket=" + ticket
	if base := a.svc.APIURL(); base != "" {
		return "ws" + strings.TrimPrefix(base, "http") + path
	}
	return path
}

// terminalControl são as mensagens de texto do browser; as binárias são
// o que se escreve no terminal.
type terminalControl struct {
	Type string `json:"type"` // "resize"
	Rows int    `json:"rows"`
	Cols int    `json:"cols"`
}

func (a *API) terminalSocket(w http.ResponseWriter, r *http.Request) {
	tk, err := a.term.redeem(r.URL.Query().Get("ticket"), r.PathValue("project"), false)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", err.Error())
		return
	}
	defer a.term.release(tk)

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: a.term.origins})
	if err != nil {
		return // Accept já respondeu (ex: origem não autorizada)
	}
	conn.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	t := tk.target
	detail := fmt.Sprintf("%s/%s %s %s container=%s debug=%t", t.Project.Slug, t.Environment.Slug, t.Kind, t.Slug, t.Container, tk.debug)
	term, err := a.svc.OpenTerminal(ctx, t, tk.debug)
	if err != nil {
		a.audit.Log(tk.subject, "terminal.open", detail, "error: "+err.Error())
		conn.Close(websocket.StatusInternalError, truncate("não foi possível abrir o terminal: "+err.Error(), 120))
		return
	}
	a.audit.Log(tk.subject, "terminal.open", detail, "ok")
	started := time.Now()
	if tk.debug {
		banner := "\r\n[modo debug] busybox ao lado de " + t.Container + ": mesma rede e mesmos processos (ps).\r\n" +
			"Os ficheiros do serviço estão em /proc/1/root. Este container é removido ao fechar.\r\n\r\n"
		_ = conn.Write(ctx, websocket.MessageBinary, []byte(banner))
	}

	var lastInput atomic.Int64
	lastInput.Store(time.Now().UnixNano())
	done := make(chan string, 2)

	go func() { // container -> browser
		buf := make([]byte, 32<<10)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					done <- "ligação fechada"
					return
				}
			}
			if err != nil {
				done <- "sessão terminada"
				return
			}
		}
	}()
	go func() { // browser -> container
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				done <- "ligação fechada"
				return
			}
			if typ == websocket.MessageText {
				var c terminalControl
				if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Rows > 0 && c.Cols > 0 {
					_ = term.Resize(ctx, c.Rows, c.Cols)
				}
				continue
			}
			lastInput.Store(time.Now().UnixNano())
			if _, err := term.Write(data); err != nil {
				done <- "sessão terminada"
				return
			}
		}
	}()

	check := time.NewTicker(min(a.term.idle/4, 30*time.Second))
	defer check.Stop()
	var reason string
	for reason == "" {
		select {
		case reason = <-done:
		case <-check.C:
			if time.Since(time.Unix(0, lastInput.Load())) >= a.term.idle {
				reason = "fechado por inatividade"
				_ = conn.Write(ctx, websocket.MessageBinary, []byte("\r\n\r\n[sessão fechada por inatividade]\r\n"))
			}
		}
	}
	_ = term.Close()
	conn.Close(websocket.StatusNormalClosure, reason)
	a.audit.Log(tk.subject, "terminal.close", detail, fmt.Sprintf("%s após %s", reason, time.Since(started).Round(time.Second)))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
