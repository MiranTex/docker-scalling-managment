package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type client struct {
	cfg  config
	http *http.Client
}

func newClient(c config) *client {
	return &client{cfg: c, http: &http.Client{Timeout: 30 * time.Second}}
}

// apiError é o formato {error, message} de toda a API.
type apiError struct {
	Status  int
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("erro %d", e.Status)
}

func (c *client) call(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.cfg.APIURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		e := &apiError{Status: resp.StatusCode}
		_ = json.NewDecoder(resp.Body).Decode(e)
		return e
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ticket é a resposta de .../terminal e .../tunnel: o WebSocket a abrir.
type ticket struct {
	URL       string `json:"url"`
	Container string `json:"container"`
}

// socketURL torna o url de um bilhete absoluto -- sem API pública, o
// launcher devolve só o caminho.
func (c *client) socketURL(t ticket) string {
	if !strings.HasPrefix(t.URL, "/") {
		return t.URL
	}
	return "ws" + strings.TrimPrefix(c.cfg.APIURL, "http") + t.URL
}

// target é <projeto>/<ambiente>/<nome>.
type target struct{ project, env, name string }

func parseTarget(s string) (target, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return target{}, fmt.Errorf("alvo %q inválido -- usa <projeto>/<ambiente>/<serviço ou addon>", s)
	}
	return target{parts[0], parts[1], parts[2]}, nil
}

func (t target) envPath(rest ...string) string {
	parts := []string{"/v1/projects", url.PathEscape(t.project), "environments", url.PathEscape(t.env)}
	for _, r := range rest {
		parts = append(parts, url.PathEscape(r))
	}
	return strings.Join(parts, "/")
}
