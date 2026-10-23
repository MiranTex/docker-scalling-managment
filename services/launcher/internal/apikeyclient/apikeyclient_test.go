package apikeyclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestIntrospectCachesByKey(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in struct{ Key string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Key != "ak_boa" {
			_ = json.NewEncoder(w).Encode(Result{Active: false})
			return
		}
		_ = json.NewEncoder(w).Encode(Result{Active: true, Owner: "u1", Scopes: []string{"read", "platform"}, Role: "infra-admin"})
	}))
	defer srv.Close()
	c := New(srv.URL)

	for i := 0; i < 3; i++ {
		r, err := c.Introspect(context.Background(), "ak_boa")
		if err != nil || !r.Active || !r.HasScope("platform") || r.Role != "infra-admin" {
			t.Fatalf("introspect = %+v, %v", r, err)
		}
	}
	if r, _ := c.Introspect(context.Background(), "ak_ma"); r.Active {
		t.Error("key desconhecida não pode estar ativa")
	}
	if calls.Load() != 2 {
		t.Errorf("chamadas ao auth = %d, queria 2 (uma por key, o resto em cache)", calls.Load())
	}
}
