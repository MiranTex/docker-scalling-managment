// Package dockerclient fala diretamente com a API HTTP do Docker Engine
// via unix socket, sem depender do SDK oficial (github.com/docker/docker).
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
