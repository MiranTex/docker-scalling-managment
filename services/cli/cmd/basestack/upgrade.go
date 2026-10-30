package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
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
	if newVersion == version && !*force {
		fmt.Printf("Já está na versão %s.\n", version)
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if err := replaceBinary(filepath.Join(tmp, "basestack"), exe); err != nil {
		return err
	}
	fmt.Printf("basestack: %s -> %s (%s)\n", version, newVersion, exe)
	// O bs, se estiver ao lado.
	if bsPath := filepath.Join(filepath.Dir(exe), "bs"); fileExists(bsPath) {
		if err := replaceBinary(filepath.Join(tmp, "bs"), bsPath); err != nil {
			return err
		}
		fmt.Printf("bs:        %s\n", bsPath)
	}

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
	if err != nil || u.Scheme != "https" || !strings.HasPrefix(u.Host, "api.") {
		return errors.New("o URL é o da API da plataforma: https://api.<domínio>")
	}
	registry := "registry." + strings.TrimPrefix(u.Hostname(), "api.")

	var caPEM []byte
	if *want != "" {
		if caPEM, err = fetchCA(apiURL, *want); err != nil {
			return err
		}
	} else {
		raw, hash, err := fetchCAUnverified(apiURL)
		if err != nil {
			return err
		}
		fmt.Printf("A CA de %s tem a impressão digital:\n    %s\nConfere-a na máquina da plataforma (Nós > Adicionar nó mostra-a em --ca).\n", apiURL, hash)
		if !isTerminal(os.Stdin) {
			return errors.New("sem terminal para confirmar: repete com --ca " + hash)
		}
		fmt.Print("Confiar nela? [s/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "s" && a != "sim" && a != "y" && a != "yes" {
			return errors.New("cancelado")
		}
		caPEM = raw
	}

	switch runtime.GOOS {
	case "darwin":
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		path := filepath.Join(h, ".docker", "certs.d", registry, "ca.crt")
		if err := writeFile(path, caPEM, 0o644); err != nil {
			return err
		}
		fmt.Printf("CA instalada em %s.\n  Reinicia o Docker Desktop uma vez para ele confiar em %s.\n", path, registry)
	case "linux":
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
		if err := trustOnLinux(f.Name(), registry); err != nil {
			return err
		}
	default:
		return fmt.Errorf("instala à mão a CA de %s no docker desta máquina", registry)
	}
	fmt.Printf("A seguir: docker login %s (o teu email e uma API key dessa plataforma).\n", registry)
	return nil
}

// fetchCAUnverified busca a CA sem a conferir (para mostrar a impressão
// digital a quem a vai confirmar).
func fetchCAUnverified(apiURL string) ([]byte, string, error) {
	insecure := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := insecure.Get(apiURL + "/v1/agent/ca")
	if err != nil {
		return nil, "", fmt.Errorf("ligando a %s: %w", apiURL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	b, _ := pem.Decode(raw)
	if resp.StatusCode != http.StatusOK || b == nil {
		return nil, "", fmt.Errorf("%s não devolveu a CA da plataforma (%d)", apiURL, resp.StatusCode)
	}
	sum := sha256.Sum256(b.Bytes)
	return raw, "sha256:" + hex.EncodeToString(sum[:]), nil
}
