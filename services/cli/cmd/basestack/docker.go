package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// O basestack usa o docker e o "docker compose" que vêm com o Docker
// (Docker Desktop no Mac, docker-ce + plugin compose no Linux).

func compose(dir string, args ...string) *exec.Cmd {
	full := append([]string{"compose", "--project-name", "basestack", "--file", filepath.Join(dir, "compose.yml")}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}

// dockerOutput corre "docker args..." e devolve o stdout.
func dockerOutput(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("docker %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// containerdStore: o Docker guarda as imagens no containerd -- o que deixa
// ter várias arquiteturas numa só imagem local (e enviá-las num push).
func containerdStore() bool {
	out, err := dockerOutput("info", "--format", "{{json .DriverStatus}}")
	return err == nil && strings.Contains(out, "io.containerd.snapshotter")
}

func imageExists(ref string) bool {
	_, err := dockerOutput("image", "inspect", "--format", "{{.Id}}", ref)
	return err == nil
}

// platformImages são as imagens construídas a partir do código, com a
// pasta de cada uma no repositório.
var platformImages = []struct{ name, dir string }{
	{"database", "services/database"},
	{"auth", "services/auth"},
	{"secretsadmin", "services/secretsadmin"},
	{"templatesadmin", "services/templatesadmin"},
	{"launcher", "services/launcher"},
	{"portal", "services/portal"},
	{"autoscaler-group", "services/autoscaler"},
	// Não correm aqui: o agente é o que os nós buscam ao registry; o cli
	// transporta o basestack e o bs (basestack upgrade).
	{"agent", "services/agent"},
	{"cli", "services/cli"},
}

// labelVersion: a versão (commit do git) com que uma imagem foi construída.
const labelVersion = "org.opencontainers.image.version"

// imageVersion lê a versão de uma imagem local ("" se não tiver).
func imageVersion(ref string) string {
	v, err := dockerOutput("image", "inspect", "--format", `{{index .Config.Labels "`+labelVersion+`"}}`, ref)
	if err != nil || v == "<no value>" {
		return ""
	}
	return v
}

// foreignPublishers lista containers fora desta instalação que publicam
// uma destas portas no host -- arrancar por cima deles falharia a meio.
func foreignPublishers(ports ...int) ([]string, error) {
	out, err := dockerOutput("ps", "--format", `{{.Names}}|{{.Ports}}|{{.Label "com.docker.compose.project"}}`)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 || parts[2] == "basestack" {
			continue
		}
		for _, p := range ports {
			if strings.Contains(parts[1], fmt.Sprintf(":%d->", p)) {
				found = append(found, fmt.Sprintf("%s (porta %d)", parts[0], p))
			}
		}
	}
	return found, nil
}

// waitForRoutes espera que a API e o registry respondam pelo Traefik (com
// a CA da instalação): qualquer resposta que não seja 404/502/503 serve --
// um 401 quer dizer que a rota está lá.
func waitForRoutes(dir string, s Settings) error {
	caPEM, err := os.ReadFile(filepath.Join(dir, "tls", "ca.crt"))
	if err != nil {
		return err
	}
	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM(caPEM)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	// O /token do registry é servido pelo launcher: sem ele, um push ou
	// pull logo a seguir ao up falha.
	urls := []string{s.APIURL() + "/v1/projects", "https://" + s.RegistryHost() + "/v2/", "https://" + s.RegistryHost() + "/token"}
	deadline := time.Now().Add(60 * time.Second)
	for _, u := range urls {
		for {
			resp, err := client.Get(u)
			if err == nil {
				resp.Body.Close()
				if c := resp.StatusCode; c != http.StatusNotFound && c != http.StatusBadGateway && c != http.StatusServiceUnavailable {
					break
				}
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s ainda não responde pelo Traefik -- vê basestack logs traefik", u)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}
