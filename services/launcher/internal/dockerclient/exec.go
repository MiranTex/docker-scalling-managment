package dockerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// ExecConfig é o processo a abrir dentro de um container que já corre
// (POST /containers/{id}/exec) -- o terminal do portal.
type ExecConfig struct {
	Cmd []string
	Env []string
	// Tty liga um pseudo-terminal: o output vem cru (sem o cabeçalho de 8
	// bytes por bloco) e o stdin fica ligado.
	Tty bool
}

// CreateExec prepara o processo e devolve o id do exec.
func (c *Client) CreateExec(ctx context.Context, containerID string, cfg ExecConfig) (string, error) {
	body := map[string]any{
		"Cmd": cfg.Cmd, "Env": cfg.Env, "Tty": cfg.Tty,
		"AttachStdin": cfg.Tty, "AttachStdout": true, "AttachStderr": true,
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(containerID)+"/exec", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// StartExec arranca o exec e devolve a ligação ao processo: o que se
// escreve vai para o stdin, o que se lê é o output. O daemon troca o
// protocolo HTTP pela ligação crua (101 Switching Protocols); fechar a
// ligação é da responsabilidade de quem chama.
func (c *Client) StartExec(ctx context.Context, execID string, tty bool) (io.ReadWriteCloser, error) {
	encoded, _ := json.Marshal(map[string]bool{"Detach": false, "Tty": tty})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/exec/"+url.PathEscape(execID)+"/start", bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("dockerclient: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dockerclient: request failed: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("dockerclient: start exec %s: status %d: %s", execID, resp.StatusCode, bytes.TrimSpace(raw))
	}
	conn, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, fmt.Errorf("dockerclient: start exec %s: ligação não é bidirecional", execID)
	}
	return conn, nil
}

// StartExecDetached arranca o exec sem esperar por ele -- o fim e o exit
// code leem-se depois com InspectExec.
func (c *Client) StartExecDetached(ctx context.Context, execID string) error {
	return c.do(ctx, http.MethodPost, "/exec/"+url.PathEscape(execID)+"/start", map[string]bool{"Detach": true}, nil)
}

// ResizeExec muda o tamanho do pseudo-terminal (linhas x colunas).
func (c *Client) ResizeExec(ctx context.Context, execID string, rows, cols int) error {
	q := url.Values{"h": {strconv.Itoa(rows)}, "w": {strconv.Itoa(cols)}}
	return c.do(ctx, http.MethodPost, "/exec/"+url.PathEscape(execID)+"/resize?"+q.Encode(), nil, nil)
}

type ExecInspect struct {
	Running  bool `json:"Running"`
	ExitCode int  `json:"ExitCode"`
}

func (c *Client) InspectExec(ctx context.Context, execID string) (*ExecInspect, error) {
	var out ExecInspect
	if err := c.do(ctx, http.MethodGet, "/exec/"+url.PathEscape(execID)+"/json", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
