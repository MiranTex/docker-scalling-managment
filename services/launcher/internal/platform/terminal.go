package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// RoleDebug marca os containers temporários do modo debug do terminal --
// vivem só enquanto a sessão estiver aberta.
const RoleDebug = "debug"

// defaultDebugImage tem sh e as ferramentas básicas (ps, wget, nc, vi...).
const defaultDebugImage = "busybox:1.37"

// ErrNoShell: a imagem não tem /bin/sh (ex: imagens "scratch" ou
// distroless) -- só o modo debug consegue entrar.
var ErrNoShell = errors.New("esta imagem não tem shell (/bin/sh) -- usa o modo debug")

// shellCmd prefere bash quando existe.
var shellCmd = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}

// TerminalTarget é o container onde um terminal vai abrir -- uma réplica
// de um serviço ou o container de um addon, sempre deste ambiente.
type TerminalTarget struct {
	Project     store.Project
	Environment store.Environment
	// Kind é "service" ou "addon"; Slug o do serviço/addon.
	Kind        string
	Slug        string
	Container   string // nome
	ContainerID string
}

// ServiceTerminalTarget escolhe a réplica pedida, ou a primeira a correr.
func (s *Service) ServiceTerminalTarget(ctx context.Context, projectSlug, envSlug, svcSlug, replica string) (TerminalTarget, error) {
	p, e, svc, err := s.serviceScope(ctx, projectSlug, envSlug, svcSlug)
	if err != nil {
		return TerminalTarget{}, err
	}
	containers, err := s.replicasOf(ctx, svc.ID)
	if err != nil {
		return TerminalTarget{}, err
	}
	for _, c := range containers {
		name := containerName(c)
		if c.State != "running" || (replica != "" && name != replica) || !inEnvironment(c.Labels, p, e) {
			continue
		}
		return TerminalTarget{Project: p, Environment: e, Kind: "service", Slug: svc.Slug, Container: name, ContainerID: c.ID}, nil
	}
	if replica != "" {
		return TerminalTarget{}, fmt.Errorf("%w: réplica %q não está a correr", ErrNotFound, replica)
	}
	return TerminalTarget{}, fmt.Errorf("%w: o serviço %q não tem réplicas a correr", ErrNotFound, svcSlug)
}

func (s *Service) AddonTerminalTarget(ctx context.Context, projectSlug, envSlug, addonSlug string) (TerminalTarget, error) {
	p, e, a, err := s.addonScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return TerminalTarget{}, err
	}
	name := addonContainerName(p, e, a)
	info, err := s.docker.InspectContainer(ctx, name)
	if err != nil || !info.State.Running || info.Config.Labels[LabelAddon] != a.ID || !inEnvironment(info.Config.Labels, p, e) {
		return TerminalTarget{}, fmt.Errorf("%w: o addon %q não está a correr", ErrNotFound, addonSlug)
	}
	return TerminalTarget{Project: p, Environment: e, Kind: "addon", Slug: a.Slug, Container: name, ContainerID: info.ID}, nil
}

// inEnvironment confirma pelas labels que o container é deste ambiente --
// um terminal nunca abre num container da plataforma ou de outro projeto.
func inEnvironment(labels map[string]string, p store.Project, e store.Environment) bool {
	return labels[LabelProjectID] == p.ID && labels[LabelEnvironmentID] == e.ID
}

// CheckShell confirma que o container tem /bin/sh, com um exec que só
// termina. Devolve ErrNoShell se não tiver.
func (s *Service) CheckShell(ctx context.Context, t TerminalTarget) error {
	id, err := s.docker.CreateExec(ctx, t.ContainerID, dockerclient.ExecConfig{Cmd: []string{"/bin/sh", "-c", "exit 0"}})
	if err != nil {
		return err
	}
	if err := s.docker.StartExecDetached(ctx, id); err != nil {
		return ErrNoShell
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		info, err := s.docker.InspectExec(ctx, id)
		if err != nil {
			return err
		}
		if !info.Running {
			if info.ExitCode != 0 {
				return ErrNoShell
			}
			return nil
		}
		if time.Now().After(deadline) {
			return nil // a correr ainda: a shell existe
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Terminal é uma sessão aberta: lê-se o output, escreve-se o input.
type Terminal struct {
	io.ReadWriteCloser
	svc     *Service
	execID  string
	debugID string // container de debug a remover no fim
}

// debugBanner avisa ao abrir o modo debug: o env e os ficheiros que se
// veem são os do busybox, não os do serviço.
const debugBanner = `printf '%s\n' \
  'Modo debug: busybox ao lado do serviço (mesma rede e processos; env e ficheiros próprios).' \
  "  env do serviço:       tr '\\0' '\\n' < /proc/1/environ" \
  '  ficheiros do serviço: /proc/1/root/' \
  ''
`

// OpenTerminal abre uma shell com pseudo-terminal no alvo. Em modo debug
// arranca primeiro um container temporário que partilha a rede e os
// processos do alvo, e a shell abre nele -- serve imagens sem shell, e os
// ficheiros do alvo ficam em /proc/1/root.
func (s *Service) OpenTerminal(ctx context.Context, t TerminalTarget, debug bool) (*Terminal, error) {
	term := &Terminal{svc: s}
	target, cmd := t.ContainerID, shellCmd
	if debug {
		id, err := s.startDebugContainer(ctx, t)
		if err != nil {
			return nil, err
		}
		term.debugID, target, cmd = id, id, []string{"sh", "-c", debugBanner + "exec sh"}
	}
	execID, err := s.docker.CreateExec(ctx, target, dockerclient.ExecConfig{Cmd: cmd, Env: []string{"TERM=xterm-256color"}, Tty: true})
	if err == nil {
		term.execID = execID
		term.ReadWriteCloser, err = s.docker.StartExec(ctx, execID, true)
	}
	if err != nil {
		term.removeDebug()
		return nil, fmt.Errorf("abrindo terminal em %s: %w", t.Container, err)
	}
	return term, nil
}

func (s *Service) startDebugContainer(ctx context.Context, t TerminalTarget) (string, error) {
	image := s.cfg.DebugImage
	if image == "" {
		image = defaultDebugImage
	}
	if err := s.ensureImage(ctx, image, t.Project.ID); err != nil {
		return "", err
	}
	labels := environmentLabels(t.Project, t.Environment)
	labels[LabelRole] = RoleDebug
	pids := int64(256)
	id, err := s.docker.CreateContainer(ctx, t.Container+"-debug-"+newID()[:6], dockerclient.CreateContainerRequest{
		Image:  image,
		Cmd:    []string{"sleep", "86400"},
		Labels: labels,
		HostConfig: &dockerclient.CreateHostConfig{
			NetworkMode: "container:" + t.ContainerID,
			PidMode:     "container:" + t.ContainerID,
			SecurityOpt: dockerclient.AppSecurityOpt(),
			Resources:   dockerclient.Resources{NanoCPUs: 250_000_000, Memory: 128 << 20, MemorySwap: 128 << 20, PidsLimit: &pids},
		},
	})
	if err != nil {
		return "", fmt.Errorf("criando container de debug: %w", err)
	}
	if err := s.docker.StartContainer(ctx, id); err != nil {
		_ = s.docker.RemoveContainer(context.WithoutCancel(ctx), id, true)
		return "", fmt.Errorf("arrancando container de debug: %w", err)
	}
	return id, nil
}

// Resize acompanha o tamanho da janela do browser.
func (t *Terminal) Resize(ctx context.Context, rows, cols int) error {
	return t.svc.docker.ResizeExec(ctx, t.execID, rows, cols)
}

// Close fecha a shell e remove o container de debug, se houver.
func (t *Terminal) Close() error {
	err := t.ReadWriteCloser.Close()
	t.removeDebug()
	return err
}

func (t *Terminal) removeDebug() {
	if t.debugID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := t.svc.docker.RemoveContainer(ctx, t.debugID, true); err != nil {
		t.svc.logger.Warn("removendo container de debug", "container", t.debugID, "err", err)
	}
}
