package main

import (
	"bs/internal/trust"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// "basestack node": esta máquina como nó remoto de uma plataforma que corre
// noutra. O join troca o token de entrada (criado no portal, Nós >
// Adicionar nó) por uma credencial do nó e arranca o agente
// (basestack-agent), que se liga ao launcher e lhe dá acesso ao Docker
// daqui. Um nó não tem a plataforma instalada.

const agentContainer = "basestack-agent"

// nodeFile é o que o join guarda (e o agente lê) em nodeHome().
type nodeFile struct {
	APIURL     string `json:"apiUrl"`
	Node       string `json:"node"`
	NodeToken  string `json:"nodeToken"`
	Registry   string `json:"registry"`
	AgentImage string `json:"agentImage"`
	// Firewall: as regras da ufw para o cluster já foram aplicadas.
	Firewall bool `json:"firewall,omitempty"`
}

// nodeHome: ~/.basestack-node (como root, /var/lib/basestack-node);
// BASESTACK_NODE_HOME muda-o.
func nodeHome() (string, error) {
	if d := os.Getenv("BASESTACK_NODE_HOME"); d != "" {
		return d, nil
	}
	if os.Geteuid() == 0 {
		return "/var/lib/basestack-node", nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".basestack-node"), nil
}

func loadNode() (nodeFile, string, error) {
	dir, err := nodeHome()
	if err != nil {
		return nodeFile{}, "", err
	}
	var n nodeFile
	raw, err := os.ReadFile(filepath.Join(dir, "node.json"))
	if errors.Is(err, os.ErrNotExist) {
		return n, dir, errors.New("esta máquina não é um nó -- basestack node join <url> --token ... --ca ...")
	}
	if err != nil {
		return n, dir, err
	}
	return n, dir, json.Unmarshal(raw, &n)
}

func cmdNode(args []string) error {
	usage := errors.New("uso: basestack node token | join <url> --token bsj_... --ca sha256:... [--name <nome>] | set-api <url> | start | status | leave")
	if len(args) == 0 {
		return usage
	}
	switch args[0] {
	case "token":
		// Na máquina da plataforma: um token de entrada sem passar pelo
		// portal (o mesmo de Nós > Adicionar nó).
		_, dir, err := loadSettings()
		if err != nil {
			return err
		}
		return compose(dir, "exec", "-T", "launcher", "/usr/local/bin/launcher", "join-token").Run()
	case "join":
		return nodeJoin(args[1:])
	case "start":
		n, dir, err := loadNode()
		if err != nil {
			return err
		}
		return startAgent(n, dir)
	case "status":
		return nodeStatus()
	case "set-api":
		if len(args) != 2 {
			return errors.New("uso: basestack node set-api https://api.<domínio novo>")
		}
		return nodeSetAPI(args[1])
	case "leave":
		return nodeLeave()
	}
	return usage
}

var nodeNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

func defaultNodeName() string {
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(strings.ToLower(h), ".")
	return strings.Trim(nodeNameRe.ReplaceAllString(h, "-"), "-")
}

func nodeJoin(args []string) error {
	fs := flag.NewFlagSet("node join", flag.ContinueOnError)
	token := fs.String("token", "", "token de entrada (bsj_..., do portal: Nós > Adicionar nó)")
	caHash := fs.String("ca", "", "impressão digital da CA da plataforma (sha256:...)")
	name := fs.String("name", defaultNodeName(), "nome deste nó na plataforma")
	// O URL vem antes das flags (é o que o portal mostra).
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("uso: basestack node join <url> --token bsj_... --ca sha256:...")
	}
	apiURL := strings.TrimRight(args[0], "/")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !strings.HasPrefix(apiURL, "https://") || *token == "" || !strings.HasPrefix(*caHash, "sha256:") {
		return errors.New("precisa do URL https://, de --token e de --ca sha256:... (copia o comando do portal)")
	}
	if _, _, err := loadSettings(); err == nil {
		return errors.New("esta máquina tem a plataforma instalada -- um nó não pode ter as duas (basestack uninstall primeiro)")
	}
	if n, _, err := loadNode(); err == nil {
		return fmt.Errorf("esta máquina já é o nó %q de %s -- basestack node leave primeiro", n.Node, n.APIURL)
	}
	dir, err := nodeHome()
	if err != nil {
		return err
	}

	// 1. A CA: buscada sem verificar e conferida pela impressão digital
	// que veio do portal -- a partir daqui, só se fala com quem a tem.
	caPEM, err := trust.FetchVerified(apiURL, *caHash)
	if err != nil {
		return err
	}
	client, err := clientWithCA(caPEM)
	if err != nil {
		return err
	}

	// 2. A credencial do nó.
	body, _ := json.Marshal(map[string]string{"token": *token, "name": *name})
	resp, err := client.Post(apiURL+"/v1/agent/join", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ligando a %s: %w", apiURL, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		var e struct{ Message string }
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("a plataforma recusou a entrada (%d): %s", resp.StatusCode, firstNonEmpty(e.Message, string(raw)))
	}
	var n nodeFile
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "ca.crt"), caPEM, 0o644); err != nil {
		return err
	}
	if err := saveNode(dir, n); err != nil {
		return err
	}
	fmt.Printf("Registado como o nó %q. Credencial guardada em %s.\n", n.Node, dir)

	// 3. O Docker daqui tem de confiar no registry da plataforma (para
	// buscar o agente e, mais tarde, as apps), e a firewall tem de deixar
	// passar a rede do cluster.
	if runtime.GOOS == "linux" {
		if err := trust.InstallLinux(filepath.Join(dir, "ca.crt"), n.Registry); err != nil {
			return err
		}
		if ufwActive() {
			if applied, err := offerFirewall(roleNode); err != nil {
				return err
			} else if applied {
				n.Firewall = true
				if err := saveNode(dir, n); err != nil {
					return err
				}
			}
		}
	}
	return startAgent(n, dir)
}

// clientWithCA confia nas CAs do sistema (domínio público) e na da
// plataforma.
func clientWithCA(caPEM []byte) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("CA inválida")
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}, nil
}

// startAgent busca a imagem do agente (com a credencial do nó, numa
// configuração temporária) e (re)cria o container dele.
func startAgent(n nodeFile, dir string) error {
	tmp, err := os.MkdirTemp("", "basestack-docker-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	docker := func(stdin string, args ...string) error {
		cmd := exec.Command("docker", args...)
		cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+tmp)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(stdin), os.Stdout, os.Stderr
		return cmd.Run()
	}
	if err := docker(n.NodeToken, "login", n.Registry, "--username", "node-"+n.Node, "--password-stdin"); err != nil {
		return fmt.Errorf("login no registry %s (o docker daqui confia na CA da plataforma?): %w", n.Registry, err)
	}
	fmt.Printf("==> %s\n", n.AgentImage)
	if err := docker("", "pull", "--quiet", n.AgentImage); err != nil {
		return fmt.Errorf("buscando o agente: %w", err)
	}
	_, _ = dockerOutput("rm", "--force", agentContainer)
	if _, err := dockerOutput("run", "--detach", "--name", agentContainer, "--restart", "unless-stopped",
		"--label", "base-stack.node-agent=true",
		// A rede da máquina: o agente reporta o IP verdadeiro do nó (e não
		// abre portas).
		"--network", "host",
		"--volume", "/var/run/docker.sock:/var/run/docker.sock",
		"--volume", dir+":/etc/basestack:ro",
		n.AgentImage); err != nil {
		return err
	}
	// Espera que o agente diga que se ligou.
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		logs, _ := exec.Command("docker", "logs", agentContainer).CombinedOutput()
		if strings.Contains(string(logs), "ligado ao launcher") {
			fmt.Printf("\nO nó %q está ligado a %s. Vê-o no portal, em Nós.\n", n.Node, n.APIURL)
			return nil
		}
	}
	return fmt.Errorf("o agente arrancou mas ainda não se ligou -- vê: docker logs %s", agentContainer)
}

// nodeSetAPI muda o endereço da plataforma neste nó (ex: a plataforma
// passou a um domínio público): a API, o registry e a imagem do agente
// seguem-no, e o agente religa-se. A credencial do nó não muda.
func nodeSetAPI(apiURL string) error {
	n, dir, err := loadNode()
	if err != nil {
		return err
	}
	apiURL = strings.TrimRight(apiURL, "/")
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme != "https" || !strings.HasPrefix(u.Hostname(), "api.") {
		return errors.New("o endereço é o da API da plataforma, em HTTPS: https://api.<domínio>")
	}
	registry := "registry." + strings.TrimPrefix(u.Hostname(), "api.")
	tag := "dev"
	if i := strings.LastIndex(n.AgentImage, ":"); i > 0 {
		tag = n.AgentImage[i+1:]
	}
	n.APIURL, n.Registry, n.AgentImage = apiURL, registry, registry+"/basestack/agent:"+tag
	if err := saveNode(dir, n); err != nil {
		return err
	}
	fmt.Printf("O nó %q passa a ligar-se a %s (registry %s).\n", n.Node, apiURL, registry)
	// Sem um certificado público, o docker daqui tem de confiar na CA da
	// plataforma também para o nome novo.
	if runtime.GOOS == "linux" && !trust.PubliclyTrusted(registry) {
		if err := trust.InstallLinux(filepath.Join(dir, "ca.crt"), registry); err != nil {
			return err
		}
	}
	return startAgent(n, dir)
}

func nodeStatus() error {
	n, dir, err := loadNode()
	if err != nil {
		return err
	}
	state, _ := dockerOutput("inspect", "--format", "{{.State.Status}} (desde {{.State.StartedAt}})", agentContainer)
	fmt.Printf("Nó:         %s\nPlataforma: %s\nAgente:     %s\nDados:      %s\n\n", n.Node, n.APIURL, firstNonEmpty(state, "não existe"), dir)
	cmd := exec.Command("docker", "logs", "--tail", "5", agentContainer)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stdout
	_ = cmd.Run()
	return nil
}

// nodeLeave tira esta máquina da plataforma: avisa o launcher (que apaga a
// credencial), remove o agente e os ficheiros do nó.
func nodeLeave() error {
	n, dir, err := loadNode()
	if err != nil {
		return err
	}
	if caPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt")); err == nil {
		if client, err := clientWithCA(caPEM); err == nil {
			req, _ := http.NewRequest(http.MethodPost, n.APIURL+"/v1/agent/leave", nil)
			req.Header.Set("Authorization", "Bearer "+n.NodeToken)
			if resp, err := client.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
				fmt.Printf("Aviso: a plataforma não confirmou a saída (%v) -- remove o nó %q no portal.\n", errOrStatus(resp, err), n.Node)
			}
		}
	}
	_, _ = dockerOutput("rm", "--force", agentContainer)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Printf("Esta máquina deixou de ser o nó %q.\n", n.Node)
	return nil
}

func errOrStatus(resp *http.Response, err error) string {
	if err != nil {
		return err.Error()
	}
	return resp.Status
}
