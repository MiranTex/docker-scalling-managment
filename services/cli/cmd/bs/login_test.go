package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryOf(t *testing.T) {
	for in, want := range map[string]string{
		"http://api.192.168.1.100.nip.io":  "registry.192.168.1.100.nip.io",
		"https://api.exemplo.pt/":          "registry.exemplo.pt",
		"http://localhost:8094":            "",
		"http://plataforma.exemplo.pt/api": "",
	} {
		if got := registryOf(in); got != want {
			t.Errorf("registryOf(%q) = %q, queria %q", in, got, want)
		}
	}
}

// O config.json do docker ganha o helper para o registry sem perder o
// resto (ex: o credsStore do Docker Desktop e outros helpers).
func TestRegisterCredHelperKeepsDockerConfig(t *testing.T) {
	dir := t.TempDir()
	orig := `{"credsStore":"desktop","credHelpers":{"ghcr.io":"gh"},"currentContext":"desktop-linux"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registerCredHelper(dir, "registry.exemplo.pt"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	var cfg struct {
		CredsStore     string            `json:"credsStore"`
		CredHelpers    map[string]string `json:"credHelpers"`
		CurrentContext string            `json:"currentContext"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.CredsStore != "desktop" || cfg.CurrentContext != "desktop-linux" || cfg.CredHelpers["ghcr.io"] != "gh" || cfg.CredHelpers["registry.exemplo.pt"] != "basestack" {
		t.Errorf("config.json = %s", raw)
	}
	// Sem config.json: cria-o.
	empty := t.TempDir()
	if err := registerCredHelper(empty, "registry.exemplo.pt"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(empty, "config.json")); len(raw) == 0 {
		t.Error("não criou o config.json")
	}
}
