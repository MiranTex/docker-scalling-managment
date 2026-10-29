package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Imagens da plataforma fora do "docker build" local: enviar para o
// registry e, numa máquina sem o código, buscá-las de um registry
// (Settings.ImageSource).

// withRegistryLogin corre fn com o docker autenticado no registry desta
// instalação pela credencial interna, numa configuração temporária -- a
// tua (~/.docker) não fica com ela.
func withRegistryLogin(s Settings, fn func(docker func(args ...string) error) error) error {
	if s.Secrets.RegistryPullPassword == "" {
		return errors.New("o registry ainda não está configurado -- corre \"basestack up\" primeiro")
	}
	host, err := dockerHost()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "basestack-docker-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	run := func(stdin string, args ...string) error {
		cmd := exec.Command("docker", args...)
		cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+tmp, "DOCKER_HOST="+host)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(stdin), os.Stdout, os.Stderr
		return cmd.Run()
	}
	if err := run(s.Secrets.RegistryPullPassword, "login", s.RegistryHost(), "--username", "basestack-internal", "--password-stdin"); err != nil {
		return fmt.Errorf("login no registry: %w", err)
	}
	return fn(func(args ...string) error { return run("", args...) })
}

// dockerHost é o endpoint do daemon no contexto atual (no Mac, o do
// Docker Desktop): a configuração temporária não tem os contextos.
func dockerHost() (string, error) {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h, nil
	}
	return dockerOutput("context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
}

// remoteImage é o nome de uma imagem da plataforma no registry desta
// instalação.
func remoteImage(s Settings, name string) string {
	return s.RegistryHost() + "/basestack/" + name + ":" + s.Version
}

// pushImages envia as imagens construídas localmente -- com todas as
// arquiteturas com que foram construídas (dev build --platform).
func pushImages(s Settings, only map[string]bool) error {
	return withRegistryLogin(s, func(docker func(args ...string) error) error {
		for _, img := range selected(only) {
			remote := remoteImage(s, img.name)
			fmt.Printf("==> %s\n", remote)
			if err := docker("tag", s.image(img.name), remote); err != nil {
				return err
			}
			if err := docker("push", remote); err != nil {
				return fmt.Errorf("enviando %s: %w", img.name, err)
			}
		}
		return nil
	})
}

// pullImages busca as imagens da plataforma de ImageSource e dá-lhes o
// nome local que o compose usa. Com missingOnly, só as que faltam. Usa o
// login do docker de quem corre (docker login <registry>).
func pullImages(s Settings, missingOnly bool) error {
	if s.ImageSource == "" {
		return errors.New("sem origem das imagens -- basestack set images-from <registry>/basestack, ou constrói-as: basestack dev build")
	}
	for _, img := range platformImages {
		local := s.image(img.name)
		if missingOnly && imageExists(local) {
			continue
		}
		remote := strings.TrimRight(s.ImageSource, "/") + "/" + img.name + ":" + s.Version
		fmt.Printf("==> %s\n", remote)
		cmd := exec.Command("docker", "pull", remote)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("buscando %s (fizeste \"docker login %s\"?): %w", remote, strings.SplitN(s.ImageSource, "/", 2)[0], err)
		}
		if _, err := dockerOutput("tag", remote, local); err != nil {
			return err
		}
	}
	return nil
}

func selected(only map[string]bool) []struct{ name, dir string } {
	var out []struct{ name, dir string }
	for _, img := range platformImages {
		if len(only) == 0 || only[img.name] {
			out = append(out, img)
		}
	}
	return out
}
