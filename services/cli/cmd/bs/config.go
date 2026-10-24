package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
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

func cmdLogin(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("bs login não leva argumentos")
	}
	in := bufio.NewReader(os.Stdin)
	def := "http://api.127.0.0.1.nip.io"
	fmt.Printf("URL da API [%s]: ", def)
	line, _ := in.ReadString('\n')
	apiURL := strings.TrimRight(strings.TrimSpace(line), "/")
	if apiURL == "" {
		apiURL = def
	}
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
	c := config{APIURL: apiURL, Token: token}
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
	return nil
}
