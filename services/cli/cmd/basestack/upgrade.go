package main

import (
	"bs/internal/trust"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// O basestack chega às outras máquinas pela imagem basestack/cli no
// registry (uma por arquitetura, com o basestack e o bs lá dentro): o
// upgrade busca-a, tira o binário e substitui-se. Num nó, o registry é o
// da plataforma dele; na máquina da plataforma, o do images-from.

func cmdVersion(args []string) error {
	if len(args) > 0 && args[0] == "--short" {
		fmt.Println(version)
		return nil
	}
	fmt.Printf("basestack %s\n", version)
	if s, _, err := loadSettings(); err == nil {
		fmt.Println("\nImagens da plataforma nesta máquina:")
		for _, img := range platformImages {
			v := imageVersion(s.image(img.name))
			if v == "" {
				v = "?"
				if !imageExists(s.image(img.name)) {
					v = "não existe"
				}
			}
			fmt.Printf("  %-18s %s\n", img.name, v)
		}
	}
	if _, _, err := loadNode(); err == nil {
		img, _ := dockerOutput("inspect", "--format", "{{.Config.Image}}", agentContainer)
		v := "?"
		if img != "" {
			v = firstNonEmpty(imageVersion(img), "?")
		}
		fmt.Printf("\nAgente deste nó: %s\n", v)
	}
	return nil
}

// upgradeSource: de onde vem a imagem do cli e com que credencial (vazio =
// o docker login de quem corre).
type upgradeSource struct {
	image      string
	user, pass string
	node       bool
}

func findUpgradeSource(from string) (upgradeSource, error) {
	if n, _, err := loadNode(); err == nil {
		tag := "dev"
		if i := strings.LastIndex(n.AgentImage, ":"); i > 0 {
			tag = n.AgentImage[i+1:]
		}
		prefix := firstNonEmpty(from, n.Registry+"/basestack")
		return upgradeSource{image: prefix + "/cli:" + tag, user: "node-" + n.Node, pass: n.NodeToken, node: true}, nil
	}
	s, _, err := loadSettings()
	if errors.Is(err, errNotInstalled) {
		if from == "" {
			return upgradeSource{}, errors.New("esta máquina não tem a plataforma nem é um nó: diz de onde, com --from <registry>/basestack")
		}
		return upgradeSource{image: from + "/cli:dev"}, nil
	}
	if err != nil {
		return upgradeSource{}, err
	}
	prefix := firstNonEmpty(from, s.ImageSource)
	if prefix == "" {
		return upgradeSource{}, errors.New("sem origem: esta plataforma constrói as imagens aqui (basestack dev build + make install-basestack), ou usa --from <registry>/basestack")
	}
	src := upgradeSource{image: prefix + "/cli:" + s.Version}
	// O registry da própria plataforma: a credencial interna.
	if strings.SplitN(prefix, "/", 2)[0] == s.RegistryHost() {
		src.user, src.pass = "basestack-internal", s.Secrets.RegistryPullPassword
	}
	return src, nil
}

func cmdUpgrade(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	from := fs.String("from", "", "registry de onde buscar (ex: registry.192.168.1.100.nip.io/basestack); por defeito o desta instalação")
	force := fs.Bool("force", false, "substituir mesmo que a versão seja a mesma")
	if err := fs.Parse(args); err != nil {
		return err
	}
	src, err := findUpgradeSource(strings.TrimRight(*from, "/"))
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "basestack-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// Buscar a imagem (com a credencial, numa configuração temporária).
	docker := func(args ...string) *exec.Cmd {
		cmd := exec.Command("docker", args...)
		if src.user != "" {
			cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+filepath.Join(tmp, "docker"))
		}
		return cmd
	}
	if src.user != "" {
		login := docker("login", strings.SplitN(src.image, "/", 2)[0], "--username", src.user, "--password-stdin")
		login.Stdin = strings.NewReader(src.pass)
		if out, err := login.CombinedOutput(); err != nil {
			return fmt.Errorf("login no registry: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	fmt.Printf("==> %s\n", src.image)
	if out, err := docker("pull", "--quiet", src.image).CombinedOutput(); err != nil {
		return fmt.Errorf("buscando %s: %w: %s", src.image, err, strings.TrimSpace(string(out)))
	}
	id, err := dockerOutput("create", src.image)
	if err != nil {
		return err
	}
	defer func() { _, _ = dockerOutput("rm", id) }()
	for _, b := range []string{"basestack", "bs"} {
		if _, err := dockerOutput("cp", id+":/usr/local/bin/"+b, filepath.Join(tmp, b)); err != nil {
			return err
		}
	}
	out, err := exec.Command(filepath.Join(tmp, "basestack"), "version", "--short").Output()
	if err != nil {
		return fmt.Errorf("o binário novo não corre nesta máquina: %w", err)
	}
	newVersion := strings.TrimSpace(string(out))
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if newVersion == version && !*force && fileExists(filepath.Join(filepath.Dir(exe), "bs")) {
		fmt.Printf("Já está na versão %s.\n", version)
		return nil
	}
	if err := replaceBinary(filepath.Join(tmp, "basestack"), exe); err != nil {
		return err
	}
	fmt.Printf("basestack: %s -> %s (%s)\n", version, newVersion, exe)
	// O bs fica ao lado do basestack -- instalado se ainda não existia
	// (vem na mesma imagem e não há outra forma de o ter).
	bsPath := filepath.Join(filepath.Dir(exe), "bs")
	verb := "atualizado"
	if !fileExists(bsPath) {
		verb = "instalado"
	}
	if err := replaceBinary(filepath.Join(tmp, "bs"), bsPath); err != nil {
		return err
	}
	fmt.Printf("bs:        %s (%s)\n", bsPath, verb)

	if src.node {
		// O binário novo recria o agente com a imagem atual.
		cmd := exec.Command(exe, "node", "start")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	fmt.Println("Aplica as imagens novas da plataforma com: basestack up")
	return nil
}

// replaceBinary troca dst por src de forma atómica (cópia ao lado +
// rename): o processo que corre continua com o antigo até terminar.
func replaceBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	next := dst + ".new"
	out, err := os.OpenFile(next, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("sem permissão para escrever em %s -- corre com sudo", filepath.Dir(dst))
		}
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(next, dst)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// cmdTrust: o docker desta máquina passa a confiar no registry de outra
// plataforma -- ex: o Mac de desenvolvimento a enviar imagens para o
// registry central. A CA confere-se pela impressão digital (--ca, ou
// mostrada para confirmar).
func cmdTrust(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("uso: basestack trust https://api.<domínio> [--ca sha256:...]")
	}
	apiURL := strings.TrimRight(args[0], "/")
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	want := fs.String("ca", "", "impressão digital esperada (sha256:...; a do comando de Adicionar nó)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	u, err := url.Parse(apiURL)
	if err != nil || !strings.HasPrefix(u.Host, "api.") {
		return errors.New("o URL é o da API da plataforma: https://api.<domínio>")
	}
	registry := "registry." + strings.TrimPrefix(u.Hostname(), "api.")

	var caPEM []byte
	if *want != "" {
		if caPEM, err = trust.FetchVerified(apiURL, *want); err != nil {
			return err
		}
	} else {
		raw, hash, err := trust.Fetch(apiURL)
		if err != nil {
			return err
		}
		fmt.Printf("A CA de %s tem a impressão digital:\n    %s\nConfere-a na máquina da plataforma (Nós > Adicionar nó mostra-a em --ca).\n", apiURL, hash)
		if !trust.IsTerminal(os.Stdin) {
			return errors.New("sem terminal para confirmar: repete com --ca " + hash)
		}
		if !trust.Confirm("Confiar nela?") {
			return errors.New("cancelado")
		}
		caPEM = raw
	}
	if err := trust.Install(caPEM, registry); err != nil {
		return err
	}
	fmt.Printf("A seguir: bs login %s (ou docker login %s)\n", apiURL, registry)
	return nil
}
