package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
	// ImageSource: registry de onde o up busca as imagens da plataforma
	// que faltam (ex: registry.<domínio>/basestack de outra instalação).
	// Vazio = construídas aqui (basestack dev build).
	ImageSource string `json:"imageSource,omitempty"`
	// Cluster: esta máquina é manager de um swarm e as redes dos ambientes
	// são overlay -- os nós remotos recebem apps. Só Linux.
	Cluster bool `json:"cluster,omitempty"`
	// FirewallApplied: as regras da ufw para o cluster já foram aplicadas
	// (basestack firewall) -- o up deixa de as oferecer.
	FirewallApplied bool `json:"firewallApplied,omitempty"`

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
	// BackupRecipient: a chave pública (age) com que se cifra a cópia da
	// configuração no storage. A privada fica com quem opera, fora daqui
	// (basestack backup init). Vazio = sem cópia da configuração.
	BackupRecipient string `json:"backupRecipient,omitempty"`

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
	// Registry: a credencial interna do launcher (busca imagens) e o
	// segredo HTTP do próprio registry.
	RegistryPullPassword string `json:"registryPullPassword"`
	RegistryHTTPSecret   string `json:"registryHttpSecret"`
	// MFAKey cifra os segredos do 2FA na base (AES-256, base64). Perdê-la
	// obriga a reconfigurar o 2FA de todas as contas.
	MFAKey string `json:"mfaKey"`
	// O storage da plataforma (backups): a credencial S3 de quem lá
	// escreve, e a base das chaves JWT das portas internas dele.
	StorageAccessKey string `json:"storageAccessKey"`
	StorageSecretKey string `json:"storageSecretKey"`
	StorageJWTKey    string `json:"storageJwtKey"`
}

// lanIP é o IP desta máquina na rede local (o da interface que sai para
// fora) -- o domínio por defeito é <ip>.nip.io, que as outras máquinas da
// rede também resolvem. UDP não envia nada: só escolhe a interface.
func lanIP() string {
	conn, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// privateDomain: um domínio que só existe na rede local (<ip privado>.nip.io,
// localhost, .local) -- o Let's Encrypt nunca lá chega para validar, por
// isso o HTTPS automático não pode funcionar.
func privateDomain(domain string) bool {
	d := strings.ToLower(strings.TrimSuffix(domain, "."))
	if d == "localhost" || strings.HasSuffix(d, ".localhost") || strings.HasSuffix(d, ".local") {
		return true
	}
	for _, suffix := range []string{".nip.io", ".sslip.io"} {
		host, ok := strings.CutSuffix(d, suffix)
		if !ok {
			continue
		}
		// O IP são os últimos rótulos (app.192.168.1.10.nip.io) ou o
		// último com hífenes (app.192-168-1-10.nip.io).
		labels := strings.Split(host, ".")
		candidates := []string{strings.ReplaceAll(labels[len(labels)-1], "-", ".")}
		if len(labels) >= 4 {
			candidates = append(candidates, strings.Join(labels[len(labels)-4:], "."))
		}
		for _, c := range candidates {
			if ip := net.ParseIP(c); ip != nil {
				return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
			}
		}
	}
	return false
}

var errACMEPrivate = errors.New("o HTTPS automático (Let's Encrypt) não funciona com um domínio da rede local: o Let's Encrypt precisa de chegar a esta máquina pela internet. Deixa o email vazio (o registry e os nós continuam em HTTPS com a CA da instalação)")

// AdvertiseAddr: o IP pelo qual os nós chegam a esta máquina (swarm).
func (s Settings) AdvertiseAddr() string { return lanIP() }

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
		RegistryPullPassword: password(), RegistryHTTPSecret: password(), MFAKey: key(),
		StorageAccessKey: accessKey(), StorageSecretKey: password(), StorageJWTKey: password(),
	}
}

// fillMissingSecrets gera os segredos que uma versão mais nova acrescentou
// e que uma instalação antiga ainda não tem. Nunca troca um que exista.
func (s *Settings) fillMissingSecrets() bool {
	fresh, changed := generateSecrets(), false
	fill := func(cur *string, v string) {
		if *cur == "" {
			*cur, changed = v, true
		}
	}
	fill(&s.Secrets.RegistryPullPassword, fresh.RegistryPullPassword)
	fill(&s.Secrets.RegistryHTTPSecret, fresh.RegistryHTTPSecret)
	fill(&s.Secrets.MFAKey, fresh.MFAKey)
	fill(&s.Secrets.StorageAccessKey, fresh.StorageAccessKey)
	fill(&s.Secrets.StorageSecretKey, fresh.StorageSecretKey)
	fill(&s.Secrets.StorageJWTKey, fresh.StorageJWTKey)
	return changed
}

// accessKey: o "utilizador" S3, no formato das chaves da AWS.
func accessKey() string {
	b := make([]byte, 10)
	mustRead(b)
	return strings.ToUpper(hex.EncodeToString(b))
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

// RegistryHost é o registry da plataforma.
func (s Settings) RegistryHost() string { return "registry." + s.Domain }

// PortalURL é por onde se entra na plataforma, desta ou de outra máquina:
// portal.<domínio>, pelo Traefik.
func (s Settings) PortalURL() string { return s.scheme() + "://portal." + s.Domain }

// LocalPortalURL: a porta do portal publicada diretamente, só útil nesta
// máquina (ou se o domínio não resolver).
func (s Settings) LocalPortalURL() string { return "http://localhost:" + strconv.Itoa(s.PortalPort) }

func (s Settings) APIURL() string { return s.scheme() + "://api." + s.Domain }

func (s Settings) scheme() string {
	if s.TLSCertResolver() != "" {
		return "https"
	}
	return "http"
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
	return strings.Join(append([]string{"portal." + s.Domain, "localhost:" + p, "127.0.0.1:" + p}, s.ExtraTerminalOrigins...), ",")
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
