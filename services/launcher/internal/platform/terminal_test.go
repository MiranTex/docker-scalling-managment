package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"launcher/internal/dockerclient"
)

// echoConn devolve o que se escreve -- uma "shell" que só faz eco.
type echoConn struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func newEchoConn() *echoConn {
	r, w := io.Pipe()
	return &echoConn{r: r, w: w}
}

func (c *echoConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *echoConn) Write(b []byte) (int, error) { return c.w.Write(b) }
func (c *echoConn) Close() error                { c.w.Close(); return c.r.Close() }

func (f *fakeDocker) CreateExec(_ context.Context, containerID string, _ dockerclient.ExecConfig) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(containerID); !ok {
		return "", fmt.Errorf("status 404: no such container %s", containerID)
	}
	if f.execs == nil {
		f.execs = map[string]string{}
	}
	id := fmt.Sprintf("exec-%d", len(f.execs)+1)
	f.execs[id] = containerID
	return id, nil
}

func (f *fakeDocker) StartExec(context.Context, string, bool) (io.ReadWriteCloser, error) {
	return newEchoConn(), nil
}

func (f *fakeDocker) StartExecDetached(context.Context, string) error { return nil }

func (f *fakeDocker) ResizeExec(context.Context, string, int, int) error { return nil }

func (f *fakeDocker) InspectExec(_ context.Context, id string) (*dockerclient.ExecInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noShell[f.execs[id]] {
		return &dockerclient.ExecInspect{ExitCode: 127}, nil
	}
	return &dockerclient.ExecInspect{}, nil
}

// addReplica põe uma réplica do serviço no Docker falso, com as labels de
// projectID/envID (as do ambiente, a não ser que o teste as falsifique).
func (f *fakeDocker) addReplica(name, serviceID, projectID, envID, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers["cid-"+name] = dockerclient.Container{ID: "cid-" + name, Names: []string{"/" + name}, State: state, Labels: map[string]string{
		LabelService: serviceID, LabelProjectID: projectID, LabelEnvironmentID: envID, LabelRole: RoleApp,
	}}
}

func setupTerminalService(t *testing.T) (*Service, *fakeDocker, string, string, string) {
	t.Helper()
	ctx := context.Background()
	svc, docker := newTestService(t)
	setupEnv(t, svc)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	p, e, _ := svc.Scope(ctx, "loja", "prod")
	view, err := svc.GetService(ctx, "loja", "prod", "web")
	if err != nil {
		t.Fatal(err)
	}
	return svc, docker, p.ID, e.ID, view.ID
}

func TestTerminalTargetOnlyInItsEnvironment(t *testing.T) {
	ctx := context.Background()
	svc, docker, projectID, envID, serviceID := setupTerminalService(t)

	if _, err := svc.ServiceTerminalTarget(ctx, "loja", "prod", "web", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sem réplicas: err = %v, queria ErrNotFound", err)
	}
	// Uma réplica parada e um container com a label do serviço mas de
	// outro projeto não servem.
	docker.addReplica("web-parada", serviceID, projectID, envID, "exited")
	docker.addReplica("intruso", serviceID, "outro-projeto", envID, "running")
	if _, err := svc.ServiceTerminalTarget(ctx, "loja", "prod", "web", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("só réplicas que não servem: err = %v", err)
	}
	if _, err := svc.ServiceTerminalTarget(ctx, "loja", "prod", "web", "intruso"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pedir o intruso pelo nome: err = %v", err)
	}

	docker.addReplica("web-1", serviceID, projectID, envID, "running")
	target, err := svc.ServiceTerminalTarget(ctx, "loja", "prod", "web", "")
	if err != nil || target.Container != "web-1" || target.ContainerID != "cid-web-1" || target.Kind != "service" {
		t.Fatalf("alvo = %+v, err = %v", target, err)
	}
}

func TestCheckShell(t *testing.T) {
	ctx := context.Background()
	svc, docker, projectID, envID, serviceID := setupTerminalService(t)
	docker.addReplica("web-1", serviceID, projectID, envID, "running")
	target, err := svc.ServiceTerminalTarget(ctx, "loja", "prod", "web", "web-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckShell(ctx, target); err != nil {
		t.Errorf("com shell: err = %v", err)
	}
	docker.noShell = map[string]bool{"cid-web-1": true}
	if err := svc.CheckShell(ctx, target); !errors.Is(err, ErrNoShell) {
		t.Errorf("sem shell: err = %v, queria ErrNoShell", err)
	}
}

func TestDebugTerminalSidecarLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, docker, projectID, envID, serviceID := setupTerminalService(t)
	docker.addReplica("web-1", serviceID, projectID, envID, "running")
	target, err := svc.ServiceTerminalTarget(ctx, "loja", "prod", "web", "")
	if err != nil {
		t.Fatal(err)
	}

	term, err := svc.OpenTerminal(ctx, target, true)
	if err != nil {
		t.Fatal(err)
	}
	debug := docker.byRole(RoleDebug)
	if len(debug) != 1 || debug[0].State != "running" {
		t.Fatalf("containers de debug = %+v", debug)
	}
	req := docker.created[containerName(debug[0])]
	if req.HostConfig.NetworkMode != "container:cid-web-1" || req.HostConfig.PidMode != "container:cid-web-1" {
		t.Errorf("o debug não partilha rede/processos do alvo: %+v", req.HostConfig)
	}
	if req.Image != defaultDebugImage || req.Labels[LabelProjectID] != projectID || req.HostConfig.PidsLimit == nil {
		t.Errorf("pedido de criação = %+v", req)
	}
	if docker.execs["exec-1"] != debug[0].ID {
		t.Errorf("a shell abriu em %q, não no container de debug", docker.execs["exec-1"])
	}

	go func() { _, _ = term.Write([]byte("ls\n")) }()
	buf := make([]byte, 16)
	if n, _ := term.Read(buf); string(buf[:n]) != "ls\n" {
		t.Errorf("leu %q", buf[:n])
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if left := docker.byRole(RoleDebug); len(left) != 0 {
		t.Errorf("o container de debug ficou depois de fechar: %+v", left)
	}

	// Um que ficou para trás (launcher reiniciado a meio) sai no arranque.
	if _, err := svc.OpenTerminal(ctx, target, true); err != nil {
		t.Fatal(err)
	}
	svc.startupCleanup(ctx)
	if left := docker.byRole(RoleDebug); len(left) != 0 {
		t.Errorf("startupCleanup não removeu o debug órfão: %+v", left)
	}
}
