package main

import (
	"bs/internal/trust"
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	def := defaultSettings()
	domain := fs.String("domain", "", "domínio base (api.<domínio>, <serviço>-<ambiente>-<projeto>.<domínio>)")
	portalPort := fs.Int("portal-port", 0, "porta do portal neste host")
	adminEmail := fs.String("admin-email", "", "email da conta que fica super-admin")
	acmeEmail := fs.String("acme-email", "", "email para certificados HTTPS (Let's Encrypt); vazio = só HTTP")
	cluster := fs.Bool("cluster", false, "modo cluster: esta máquina recebe nós remotos (swarm + redes overlay; só Linux)")
	imagesFrom := fs.String("images-from", "", "registry de onde buscar as imagens da plataforma (ex: registry.<domínio>/basestack); vazio = construí-las aqui")
	yes := fs.Bool("yes", false, "não perguntar: usa as flags e os valores por defeito")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, dir, err := loadSettings(); err == nil {
		return fmt.Errorf("já existe uma instalação em %s -- para recomeçar: basestack uninstall", dir)
	} else if !errors.Is(err, errNotInstalled) {
		return err
	}
	dir, err := home()
	if err != nil {
		return err
	}

	s := def
	in := bufio.NewReader(os.Stdin)
	ask := func(question, current string) string {
		if *yes {
			return current
		}
		if current != "" {
			fmt.Printf("%s [%s]: ", question, current)
		} else {
			fmt.Printf("%s: ", question)
		}
		line, _ := in.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
		return current
	}
	s.Domain = ask("Domínio base (<ip>.nip.io funciona em toda a rede local)", firstNonEmpty(*domain, lanIP()+".nip.io"))
	port := ask("Porta do portal", strconv.Itoa(firstNonZero(*portalPort, def.PortalPort)))
	if s.PortalPort, err = strconv.Atoi(port); err != nil || s.PortalPort <= 0 || s.PortalPort > 65535 {
		return fmt.Errorf("porta inválida: %q", port)
	}
	s.AdminEmail = ask("Email do administrador (a conta que vais registar no portal)", *adminEmail)
	if _, err := mail.ParseAddress(s.AdminEmail); err != nil {
		return fmt.Errorf("email do administrador inválido: %q", s.AdminEmail)
	}
	if privateDomain(s.Domain) {
		if *acmeEmail != "" {
			return errACMEPrivate
		}
		fmt.Println("HTTPS automático: não (o domínio é da rede local; as apps ficam em HTTP, o registry e os nós em HTTPS com a CA da instalação)")
	} else {
		s.ACMEEmail = ask("Email para HTTPS automático (Let's Encrypt; vazio = só HTTP)", *acmeEmail)
	}
	s.ImageSource = strings.TrimRight(*imagesFrom, "/")
	if *cluster && runtime.GOOS == "darwin" {
		return errors.New("o modo cluster é para servidores Linux: o Docker Desktop corre numa VM que as outras máquinas não alcançam")
	}
	s.Cluster = *cluster
	s.Secrets = generateSecrets()

	if err := saveSettings(dir, s); err != nil {
		return err
	}
	fmt.Printf("\nConfiguração e segredos guardados em %s.\n", filepath.Join(dir, "settings.json"))
	fmt.Println("Guarda uma cópia desse ficheiro num sítio seguro: sem ele, os segredos cifrados não se recuperam.")
	fmt.Println("\nA seguir: basestack up")
	return nil
}

func cmdUp(args []string) error {
	s, dir, err := loadSettings()
	if err != nil {
		return err
	}
	// Com uma origem, busca-se sempre: só as que faltam deixava ficar
	// imagens antigas de outra instalação (é rápido se nada mudou).
	if s.ImageSource != "" {
		if err := pullImages(s); err != nil {
			// Ex: a origem é o registry desta plataforma, que ainda não
			// arrancou -- com as imagens já cá, segue-se com elas.
			if !allImagesExist(s) {
				return err
			}
			fmt.Printf("Aviso: %v\n  A usar as imagens que já estão nesta máquina.\n\n", err)
		}
	}
	var missing []string
	for _, img := range platformImages {
		if !imageExists(s.image(img.name)) {
			missing = append(missing, s.image(img.name))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("faltam imagens: %s\n  constrói-as (basestack dev build) ou diz de onde vêm: basestack set images-from <registry>/basestack", strings.Join(missing, ", "))
	}
	if busy, err := foreignPublishers(s.HTTPPort, s.HTTPSPort, s.PortalPort, s.GrafanaPort); err != nil {
		return err
	} else if len(busy) > 0 {
		return fmt.Errorf("estas portas já estão ocupadas por outros containers: %s\n  pára-os primeiro (ex: a demo antiga: docker compose -f demo/docker-compose.yml stop)", strings.Join(busy, ", "))
	}
	if s.ACMEEmail != "" && privateDomain(s.Domain) {
		fmt.Printf("Aviso: %v.\n  Corrige com: basestack set acme-email \"\" && basestack up\n\n", errACMEPrivate)
	}
	if s.fillMissingSecrets() {
		if err := saveSettings(dir, s); err != nil {
			return err
		}
	}
	if err := render(dir, s); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		if err := trust.InstallLinux(filepath.Join(dir, "tls", "ca.crt"), s.RegistryHost()); err != nil {
			return err
		}
		if s.Cluster && !s.FirewallApplied && ufwActive() {
			if applied, err := offerFirewall(rolePlatform); err != nil {
				return err
			} else if applied {
				s.FirewallApplied = true
				if err := saveSettings(dir, s); err != nil {
					return err
				}
			}
		}
	}
	if err := compose(dir, append([]string{"up", "--detach", "--remove-orphans", "--wait"}, args...)...).Run(); err != nil {
		return fmt.Errorf("a plataforma não arrancou toda -- vê \"basestack status\" e \"basestack logs <componente>\": %w", err)
	}
	if runtime.GOOS == "darwin" {
		if path, changed, err := trustCA(dir, s.RegistryHost()); err != nil {
			fmt.Printf("\nAviso: não consegui pôr a CA em %s (%v).\n  O docker desta máquina não vai confiar no registry; copia %s para lá.\n",
				path, err, filepath.Join(dir, "tls", "ca.crt"))
		} else if changed {
			fmt.Printf("\nCA do registry instalada em %s.\n  Reinicia o Docker Desktop uma vez para ele confiar no registry (só ele lê essa pasta ao arrancar).\n", path)
		}
	}
	// O compose espera pelos health checks; o Traefik ainda demora a pôr
	// as rotas no ar -- os passos seguintes (e quem use a plataforma logo a
	// seguir) precisam delas.
	if err := waitForRoutes(dir, s); err != nil {
		fmt.Printf("\nAviso: %v\n", err)
	}
	// Os nós remotos buscam o agente ao registry desta plataforma.
	if imageExists(s.image("agent")) {
		if err := pushQuiet(s, "agent"); err != nil {
			fmt.Printf("\nAviso: não consegui pôr o agente no registry (%v) -- os nós novos não o vão conseguir buscar.\n  Tenta outra vez com: basestack up\n", err)
		}
	}
	printAddresses(s)
	fmt.Printf(`
Primeira vez?
  1. Regista-te em %s/register com %s
  2. basestack restart auth   (promove essa conta a super-admin)
  3. Entra de novo no portal.
`, s.PortalURL(), s.AdminEmail)
	return nil
}

func allImagesExist(s Settings) bool {
	for _, img := range platformImages {
		if !imageExists(s.image(img.name)) {
			return false
		}
	}
	return true
}

func printAddresses(s Settings) {
	fmt.Printf("\nPortal:             %s  (nesta máquina também %s)\n", s.PortalURL(), s.LocalPortalURL())
	fmt.Printf("Métricas e logs:    %s/grafana/ (pelo portal)\n", s.PortalURL())
	fmt.Printf("API (CLI bs, CI):   %s\n", s.APIURL())
	fmt.Printf("Registry:           %s  (docker login com o teu email e uma API key)\n", s.RegistryHost())
	fmt.Printf("Grafana (emergência, admin): http://127.0.0.1:%d/grafana/\n", s.GrafanaPort)
}

func cmdStatus(args []string) error {
	s, dir, err := loadSettings()
	if err != nil {
		return err
	}
	if err := compose(dir, "ps", "--all", "--format", "table {{.Service}}\t{{.State}}\t{{.Status}}").Run(); err != nil {
		return err
	}
	printAddresses(s)
	return nil
}

func cmdLogs(args []string) error {
	_, dir, err := loadSettings()
	if err != nil {
		return err
	}
	return compose(dir, append([]string{"logs", "--tail", "200"}, args...)...).Run()
}

func cmdRestart(args []string) error {
	_, dir, err := loadSettings()
	if err != nil {
		return err
	}
	return compose(dir, append([]string{"restart"}, args...)...).Run()
}

// cmdDown pára a plataforma. As apps dos projetos continuam a correr (o
// launcher é plano de controlo), a não ser com --all: aí param também,
// depois da plataforma -- com o launcher ainda no ar, o reconciliador
// voltava a criar as réplicas paradas.
func cmdDown(args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	all := fs.Bool("all", false, "parar também as apps e bases de dados dos projetos")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, dir, err := loadSettings()
	if err != nil {
		return err
	}
	if err := compose(dir, "down").Run(); err != nil {
		return err
	}
	if !*all {
		fmt.Println("Plataforma parada. Os dados ficam, e as apps dos projetos continuam a correr; \"basestack up\" volta a arrancá-la.")
		fmt.Println("Para parar também as apps: basestack down --all")
		return nil
	}
	ids, err := dockerOutput("ps", "--quiet", "--filter", "label=base-stack.project-id")
	if err != nil {
		return err
	}
	if ids != "" {
		list := strings.Fields(ids)
		fmt.Printf("A parar %d container(s) dos projetos...\n", len(list))
		// 30s como o launcher dá a cada réplica para terminar (SIGTERM).
		if _, err := dockerOutput(append([]string{"stop", "--time", "30"}, list...)...); err != nil {
			return err
		}
	}
	fmt.Println("Plataforma e apps paradas. Os dados ficam; \"basestack up\" arranca a plataforma e o launcher volta a ligar as apps e bases de dados.")
	return nil
}

func cmdDoctor(args []string) error {
	ok := true
	check := func(name string, err error) {
		if err != nil {
			ok = false
			fmt.Printf("  ✗ %s: %v\n", name, err)
			return
		}
		fmt.Printf("  ✓ %s\n", name)
	}
	_, err := dockerOutput("info", "--format", "{{.ServerVersion}}")
	check("Docker a correr", err)
	_, err = dockerOutput("compose", "version")
	check("docker compose", err)
	if mem, err := dockerOutput("info", "--format", "{{.MemTotal}}"); err == nil {
		n, _ := strconv.ParseInt(mem, 10, 64)
		if n < 4<<30 {
			check("memória", fmt.Errorf("%d MB disponíveis para o Docker; recomendado pelo menos 4096", n>>20))
		} else {
			check(fmt.Sprintf("memória (%d MB para o Docker)", n>>20), nil)
		}
	}
	s, _, err := loadSettings()
	if err != nil {
		check("instalação", err)
	} else {
		check("instalação configurada", nil)
		for _, img := range platformImages {
			if !imageExists(s.image(img.name)) {
				check("imagem "+s.image(img.name), errors.New("não existe -- basestack dev build"))
			}
		}
		busy, err := foreignPublishers(s.HTTPPort, s.HTTPSPort, s.PortalPort, s.GrafanaPort)
		if err == nil && len(busy) > 0 {
			err = fmt.Errorf("ocupadas por %s", strings.Join(busy, ", "))
		}
		check("portas livres", err)
	}
	if !ok {
		return errors.New("há problemas a resolver (acima)")
	}
	return nil
}

func cmdDev(args []string) error {
	if len(args) == 0 || args[0] != "build" {
		return errors.New("uso: basestack dev build [--source <repositório>] [--platform linux/amd64,linux/arm64] [--push | --push-to <registry>] [imagem...]")
	}
	fs := flag.NewFlagSet("dev build", flag.ContinueOnError)
	source := fs.String("source", "", "raiz do repositório base-stack (por defeito, procura a partir da pasta atual)")
	push := fs.Bool("push", false, "enviar também para o registry da plataforma (registry.<domínio>/basestack/...)")
	pushTo := fs.String("push-to", "", "enviar para o registry de outra plataforma (ex: registry.192.168.1.100.nip.io), com o teu docker login lá")
	platforms := fs.String("platform", "", "arquiteturas a construir, numa só tag (ex: linux/amd64,linux/arm64); por defeito a desta máquina")
	names, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return err
	}
	if strings.Contains(*platforms, ",") && !containerdStore() {
		return errors.New("várias arquiteturas numa imagem precisam do image store do containerd no Docker (Docker Desktop: Settings > General > \"Use containerd for pulling and storing images\"; Docker Engine 29+ já o usa)")
	}
	root := *source
	if root == "" {
		var err error
		if root, err = findRepo(); err != nil {
			return err
		}
	}
	s, _, err := loadSettings()
	if errors.Is(err, errNotInstalled) {
		s = defaultSettings()
	} else if err != nil {
		return err
	}
	only := map[string]bool{}
	for _, n := range names {
		only[n] = true
	}
	// A versão (commit do git) fica na imagem e nos binários de dentro.
	ver := gitVersion(root)
	for _, img := range selected(only) {
		fmt.Printf("==> %s (%s)\n", s.image(img.name), ver)
		build := []string{"build", "--tag", s.image(img.name), "--label", labelVersion + "=" + ver, "--build-arg", "VERSION=" + ver}
		if *platforms != "" {
			build = append(build, "--platform", *platforms)
		}
		cmd := exec.Command("docker", append(build, filepath.Join(root, img.dir))...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("construindo %s: %w", img.name, err)
		}
	}
	if *pushTo != "" {
		return pushImagesTo(s, strings.TrimRight(*pushTo, "/"), only)
	}
	if *push {
		return pushImages(s, only)
	}
	return nil
}

// gitVersion: o commit do repositório (com -dirty se há alterações por
// guardar).
func gitVersion(root string) string {
	out, err := exec.Command("git", "-C", root, "describe", "--always", "--dirty").Output()
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(out))
}

// parseInterspersed aceita flags antes e depois dos nomes ("dev build
// agent --push"): o flag do Go pára no primeiro argumento que não é flag e
// ignorava o resto em silêncio.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var names []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return names, nil
		}
		names = append(names, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// findRepo sobe a partir da pasta atual até encontrar o repositório.
func findRepo() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "services", "launcher", "Dockerfile")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("não encontrei o repositório base-stack a partir daqui -- usa --source <pasta>")
		}
		dir = parent
	}
}

// cmdPull busca as imagens da plataforma à origem (images-from) sem as
// aplicar -- o up também as busca, antes de arrancar.
func cmdPull(args []string) error {
	s, _, err := loadSettings()
	if err != nil {
		return err
	}
	if err := pullImages(s); err != nil {
		return err
	}
	fmt.Println("Imagens atualizadas. Aplica com: basestack up")
	return nil
}

func cmdUninstall(args []string) error {
	_, dir, err := loadSettings()
	if err != nil {
		return err
	}
	fmt.Printf(`Isto apaga a plataforma desta máquina: contas, projetos, segredos,
métricas e logs (volumes basestack_*) e %s.
As apps e bases de dados dos projetos (containers e volumes bs-*) NÃO são
apagadas -- apaga os projetos no portal antes, se as quiseres remover.

Escreve "basestack" para confirmar: `, dir)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != "basestack" {
		return errors.New("cancelado")
	}
	if err := compose(dir, "down", "--volumes", "--remove-orphans").Run(); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Println("Plataforma removida.")
	return nil
}

func parseOnOff(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "on", "true", "sim", "1":
		return true, nil
	case "off", "false", "nao", "não", "0":
		return false, nil
	}
	return false, fmt.Errorf("valor inválido %q -- on ou off", v)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstNonZero(v ...int) int {
	for _, n := range v {
		if n != 0 {
			return n
		}
	}
	return 0
}

// cmdSet muda uma escolha do init sem mexer nos segredos.
func cmdSet(args []string) error {
	if len(args) != 2 {
		return errors.New("uso: basestack set <domain|portal-port|admin-email|acme-email|images-from|cluster> <valor>")
	}
	s, dir, err := loadSettings()
	if err != nil {
		return err
	}
	key, value := args[0], strings.TrimSpace(args[1])
	switch key {
	case "domain":
		if value == "" || strings.ContainsAny(value, "/: ") {
			return fmt.Errorf("domínio inválido: %q (só o nome, ex: 192.168.1.50.nip.io)", value)
		}
		if privateDomain(value) && s.ACMEEmail != "" {
			return fmt.Errorf("%w\n  primeiro: basestack set acme-email \"\"", errACMEPrivate)
		}
		s.Domain = value
	case "portal-port":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 || n > 65535 {
			return fmt.Errorf("porta inválida: %q", value)
		}
		s.PortalPort = n
	case "admin-email":
		if _, err := mail.ParseAddress(value); err != nil {
			return fmt.Errorf("email inválido: %q", value)
		}
		s.AdminEmail = value
	case "acme-email":
		if value != "" {
			if _, err := mail.ParseAddress(value); err != nil {
				return fmt.Errorf("email inválido: %q", value)
			}
			if privateDomain(s.Domain) {
				return errACMEPrivate
			}
		}
		s.ACMEEmail = value
	case "cluster":
		on, err := parseOnOff(value)
		if err != nil {
			return err
		}
		if on && runtime.GOOS == "darwin" {
			return errors.New("o modo cluster é para servidores Linux: o Docker Desktop corre numa VM que as outras máquinas não alcançam")
		}
		s.Cluster = on
	case "images-from":
		if strings.Contains(value, "://") || strings.ContainsAny(value, " :") {
			return fmt.Errorf("origem inválida: %q (ex: registry.192.168.1.67.nip.io/basestack; vazio = construir localmente)", value)
		}
		s.ImageSource = strings.TrimRight(value, "/")
	default:
		return fmt.Errorf("chave desconhecida %q -- domain, portal-port, admin-email, acme-email, images-from ou cluster", key)
	}
	if err := saveSettings(dir, s); err != nil {
		return err
	}
	fmt.Printf("%s = %s. Aplica com: basestack up\n", key, value)
	if key == "domain" {
		fmt.Println("Os endereços dos serviços dos projetos passam a usar este domínio, e o bs login precisa do novo URL da API.")
	}
	return nil
}
