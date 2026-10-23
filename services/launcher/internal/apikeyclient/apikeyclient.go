// Package apikeyclient valida API keys pessoais (ak_...) contra o auth
// service (POST /v1/api-keys/introspect). O resultado fica em cache uns
// segundos -- um script que faz muitos pedidos não valida cada um no auth
// -- guardado pelo hash da key, nunca pela key em claro.
package apikeyclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Prefix identifica uma API key pessoal (ver services/auth/internal/apikey).
const Prefix = "ak_"

// cacheTTL é quanto tempo um resultado vale. Também é o máximo que uma key
// revogada (ou um dono que perdeu a role) continua a funcionar.
const cacheTTL = 30 * time.Second

type Result struct {
	Active bool     `json:"active"`
	Owner  string   `json:"owner"`
	Scopes []string `json:"scopes"`
	Role   string   `json:"role"`
}

// HasScope diz se a key tem este scope.
func (r Result) HasScope(scope string) bool {
	for _, s := range r.Scopes {
		if strings.TrimSpace(s) == scope {
			return true
		}
	}
	return false
}

type entry struct {
	result  Result
	expires time.Time
}

type Client struct {
	authURL string
	http    *http.Client
	mu      sync.Mutex
	cache   map[string]entry
}

func New(authServiceURL string) *Client {
	return &Client{
		authURL: strings.TrimRight(authServiceURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
		cache:   map[string]entry{},
	}
}

func (c *Client) Introspect(ctx context.Context, key string) (Result, error) {
	sum := sha256.Sum256([]byte(key))
	id := hex.EncodeToString(sum[:])
	now := time.Now()

	c.mu.Lock()
	if e, ok := c.cache[id]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.result, nil
	}
	c.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"key": key})
	req, err := http.NewRequestWithContext(ctx, "POST", c.authURL+"/v1/api-keys/introspect", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("apikeyclient: validando API key no auth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("apikeyclient: auth respondeu %d", resp.StatusCode)
	}
	var r Result
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Result{}, fmt.Errorf("apikeyclient: resposta ilegível: %w", err)
	}

	c.mu.Lock()
	// Limpeza preguiçosa: o cache nunca cresce além das keys em uso.
	for k, e := range c.cache {
		if now.After(e.expires) {
			delete(c.cache, k)
		}
	}
	c.cache[id] = entry{result: r, expires: now.Add(cacheTTL)}
	c.mu.Unlock()
	return r, nil
}
