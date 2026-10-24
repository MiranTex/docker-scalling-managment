package platformapi

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Túneis: a CLI (bs port-forward) abre uma porta local e, por cada ligação
// que lá chega, pede um bilhete e abre um WebSocket; o launcher liga ao
// addon na rede do ambiente e passa os bytes nos dois sentidos. Assim um
// cliente na máquina do developer (psql, DBeaver) chega à base de dados
// sem ela ter porta aberta para fora.

func (a *API) addonTunnel(w http.ResponseWriter, r *http.Request, who principal) {
	target, addr, err := a.svc.AddonTunnelAddress(r.Context(), r.PathValue("project"), r.PathValue("env"), r.PathValue("addon"))
	if err != nil {
		a.fail(w, err)
		return
	}
	ticket := a.term.issue(terminalTicket{subject: who.Subject, target: target, tunnel: addr})
	writeJSON(w, http.StatusCreated, map[string]any{
		"url": a.socketURL(target.Project.Slug, "tunnel", ticket), "container": target.Container,
		"expiresInSeconds": int(ticketTTL / time.Second),
	})
}

// addonCredentials revela a password do addon -- só a developers e
// admins (os mesmos que veem o env pelo terminal), e fica na auditoria.
func (a *API) addonCredentials(w http.ResponseWriter, r *http.Request, who principal) {
	p, e, addon := r.PathValue("project"), r.PathValue("env"), r.PathValue("addon")
	creds, err := a.svc.AddonCredentials(r.Context(), p, e, addon)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit.Log(who.Subject, "addon.credentials", fmt.Sprintf("%s/%s addon %s", p, e, addon), "ok")
	writeJSON(w, http.StatusOK, creds)
}

func (a *API) tunnelSocket(w http.ResponseWriter, r *http.Request) {
	tk, err := a.term.redeem(r.URL.Query().Get("ticket"), r.PathValue("project"), true)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", err.Error())
		return
	}
	defer a.term.release(tk)

	dialCtx, cancelDial := context.WithTimeout(r.Context(), 5*time.Second)
	backend, err := a.dial(dialCtx, "tcp", tk.tunnel)
	cancelDial()
	t := tk.target
	detail := fmt.Sprintf("%s/%s addon %s -> %s", t.Project.Slug, t.Environment.Slug, t.Slug, tk.tunnel)
	if err != nil {
		a.audit.Log(tk.subject, "tunnel.open", detail, "error: "+err.Error())
		writeError(w, http.StatusBadGateway, "unreachable", "não foi possível ligar ao addon: "+err.Error())
		return
	}
	defer backend.Close()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: a.term.origins})
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	client := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	defer client.Close()
	a.audit.Log(tk.subject, "tunnel.open", detail, "ok")
	started := time.Now()

	var last, up, down atomic.Int64
	last.Store(time.Now().UnixNano())
	done := make(chan struct{}, 2)
	pipe := func(dst io.Writer, src io.Reader, n *atomic.Int64) {
		buf := make([]byte, 32<<10)
		for {
			k, err := src.Read(buf)
			if k > 0 {
				last.Store(time.Now().UnixNano())
				n.Add(int64(k))
				if _, werr := dst.Write(buf[:k]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go pipe(backend, client, &up)
	go pipe(client, backend, &down)

	check := time.NewTicker(min(a.term.tunnelIdle/4, 30*time.Second))
	defer check.Stop()
	reason := "fechado"
	for waiting := true; waiting; {
		select {
		case <-done:
			waiting = false
		case <-check.C:
			if time.Since(time.Unix(0, last.Load())) >= a.term.tunnelIdle {
				reason, waiting = "fechado por inatividade", false
			}
		}
	}
	conn.Close(websocket.StatusNormalClosure, reason)
	a.audit.Log(tk.subject, "tunnel.close", detail, fmt.Sprintf("%s após %s (%d bytes enviados, %d recebidos)",
		reason, time.Since(started).Round(time.Second), up.Load(), down.Load()))
}

// dialer é como o launcher chega aos addons; os testes trocam-no.
type dialer func(ctx context.Context, network, addr string) (net.Conn, error)
