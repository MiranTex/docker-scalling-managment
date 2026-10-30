package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"bs/internal/trust"
)

type config struct {
	APIURL string `json:"apiUrl"`
	Token  string `json:"token"`
}

// configPath é ~/.config/bs/config.json em todos os sistemas -- é onde se
// procura a configuração de uma CLI.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "bs", "config.json"), nil
}

// loadConfig lê o ficheiro e aplica BS_API_URL/BS_TOKEN por cima.
func loadConfig() (config, error) {
	var c config
	if p, err := configPath(); err == nil {
		if raw, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(raw, &c)
		}
	}
	if v := os.Getenv("BS_API_URL"); v != "" {
		c.APIURL = v
	}
	if v := os.Getenv("BS_TOKEN"); v != "" {
		c.Token = v
	}
	c.APIURL = strings.TrimRight(c.APIURL, "/")
	if c.APIURL == "" || c.Token == "" {
		return c, errors.New("sem sessão -- corre \"bs login\" (ou define BS_API_URL e BS_TOKEN)")
	}
	return c, nil
}

func saveConfig(c config) (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	raw, _ := json.MarshalIndent(c, "", "  ")
	// 0600: a API key dá o teu acesso a todos os teus projetos.
	return p, os.WriteFile(p, append(raw, '\n'), 0o600)
}

// loginWithKey é o login antigo: o URL e uma API key colada (bs login
// --api-key) -- para scripts ou quando não há browser.
func loginWithKey(apiURL string) error {
	in := bufio.NewReader(os.Stdin)
	fmt.Print("API key (ak_..., com o scope \"platform\"): ")
	var token string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(raw))
	} else {
		line, _ := in.ReadString('\n')
		token = strings.TrimSpace(line)
	}
	return finishLogin(config{APIURL: apiURL, Token: token})
}

// finishLogin confirma a key na API, guarda a configuração e põe o docker
// desta máquina a usá-la no registry da plataforma.
func finishLogin(c config) error {
	var projects []struct {
		Slug string `json:"slug"`
	}
	if err := newClient(c).call("GET", "/v1/projects", nil, &projects); err != nil {
		return fmt.Errorf("não foi possível entrar: %w", err)
	}
	p, err := saveConfig(c)
	if err != nil {
		return err
	}
	fmt.Printf("Sessão guardada em %s (%d projeto(s) visível(eis)).\n", p, len(projects))
	if registry := registryOf(c.APIURL); registry != "" {
		if err := setupDockerHelper(registry); err != nil {
			fmt.Printf("Aviso: o docker não ficou configurado para %s (%v).\n", registry, err)
		} else {
			fmt.Printf("docker push/pull em %s usam esta sessão (sem docker login).\n", registry)
		}
		offerTrust(c.APIURL, registry)
	}
	return nil
}

// offerTrust: se o docker desta máquina ainda não confia no registry da
// plataforma (a CA da instalação), mostra a impressão digital -- a mesma
// da página de autorização no portal -- e oferece-se para a instalar.
func offerTrust(apiURL, registry string) {
	if _, err := exec.LookPath("docker"); err != nil {
		return // sem docker, não há o que configurar
	}
	caPEM, hash, err := trust.Fetch(apiURL)
	if err != nil {
		// Ex: um domínio público com Let's Encrypt, sem CA própria a servir.
		return
	}
	if trust.Trusted(caPEM, registry) {
		return
	}
	fmt.Printf("\nO docker desta máquina ainda não confia no registry %s.\nImpressão digital da CA da plataforma (confere com a do portal):\n    %s\n", registry, hash)
	if !trust.IsTerminal(os.Stdin) {
		fmt.Println("Sem terminal para confirmar -- mais tarde: bs login outra vez (ou basestack trust).")
		return
	}
	if !trust.Confirm("Confiar nela?") {
		fmt.Println("Ficou por fazer: docker push/pull vão recusar o HTTPS do registry.")
		return
	}
	if err := trust.Install(caPEM, registry); err != nil {
		fmt.Printf("Aviso: %v\n", err)
	}
}
