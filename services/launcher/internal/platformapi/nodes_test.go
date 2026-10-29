package platformapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// Os nós são da plataforma: só os admins dela os veem e alteram.
func TestNodesAreForPlatformAdmins(t *testing.T) {
	_, srv := setupAPIWith(t, &termDocker{}, &termDocker{})
	do := func(token, method, path string, body any) int {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req, _ := http.NewRequest(method, srv.URL+path, &buf)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, c := range []struct {
		token, method, path string
		body                any
		want                int
	}{
		{"t-platform", "GET", "/v1/nodes", nil, 200},
		{"t-dev", "GET", "/v1/nodes", nil, 403},
		{"t-dev", "PATCH", "/v1/nodes/local", map[string]bool{"schedulable": false}, 403},
		{"t-platform", "PATCH", "/v1/nodes/nao-existe", map[string]bool{"schedulable": false}, 404},
		{"t-platform", "PATCH", "/v1/nodes/local", map[string]string{}, 400},
	} {
		if got := do(c.token, c.method, c.path, c.body); got != c.want {
			t.Errorf("%s %s %s = %d, queria %d", c.token, c.method, c.path, got, c.want)
		}
	}
}
