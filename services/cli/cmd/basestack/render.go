package main

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"
)

// assets são cópias de services/monitoring (prometheus, loki, promtail,
// dashboards) mais os templates do compose e das roles do Postgres -- o
// teste TestAssetsMatchMonitoring avisa se as cópias se afastarem.
//
//go:embed assets
var assets embed.FS

// render escreve em dir tudo o que o "docker compose" precisa: o
// compose.yml e config/ (montado nos containers). É reescrito a cada up,
// por isso mudar settings.json e correr "basestack up" aplica a mudança.
func render(dir string, s Settings) error {
	funcs := template.FuncMap{"image": s.image}
	files := map[string]struct {
		tmpl string
		mode os.FileMode
	}{
		// Têm passwords: só o dono lê o compose; o SQL tem de ser legível
		// pelo Postgres dentro do container (o diretório de cima é 0700).
		"compose.yml":                 {"assets/compose.yml.tmpl", 0o600},
		"config/db-init/01-roles.sql": {"assets/db-init.sql.tmpl", 0o644},
	}
	for out, f := range files {
		raw, err := assets.ReadFile(f.tmpl)
		if err != nil {
			return err
		}
		t, err := template.New(out).Funcs(funcs).Option("missingkey=error").Parse(string(raw))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, s); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, out), buf.Bytes(), f.mode); err != nil {
			return err
		}
	}
	// O resto vai tal e qual.
	copies := map[string]string{
		"assets/prometheus.yml":      "config/prometheus.yml",
		"assets/loki-config.yml":     "config/loki-config.yml",
		"assets/promtail-config.yml": "config/promtail-config.yml",
	}
	for from, to := range copies {
		raw, err := assets.ReadFile(from)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, to), raw, 0o644); err != nil {
			return err
		}
	}
	dashboards, _ := fs.Glob(assets, "assets/dashboards/*.json")
	for _, d := range dashboards {
		raw, _ := assets.ReadFile(d)
		if err := writeFile(filepath.Join(dir, "config/dashboards", filepath.Base(d)), raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
