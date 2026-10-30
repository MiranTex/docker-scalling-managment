package main

import (
	"bytes"
	"crypto/x509"
	"flag"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testSettings() Settings {
	s := defaultSettings()
	s.AdminEmail = "admin@example.test"
	s.Secrets = generateSecrets()
	return s
}

func TestRenderUsesGeneratedSecretsAndOwnNames(t *testing.T) {
	dir := t.TempDir()
	s := testSettings()
	if err := render(dir, s); err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile(filepath.Join(dir, "compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	c := string(compose)
	for _, want := range []string{
		"name: basestack", "basestack-platform", "container_name: basestack-traefik",
		s.Secrets.LauncherSecretsKey, s.Secrets.SecretsMasterKey, s.Secrets.GrafanaAdminPassword,
		"postgres://launcher:" + s.Secrets.LauncherDBPassword + "@database",
		"image: basestack/launcher:dev", `"3100:3000"`, `HOSTNAME: "0.0.0.0"`, "Host(`api.127.0.0.1.nip.io`)",
		`AUTH_BOOTSTRAP_SUPERADMIN_EMAIL: "admin@example.test"`,
		"Host(`portal.127.0.0.1.nip.io`)", `GF_SERVER_ROOT_URL: "http://portal.127.0.0.1.nip.io/grafana/"`,
		`LAUNCHER_TERMINAL_ORIGINS: "portal.127.0.0.1.nip.io,localhost:3100,127.0.0.1:3100"`,
		"image: registry:2.8.3", `REGISTRY_AUTH_TOKEN_REALM: "https://registry.127.0.0.1.nip.io/token"`,
		`LAUNCHER_REGISTRY_HOST: "registry.127.0.0.1.nip.io"`, s.Secrets.RegistryPullPassword,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("compose.yml não tem %q", want)
		}
	}
	// Nada da demo: chaves fixas, admin/admin, redes e volumes partilhados.
	for _, bad := range []string{"Gb6oAL1A3uEIJGpHTDeN4YkGTQzI3LDAlii", "dYqiYX4FVzR2YzOtaCVk1lJuscTZf1ewnLZORaqqgWc", "auth-net", "observability-net", "auth-signing-key\n    name:", ":-admin}", "POSTGRES_PASSWORD: app"} {
		if strings.Contains(c, bad) {
			t.Errorf("compose.yml ainda tem %q da demo", bad)
		}
	}
	if info, _ := os.Stat(filepath.Join(dir, "compose.yml")); info.Mode().Perm() != 0o600 {
		t.Errorf("compose.yml com permissões %v, quero 0600 (tem passwords)", info.Mode().Perm())
	}

	sql, _ := os.ReadFile(filepath.Join(dir, "config/db-init/01-roles.sql"))
	for _, r := range s.DBRoles() {
		if !strings.Contains(string(sql), "CREATE ROLE "+r.Name+" WITH LOGIN PASSWORD '"+r.Password+"'") {
			t.Errorf("roles sem %s", r.Name)
		}
	}
	for _, f := range []string{"config/prometheus.yml", "config/loki-config.yml", "config/promtail-config.yml", "config/dashboards/projects.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("falta %s", f)
		}
	}
}

func TestSecretsAreUniquePerInstall(t *testing.T) {
	a, b := generateSecrets(), generateSecrets()
	if a.LauncherSecretsKey == b.LauncherSecretsKey || a.DatabasePassword == b.DatabasePassword {
		t.Error("duas instalações com os mesmos segredos")
	}
}

// As cópias em assets/ têm de ser iguais às de services/monitoring, que a
// demo ainda usa -- senão uma correção num sítio não chega ao outro.
func TestAssetsMatchMonitoring(t *testing.T) {
	pairs := map[string]string{
		"assets/prometheus.yml":      "../../../monitoring/prometheus/prometheus.yml",
		"assets/loki-config.yml":     "../../../monitoring/loki/loki-config.yml",
		"assets/promtail-config.yml": "../../../monitoring/promtail/promtail-config.yml",
	}
	dashboards, _ := filepath.Glob("../../../monitoring/grafana/dashboards/*.json")
	for _, d := range dashboards {
		pairs["assets/dashboards/"+filepath.Base(d)] = d
	}
	for embedded, original := range pairs {
		want, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		got, err := assets.ReadFile(embedded)
		if err != nil {
			t.Errorf("%s não está embutido", embedded)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s difere de %s -- copia de novo", embedded, original)
		}
	}
}

// O compose gerado é válido para o próprio docker compose (se houver
// Docker nesta máquina).
func TestComposeIsValid(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("sem docker")
	}
	dir := t.TempDir()
	if err := render(dir, testSettings()); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("docker", "compose", "--file", filepath.Join(dir, "compose.yml"), "config", "--quiet").CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, out)
	}
}

func TestLANIPIsUsable(t *testing.T) {
	if ip := lanIP(); net.ParseIP(ip) == nil {
		t.Errorf("lanIP() = %q", ip)
	}
}

func TestTLSMaterial(t *testing.T) {
	dir := t.TempDir()
	s := testSettings()
	first, err := ensureTLS(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := readPEM(filepath.Join(dir, "tls/ca.crt"))
	caCert, _ := x509.ParseCertificate(ca)
	leaf, _ := readPEM(filepath.Join(dir, "tls/traefik", first.RegistryCert))
	cert, err := x509.ParseCertificate(leaf)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	for _, host := range []string{"registry.127.0.0.1.nip.io", "api.127.0.0.1.nip.io"} {
		if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: host}); err != nil {
			t.Errorf("o certificado não é válido para %s pela CA: %v", host, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tls/token.key")); err != nil {
		t.Error("falta a chave dos tokens")
	}

	again, _ := ensureTLS(dir, s)
	if again != first {
		t.Errorf("certificado reemitido sem motivo: %v -> %v", first, again)
	}
	s.Domain = "192.168.1.50.nip.io"
	moved, _ := ensureTLS(dir, s)
	if moved == first {
		t.Error("domínio novo sem certificado novo")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "tls/traefik/registry-*"))
	if len(left) != 2 {
		t.Errorf("em tls/traefik ficaram %v, quero só o par atual", left)
	}
	ca2, _ := readPEM(filepath.Join(dir, "tls/ca.crt"))
	if !bytes.Equal(ca, ca2) {
		t.Error("a CA mudou -- as máquinas que confiavam nela deixavam de confiar")
	}
}

// Numa máquina sem a CA instalada, o script põe-na nos dois sítios que o
// docker do Linux lê e reinicia-o.
func TestLinuxTrustScript(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureTLS(dir, testSettings()); err != nil {
		t.Fatal(err)
	}
	script, err := linuxTrust(filepath.Join(dir, "tls", "ca.crt"), "registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/etc/docker/certs.d/registry.example.test/ca.crt",
		"basestack-registry.example.test.crt",
		filepath.Join(dir, "tls", "ca.crt"),
		"systemctl restart docker",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("o script não tem %q:\n%s", want, script)
		}
	}
}

func TestFillMissingSecretsKeepsExisting(t *testing.T) {
	s := testSettings()
	keep := s.Secrets.LauncherSecretsKey
	s.Secrets.RegistryPullPassword = ""
	if !s.fillMissingSecrets() || s.Secrets.RegistryPullPassword == "" {
		t.Error("não gerou o segredo em falta")
	}
	if s.Secrets.LauncherSecretsKey != keep {
		t.Error("trocou um segredo que já existia")
	}
	if s.fillMissingSecrets() {
		t.Error("segunda vez sem nada em falta disse que mudou")
	}
}

func TestFlagsAfterNames(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	push := fs.Bool("push", false, "")
	platform := fs.String("platform", "", "")
	names, err := parseInterspersed(fs, []string{"agent", "--platform", "linux/amd64", "launcher", "--push"})
	if err != nil || strings.Join(names, ",") != "agent,launcher" || !*push || *platform != "linux/amd64" {
		t.Errorf("names=%v push=%v platform=%q err=%v", names, *push, *platform, err)
	}
}

func TestPrivateDomain(t *testing.T) {
	for d, want := range map[string]bool{
		"192.168.1.100.nip.io":        true,
		"api.10.0.0.5.nip.io":         true,
		"192-168-1-10.sslip.io":       true,
		"127.0.0.1.nip.io":            true,
		"localhost":                   true,
		"servidor.local":              true,
		"203.0.113.10.nip.io":         false, // IP público
		"exemplo.pt":                  false,
		"plataforma.minhaempresa.com": false,
	} {
		if got := privateDomain(d); got != want {
			t.Errorf("privateDomain(%q) = %v, queria %v", d, got, want)
		}
	}
}

func TestUfwScriptPerRole(t *testing.T) {
	platform := ufwScript(rolePlatform, "192.168.1.0/24")
	node := ufwScript(roleNode, "192.168.1.0/24")
	for _, want := range []string{"port 7946", "port 4789 proto udp", "proto esp", "from 192.168.1.0/24"} {
		if !strings.Contains(platform, want) || !strings.Contains(node, want) {
			t.Errorf("falta %q:\n%s\n%s", want, platform, node)
		}
	}
	if !strings.Contains(platform, "port 2377 proto tcp") || strings.Contains(node, "2377") {
		t.Error("a 2377 (manager) é só da plataforma")
	}
}
