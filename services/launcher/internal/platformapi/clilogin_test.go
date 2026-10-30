package platformapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCLILoginThroughTheBrowser(t *testing.T) {
	api, srv := setupAPIWith(t, &termDocker{}, &termDocker{})
	api.WithPortalURL("http://portal.example.test")
	req := func(token, method, path string, body any) (int, map[string]any) {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		r, _ := http.NewRequest(method, srv.URL+path, &buf)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		raw, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	// O bs começa, sem sessão.
	code, start := req("", "POST", "/v1/cli/login", map[string]string{"name": "MacBook (bs)"})
	if code != 201 {
		t.Fatalf("início: %d %v", code, start)
	}
	device, user := start["deviceCode"].(string), start["userCode"].(string)
	if !strings.HasPrefix(start["verificationUrl"].(string), "http://portal.example.test/cli-login?code="+user) || len(user) != 9 {
		t.Fatalf("códigos = %v", start)
	}
	if code, _ := req("", "POST", "/v1/cli/login/poll", map[string]string{"deviceCode": device}); code != 428 {
		t.Errorf("antes de autorizar: %d, queria 428", code)
	}

	// A página do portal precisa de sessão.
	if code, _ := req("", "GET", "/v1/cli/login/"+user, nil); code != 401 {
		t.Errorf("sem sessão: %d", code)
	}
	code, info := req("t-dev", "GET", "/v1/cli/login/"+strings.ToLower(user), nil)
	if code != 200 || info["name"] != "MacBook (bs)" {
		t.Errorf("pedido visto pelo portal: %d %v", code, info)
	}
	if code, _ := req("t-dev", "POST", "/v1/cli/login/"+user+"/approve", map[string]string{"apiKey": "ak_nova"}); code != 204 {
		t.Fatalf("aprovar: %d", code)
	}
	if code, _ := req("t-dev", "POST", "/v1/cli/login/"+user+"/approve", map[string]string{"apiKey": "ak_outra"}); code != 404 {
		t.Errorf("aprovar duas vezes: %d", code)
	}

	// O bs recebe a key -- uma vez só; um código errado não recebe nada.
	if code, _ := req("", "POST", "/v1/cli/login/poll", map[string]string{"deviceCode": "errado"}); code != 410 {
		t.Errorf("código errado: %d", code)
	}
	code, got := req("", "POST", "/v1/cli/login/poll", map[string]string{"deviceCode": device})
	if code != 200 || got["apiKey"] != "ak_nova" {
		t.Fatalf("poll: %d %v", code, got)
	}
	if code, _ := req("", "POST", "/v1/cli/login/poll", map[string]string{"deviceCode": device}); code != 410 {
		t.Errorf("segunda vez: %d, queria 410", code)
	}
}
