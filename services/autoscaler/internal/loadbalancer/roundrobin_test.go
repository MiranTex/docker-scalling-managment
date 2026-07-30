package loadbalancer

import "testing"

func TestRoundRobin_CiclaEmSequencia(t *testing.T) {
	rr := NewRoundRobin()
	rr.SetBackends([]Backend{
		{ContainerID: "a", Addr: "10.0.0.1:80"},
		{ContainerID: "b", Addr: "10.0.0.2:80"},
		{ContainerID: "c", Addr: "10.0.0.3:80"},
	})

	want := []string{"a", "b", "c", "a", "b", "c"}
	for i, w := range want {
		got, ok := rr.Next()
		if !ok {
			t.Fatalf("chamada %d: esperava ok=true", i)
		}
		if got.ContainerID != w {
			t.Errorf("chamada %d: got %s, want %s", i, got.ContainerID, w)
		}
	}
}

func TestRoundRobin_SemBackends(t *testing.T) {
	rr := NewRoundRobin()
	if _, ok := rr.Next(); ok {
		t.Error("esperava ok=false sem backends configurados")
	}
}

func TestRoundRobin_AtualizaLista(t *testing.T) {
	rr := NewRoundRobin()
	rr.SetBackends([]Backend{{ContainerID: "a", Addr: "10.0.0.1:80"}})
	rr.Next()

	rr.SetBackends([]Backend{{ContainerID: "b", Addr: "10.0.0.2:80"}})
	got, ok := rr.Next()
	if !ok || got.ContainerID != "b" {
		t.Errorf("got %+v, ok=%v; want backend b", got, ok)
	}
}
