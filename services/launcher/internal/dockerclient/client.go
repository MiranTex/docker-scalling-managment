// Package dockerclient fala diretamente com a API HTTP do Docker Engine
// via unix socket, sem depender do SDK oficial (github.com/docker/docker).
//
// Movido de services/autoscaler/internal/dockerclient: o launcher passa a
// ser o único componente que efetivamente cria/inicia containers (réplicas
// de um group ou instâncias "solo") -- é por isso que ele, e não mais o
// cmd/group, resolve segredos e fala com o socket do Docker para ESSA
// operação. O cmd/group mantém a sua PRÓPRIA cópia deste pacote (ver
// services/autoscaler/internal/dockerclient) só para leitura/monitorização
// (listar containers, stats de CPU, parar/remover réplicas no scale down)
// -- operações que nunca tocam segredos e por isso não precisavam de mudar
// de dono.
package dockerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

// Client encapsula a conexão com o daemon do Docker.
type Client struct {
	http    *http.Client
	baseURL string
}

// New cria um Client que conversa com o Docker via unix socket.
// socketPath normalmente é "/var/run/docker.sock".
func New(socketPath string) *Client {
	return &Client{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
			},
		},
		// O host "docker" é só um placeholder exigido pelo pacote net/http;
		// quem decide o destino real é o DialContext acima, que sempre
		// abre o unix socket independente do host na URL.
		baseURL: "http://docker",
	}
}

// NewWithDialer cria um Client que chega ao Docker por outra via -- ex:
// um canal do túnel até ao agente de um nó remoto. Cada ligação HTTP abre
// a sua com dial.
func NewWithDialer(dial func(ctx context.Context) (net.Conn, error)) *Client {
	return &Client{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
				// Os canais são baratos e o túnel pode cair: não vale a
				// pena guardar ligações paradas.
				DisableKeepAlives: true,
			},
		},
		baseURL: "http://docker",
	}
}

// apiError representa o corpo de erro que o Docker devolve em respostas 4xx/5xx.
type apiError struct {
	Message string `json:"message"`
}

// do monta a requisição, envia e decodifica a resposta em out (se não for nil).
// body é serializado como JSON quando não for nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("dockerclient: encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("dockerclient: building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("dockerclient: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var apiErr apiError
		raw, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(raw, &apiErr); err != nil || apiErr.Message == "" {
			return fmt.Errorf("dockerclient: %s %s: status %d: %s", method, path, resp.StatusCode, string(raw))
		}
		return fmt.Errorf("dockerclient: %s %s: status %d: %s", method, path, resp.StatusCode, apiErr.Message)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("dockerclient: decoding response: %w", err)
	}
	return nil
}

// doRaw é do() para respostas que não são JSON (ex: logs) -- devolve o
// corpo inteiro.
func (c *Client) doRaw(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("dockerclient: building request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dockerclient: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("dockerclient: reading body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("dockerclient: %s %s: status %d: %s", method, path, resp.StatusCode, string(body))
	}
	return body, nil
}

// SystemInfo é o subconjunto de /info que descreve a máquina.
type SystemInfo struct {
	Name          string `json:"Name"`
	NCPU          int    `json:"NCPU"`
	MemTotal      int64  `json:"MemTotal"`
	Architecture  string `json:"Architecture"` // do kernel: x86_64, aarch64
	OSType        string `json:"OSType"`
	ServerVersion string `json:"ServerVersion"`
}

func (c *Client) Info(ctx context.Context) (*SystemInfo, error) {
	var info SystemInfo
	if err := c.do(ctx, "GET", "/info", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}
