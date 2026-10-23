// Package authusers resolve um email numa conta do auth service
// (POST /v1/users/resolve), em nome de quem fez o pedido -- o launcher
// reencaminha o access token dele em vez de ter credenciais próprias.
package authusers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrNoAccount: não existe nenhuma conta com esse email.
var ErrNoAccount = errors.New("não existe nenhuma conta com esse email -- a pessoa tem de se registar primeiro")

type Client struct {
	authURL string
	http    *http.Client
}

func New(authServiceURL string) *Client {
	return &Client{authURL: strings.TrimRight(authServiceURL, "/"), http: &http.Client{Timeout: 5 * time.Second}}
}

func (c *Client) Resolve(ctx context.Context, bearer, email string) (string, string, error) {
	body, _ := json.Marshal(map[string]string{"email": email})
	req, err := http.NewRequestWithContext(ctx, "POST", c.authURL+"/v1/users/resolve", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("authusers: pedindo ao auth: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", "", ErrNoAccount
	default:
		return "", "", fmt.Errorf("authusers: auth respondeu %d", resp.StatusCode)
	}
	var out struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("authusers: resposta ilegível: %w", err)
	}
	return out.ID, out.Email, nil
}
