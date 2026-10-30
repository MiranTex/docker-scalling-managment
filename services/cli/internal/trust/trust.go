// Package trust põe o docker de uma máquina a confiar no registry de uma
// plataforma base-stack: sem domínio público, o certificado do registry é
// assinado pela CA da própria instalação, que cada máquina aceita uma vez
// -- conferida pela impressão digital. Usado pelo basestack (trust, node
// join, up) e pelo bs (login).
package trust

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Hash é a impressão digital de uma CA em PEM: sha256 do certificado.
func Hash(caPEM []byte) string {
	b, _ := pem.Decode(caPEM)
	if b == nil {
		return ""
	}
	sum := sha256.Sum256(b.Bytes)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Fetch busca a CA de uma plataforma sem a conferir -- quem chama confere
// a impressão digital (FetchVerified, ou mostrando-a a uma pessoa). O
// endereço é o da API (http ou https); a CA serve-se só por HTTPS.
func Fetch(apiURL string) ([]byte, string, error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return nil, "", err
	}
	u.Scheme, u.Path = "https", "/v1/agent/ca"
	insecure := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := insecure.Get(u.String())
	if err != nil {
		return nil, "", fmt.Errorf("ligando a %s: %w", u.Host, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	hash := Hash(raw)
	if resp.StatusCode != http.StatusOK || hash == "" {
		return nil, "", fmt.Errorf("%s não devolveu a CA da plataforma (%d)", u.Host, resp.StatusCode)
	}
	return raw, hash, nil
}

// FetchVerified busca a CA e exige a impressão digital esperada.
func FetchVerified(apiURL, want string) ([]byte, error) {
	raw, got, err := Fetch(apiURL)
	if err != nil {
		return nil, err
	}
	if got != want {
		return nil, fmt.Errorf("a CA de %s não é a esperada (%s, queria %s) -- o URL está certo? alguém no meio?", apiURL, got, want)
	}
	return raw, nil
}

// dockerDesktop: o docker desta máquina é o Docker Desktop (Mac, ou Linux
// com o Docker Desktop) -- o daemon corre numa VM, que não vê as CAs do
// sistema nem o /etc/docker: lê ~/.docker/certs.d, e só ao arrancar.
func dockerDesktop() bool {
	if runtime.GOOS == "darwin" {
		return true
	}
	out, err := exec.Command("docker", "info", "--format", "{{.OperatingSystem}}").Output()
	return err == nil && strings.Contains(string(out), "Docker Desktop")
}

// desktopPath: onde o Docker Desktop procura a CA de um registry.
func desktopPath(host string) (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".docker", "certs.d", host, "ca.crt"), nil
}

// Trusted diz se o docker desta máquina já confia nesta CA para o host.
func Trusted(caPEM []byte, host string) bool {
	switch {
	case dockerDesktop():
		p, err := desktopPath(host)
		if err != nil {
			return false
		}
		cur, err := os.ReadFile(p)
		return err == nil && string(cur) == string(caPEM)
	case runtime.GOOS == "linux":
		script, err := linuxScript(caPEM, "-", host)
		return err == nil && script == ""
	}
	return false
}

// Install põe o docker desta máquina a confiar na CA para o host. Com o
// Docker Desktop (Mac ou Linux) é um ficheiro do utilizador e um reinício;
// com o Docker Engine (Linux) pede root (ver InstallLinux).
func Install(caPEM []byte, host string) error {
	switch {
	case dockerDesktop():
		p, err := desktopPath(host)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, caPEM, 0o644); err != nil {
			return err
		}
		restart := "pelo ícone do Docker Desktop"
		if runtime.GOOS == "linux" {
			restart = "systemctl --user restart docker-desktop"
		}
		fmt.Printf("CA instalada em %s.\n  Reinicia o Docker Desktop uma vez para ele confiar em %s (%s).\n", p, host, restart)
		return nil
	case runtime.GOOS == "linux":
		f, err := os.CreateTemp("", "basestack-ca-*.crt")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err := f.Write(caPEM); err != nil {
			return err
		}
		f.Close()
		_ = os.Chmod(f.Name(), 0o644)
		return InstallLinux(f.Name(), host)
	}
	return fmt.Errorf("instala à mão a CA de %s no docker desta máquina", host)
}

// LinuxScript: no Linux o docker confia no registry por dois sítios --
// /etc/docker/certs.d/<host>/ca.crt e as CAs do sistema (com o image store
// do containerd, o pedido do token usa as do sistema) -- e ambos pedem
// root. Devolve o script que falta correr ("" se já está tudo).
func LinuxScript(caPath, host string) (string, error) {
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return "", err
	}
	return linuxScript(ca, caPath, host)
}

func linuxScript(ca []byte, caPath, host string) (string, error) {
	same := func(path string) bool {
		b, err := os.ReadFile(path)
		return err == nil && string(b) == string(ca)
	}
	certsD := filepath.Join("/etc/docker/certs.d", host, "ca.crt")
	// Debian/Ubuntu ou RHEL/Fedora.
	system, update := "/usr/local/share/ca-certificates/basestack-"+host+".crt", "update-ca-certificates"
	if _, err := exec.LookPath("update-ca-certificates"); err != nil {
		if _, err := exec.LookPath("update-ca-trust"); err == nil {
			system, update = "/etc/pki/ca-trust/source/anchors/basestack-"+host+".crt", "update-ca-trust"
		}
	}
	if same(certsD) && same(system) {
		return "", nil
	}
	// Sem systemd (ex: Docker-in-Docker), reiniciar o Docker é com quem o
	// gere.
	return fmt.Sprintf("set -e\nmkdir -p %s\ncp %s %s\ncp %s %s\n%s\n"+
		"if command -v systemctl >/dev/null 2>&1; then systemctl restart docker; else echo 'Reinicia o Docker para ele ler a CA nova.'; fi\n",
		filepath.Dir(certsD), caPath, certsD, caPath, system, update), nil
}

// InstallLinux instala a CA (ver LinuxScript). Pede root: como root corre
// logo; num terminal pergunta antes de usar o sudo; sem terminal só mostra
// os comandos. Reinicia o Docker.
func InstallLinux(caPath, host string) error {
	script, err := LinuxScript(caPath, host)
	if err != nil || script == "" {
		return err
	}
	fmt.Printf("\nPara o docker desta máquina confiar no registry (%s), é preciso como root:\n\n%s\n", host, Indent(script))
	var cmd *exec.Cmd
	switch {
	case os.Geteuid() == 0:
		cmd = exec.Command("sh", "-c", script)
	case IsTerminal(os.Stdin):
		if !Confirm("Correr agora com sudo (reinicia o Docker)?") {
			fmt.Println("Ficou por fazer: o docker vai recusar o registry até correres isto.")
			return nil
		}
		cmd = exec.Command("sudo", "sh", "-c", script)
	default:
		fmt.Println("Ficou por fazer (sem terminal para pedir o sudo).")
		return nil
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("instalando a CA: %w", err)
	}
	return nil
}

// Confirm pergunta sim/não no terminal (não, por defeito).
func Confirm(question string) bool {
	fmt.Print(question + " [s/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "s" || a == "sim" || a == "y" || a == "yes"
}

func Indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ")
}

func IsTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
