package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Settings é a instalação: o que se escolheu no init e os segredos
// gerados. Fica em <home>/settings.json, só legível pelo dono -- perder os
// segredos torna os dados cifrados irrecuperáveis.
type Settings struct {
	Version     string `json:"version"`
	ImagePrefix string `json:"imagePrefix"`

	Domain     string `json:"domain"`
	HTTPPort   int    `json:"httpPort"`
	HTTPSPort  int    `json:"httpsPort"`
	PortalPort int    `json:"portalPort"`
	// GrafanaPort: acesso de emergência ao Grafana, só em 127.0.0.1.
	GrafanaPort int    `json:"grafanaPort"`
	AdminEmail  string `json:"adminEmail"`
	// ACME (HTTPS automático): vazio desliga.
	ACMEEmail string `json:"acmeEmail,omitempty"`
	// ExtraTerminalOrigins: outros host:porta de onde um portal abre o
	// terminal (ex: um portal de desenvolvimento noutra porta).
	ExtraTerminalOrigins []string `json:"extraTerminalOrigins,omitempty"`

	Secrets Secrets `json:"secrets"`
}

type Secrets struct {
	DatabasePassword         string `json:"databasePassword"`
	AuthDBPassword           string `json:"authDbPassword"`
	SecretsadminDBPassword   string `json:"secretsadminDbPassword"`
	TemplatesadminDBPassword string `json:"templatesadminDbPassword"`
	LauncherDBPassword       string `json:"launcherDbPassword"`
	// Chaves AES-256 (32 bytes em base64): segredos globais e de ambiente.
	SecretsMasterKey     string `json:"secretsMasterKey"`
	LauncherSecretsKey   string `json:"launcherSecretsKey"`
	GrafanaAdminPassword string `json:"grafanaAdminPassword"`
}

func defaultSettings() Settings {
	return Settings{
		Version: "dev", ImagePrefix: "basestack/",
		Domain: "127.0.0.1.nip.io", HTTPPort: 80, HTTPSPort: 443, PortalPort: 3100, GrafanaPort: 3000,
	}
}

func generateSecrets() Secrets {
	password := func() string {
		b := make([]byte, 24)
		mustRead(b)
		return hex.EncodeToString(b)
	}
	key := func() string {
		b := make([]byte, 32)
		mustRead(b)
		return base64.StdEncoding.EncodeToString(b)
	}
	return Secrets{
		DatabasePassword: password(), AuthDBPassword: password(), SecretsadminDBPassword: password(),
		TemplatesadminDBPassword: password(), LauncherDBPassword: password(),
		SecretsMasterKey: key(), LauncherSecretsKey: key(), GrafanaAdminPassword: password(),
	}
}

func mustRead(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
}

// home é onde vive a instalação: BASESTACK_HOME, /var/lib/basestack para
// root em Linux (servidores), ~/.basestack no resto (o teu Mac).
func home() (string, error) {
	if v := os.Getenv("BASESTACK_HOME"); v != "" {
		return v, nil
	}
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		return "/var/lib/basestack", nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".basestack"), nil
}

var errNotInstalled = errors.New("a plataforma ainda não está configurada nesta máquina -- corre \"basestack init\"")

func loadSettings() (Settings, string, error) {
	dir, err := home()
	if err != nil {
		return Settings{}, "", err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, dir, errNotInstalled
	}
	if err != nil {
		return Settings{}, dir, err
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, dir, fmt.Errorf("settings.json ilegível: %w", err)
	}
	return s, dir, nil
}

func saveSettings(dir string, s Settings) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, "settings.json"), append(raw, '\n'), 0o600)
}

func (s Settings) image(name string) string { return s.ImagePrefix + name + ":" + s.Version }

// PortalURL é por onde se entra na plataforma.
func (s Settings) PortalURL() string { return "http://localhost:" + strconv.Itoa(s.PortalPort) }

func (s Settings) APIURL() string {
	scheme := "http"
	if s.TLSCertResolver() != "" {
		scheme = "https"
	}
	return scheme + "://api." + s.Domain
}

// TLSCertResolver liga HTTPS (Let's Encrypt) quando há email de ACME.
func (s Settings) TLSCertResolver() string {
	if s.ACMEEmail == "" {
		return ""
	}
	return "le"
}

// ACMECAServer: produção da Let's Encrypt. Só é usado com ACMEEmail.
func (s Settings) ACMECAServer() string { return "https://acme-v02.api.letsencrypt.org/directory" }

// TerminalOrigins: de onde o browser pode abrir o terminal do portal.
func (s Settings) TerminalOrigins() string {
	p := strconv.Itoa(s.PortalPort)
	return strings.Join(append([]string{"localhost:" + p, "127.0.0.1:" + p}, s.ExtraTerminalOrigins...), ",")
}

type dbRole struct{ Name, Password string }

func (s Settings) DBRoles() []dbRole {
	return []dbRole{
		{"auth", s.Secrets.AuthDBPassword},
		{"secretsadmin", s.Secrets.SecretsadminDBPassword},
		{"templatesadmin", s.Secrets.TemplatesadminDBPassword},
		{"launcher", s.Secrets.LauncherDBPassword},
	}
}
