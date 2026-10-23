package dockerclient

import "testing"

func frame(stream byte, s string) []byte {
	n := len(s)
	return append([]byte{stream, 0, 0, 0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, s...)
}

func TestDemuxLogs(t *testing.T) {
	raw := append(frame(1, "a\n"), frame(2, "erro\n")...)
	if got := demuxLogs(raw); got != "a\nerro\n" {
		t.Errorf("demux = %q", got)
	}
	// Container com TTY: sem cabeçalhos, devolve tal e qual.
	if got := demuxLogs([]byte("plain text here")); got != "plain text here" {
		t.Errorf("tty = %q", got)
	}
	// Frame cortado a meio (tail): devolve o que houver.
	if got := demuxLogs(frame(1, "abcdef")[:11]); got != "abc" {
		t.Errorf("truncado = %q", got)
	}
}

func TestRegistryHost(t *testing.T) {
	cases := map[string]string{
		"nginx:alpine":                 "docker.io",
		"library/nginx":                "docker.io",
		"eu/app:1":                     "docker.io",
		"ghcr.io/eu/app:sha":           "ghcr.io",
		"localhost:5000/app":           "localhost:5000",
		"registry.example.com:443/a/b": "registry.example.com:443",
		"localhost/app":                "localhost",
	}
	for ref, want := range cases {
		if got := RegistryHost(ref); got != want {
			t.Errorf("RegistryHost(%q) = %q, queria %q", ref, got, want)
		}
	}
}
