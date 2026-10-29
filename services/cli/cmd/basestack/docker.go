package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// Não corre aqui: é o que os nós remotos buscam ao registry.
	{"agent", "services/agent"},
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
