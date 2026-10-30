package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// bs login: pelo browser (o "device flow" do GitHub CLI) -- o bs mostra um
// código, abre o portal, a pessoa entra como sempre e autoriza; a
// plataforma cria-lhe uma API key só para este computador.

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	withKey := fs.Bool("api-key", false, "colar uma API key em vez de usar o browser")
	noBrowser := fs.Bool("no-browser", false, "não abrir o browser (mostra só o endereço)")
	pos, err := parseLoginArgs(fs, args)
	if err != nil {
		return err
	}
	apiURL := pos
	if apiURL == "" {
		c, _ := loadConfig()
		apiURL = firstNonEmpty(c.APIURL, "http://api.127.0.0.1.nip.io")
		fmt.Printf("Plataforma: %s  (outra: bs login <url da API>)\n", apiURL)
	}
	apiURL = strings.TrimRight(apiURL, "/")
	if *withKey {
		return loginWithKey(apiURL)
	}
	return loginWithBrowser(apiURL, !*noBrowser)
}

// parseLoginArgs aceita o URL antes ou depois das flags.
func parseLoginArgs(fs *flag.FlagSet, args []string) (string, error) {
	var pos string
	for {
		if err := fs.Parse(args); err != nil {
			return "", err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		if pos != "" {
			return "", errors.New("uso: bs login [url da API] [--api-key] [--no-browser]")
		}
		pos = fs.Arg(0)
		args = fs.Args()[1:]
	}
}

func loginWithBrowser(apiURL string, open bool) error {
	host, _ := os.Hostname()
	anon := newClient(config{APIURL: apiURL})
	var start struct {
		DeviceCode      string `json:"deviceCode"`
		UserCode        string `json:"userCode"`
		VerificationURL string `json:"verificationUrl"`
		ExpiresIn       int    `json:"expiresIn"`
		Interval        int    `json:"interval"`
	}
	if err := anon.call("POST", "/v1/cli/login", map[string]string{"name": host + " (bs)"}, &start); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == 404 {
			return errors.New("esta plataforma não tem o login pelo browser -- usa: bs login --api-key")
		}
		return fmt.Errorf("não foi possível começar o login em %s: %w", apiURL, err)
	}
	fmt.Printf("\nCódigo: %s\n\nConfirma-o no browser e autoriza:\n  %s\n\n", start.UserCode, start.VerificationURL)
	if open {
		if err := openBrowser(start.VerificationURL); err != nil {
			fmt.Println("(não consegui abrir o browser -- abre o endereço acima)")
		}
	}
	fmt.Print("À espera da autorização...")
	interval := time.Duration(max(start.Interval, 1)) * time.Second
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		var got struct {
			APIKey string `json:"apiKey"`
		}
		err := anon.call("POST", "/v1/cli/login/poll", map[string]string{"deviceCode": start.DeviceCode}, &got)
		var ae *apiError
		switch {
		case err == nil:
			fmt.Println(" autorizado.")
			return finishLogin(config{APIURL: apiURL, Token: got.APIKey})
		case errors.As(err, &ae) && ae.Status == 428:
			continue
		default:
			fmt.Println()
			return err
		}
	}
	fmt.Println()
	return errors.New("o pedido expirou sem autorização -- corre o login outra vez")
}

func openBrowser(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("sem ambiente gráfico")
		}
		return exec.Command("xdg-open", u).Start()
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// registryOf: o registry da plataforma de uma API (api.<d> -> registry.<d>).
func registryOf(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil || !strings.HasPrefix(u.Hostname(), "api.") {
		return ""
	}
	return "registry." + strings.TrimPrefix(u.Hostname(), "api.")
}

// Credential helper do docker: o bs, chamado como docker-credential-basestack
// (uma ligação ao lado do bs), dá ao docker a key da sessão para o
// registry da plataforma -- docker push/pull sem docker login.

const helperName = "basestack"

// setupDockerHelper põe o helper ao lado do bs e regista-o para o registry
// em ~/.docker/config.json (sem mexer no resto da configuração).
func setupDockerHelper(registry string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if err := ensureHelperLink(exe); err != nil {
		return err
	}
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(h, ".docker")
	}
	return registerCredHelper(dir, registry)
}

// ensureHelperLink põe docker-credential-basestack (uma ligação ao bs)
// numa pasta do PATH: a do bs, ou -- se essa pedir root -- uma do
// utilizador (~/.local/bin, ~/bin).
func ensureHelperLink(exe string) error {
	name := "docker-credential-" + helperName
	if found, err := exec.LookPath(name); err == nil {
		if target, err := filepath.EvalSymlinks(found); err == nil && target == exe {
			return nil
		}
	}
	dirs := []string{filepath.Dir(exe)}
	if h, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, ".local", "bin"), filepath.Join(h, "bin"))
	}
	inPath := map[string]bool{}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		inPath[filepath.Clean(d)] = true
	}
	for _, d := range dirs {
		if !inPath[filepath.Clean(d)] {
			continue
		}
		link := filepath.Join(d, name)
		_ = os.Remove(link)
		if err := os.Symlink(exe, link); err == nil {
			return nil
		}
	}
	return fmt.Errorf("sem uma pasta do PATH onde escrever -- corre: sudo ln -sf %s %s", exe, filepath.Join(filepath.Dir(exe), name))
}

// registerCredHelper põe credHelpers[registry] = basestack no config.json
// do docker, preservando tudo o resto.
func registerCredHelper(dir, registry string) error {
	path := filepath.Join(dir, "config.json")
	cfg := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("%s não é JSON válido: %w", path, err)
		}
	}
	helpers, _ := cfg["credHelpers"].(map[string]any)
	if helpers == nil {
		helpers = map[string]any{}
	}
	if helpers[registry] == helperName {
		return nil
	}
	helpers[registry] = helperName
	cfg["credHelpers"] = helpers
	raw, _ := json.MarshalIndent(cfg, "", "\t")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// credentialHelper implementa o protocolo docker-credential-* (get, store,
// erase, list): só o get dá alguma coisa -- a sessão do bs, para o
// registry da plataforma dela.
func credentialHelper(args []string) error {
	if len(args) == 0 {
		return errors.New("uso: docker-credential-basestack get|store|erase|list")
	}
	c, _ := loadConfig()
	registry := registryOf(c.APIURL)
	switch args[0] {
	case "get":
		var server string
		if _, err := fmt.Fscanln(os.Stdin, &server); err != nil && server == "" {
			return err
		}
		host := server
		if u, err := url.Parse(server); err == nil && u.Host != "" {
			host = u.Host
		}
		if registry == "" || host != registry || c.Token == "" {
			// O protocolo: "credentials not found" no stdout e exit 1.
			fmt.Print("credentials not found in native keychain")
			os.Exit(1)
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"ServerURL": server, "Username": "bs", "Secret": c.Token})
	case "list":
		out := map[string]string{}
		if registry != "" && c.Token != "" {
			out[registry] = "bs"
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	case "store", "erase":
		// docker login/logout: a sessão é do bs (bs login).
		_, _ = os.Stdin.Read(make([]byte, 4096))
		return nil
	}
	return fmt.Errorf("ação desconhecida %q", args[0])
}
