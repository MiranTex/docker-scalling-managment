package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"launcher/internal/dockerclient"
)

// fakeDocker guarda redes e containers em memória. Os métodos que um teste
// não precisa vêm da interface embutida (nil) e dão panic se chamados.
type fakeDocker struct {
	Docker

	mu         sync.Mutex
	networks   map[string]map[string]string // nome -> labels
	connected  map[string][]string          // rede -> containers
	containers map[string]dockerclient.Container
	removed    []string

	// created guarda o pedido de criação de cada container, por nome.
	created map[string]dockerclient.CreateContainerRequest
	volumes map[string]map[string]string
	// exitCode/logs: o que WaitContainer/ContainerLogs devolvem a jobs.
	exitCode int
	logs     string
	// lastJobCmd é o cmd do último container de job criado.
	lastJobCmd []string
	// pulls regista cada pull (imagem e utilizador das credenciais).
	pulls []string
	// cpuPercent é o que ContainerStats reporta para todos.
	cpuPercent float64
	// failCreateImage: criar um container desta imagem falha.
	failCreateImage string
}

func (f *fakeDocker) PullImageWithAuth(_ context.Context, ref string, auth *dockerclient.RegistryAuth) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	user := ""
	if auth != nil {
		user = auth.Username + "@" + auth.ServerAddress
	}
	f.pulls = append(f.pulls, ref+" "+user)
	return nil
}

func (f *fakeDocker) ContainerStats(context.Context, string) (*dockerclient.Stats, error) {
	var st dockerclient.Stats
	st.CPUStats.CPUUsage.TotalUsage = uint64(f.cpuPercent * 100)
	st.CPUStats.SystemCPUUsage = 10000
	st.CPUStats.OnlineCPUs = 1
	return &st, nil
}

// byRole conta os containers vivos com esta role.
func (f *fakeDocker) byRole(role string) []dockerclient.Container {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []dockerclient.Container
	for _, c := range f.containers {
		if c.Labels[LabelRole] == role {
			out = append(out, c)
		}
	}
	return out
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		networks:   map[string]map[string]string{},
		connected:  map[string][]string{},
		containers: map[string]dockerclient.Container{},
		created:    map[string]dockerclient.CreateContainerRequest{},
		volumes:    map[string]map[string]string{},
	}
}

func (f *fakeDocker) CreateContainer(_ context.Context, name string, req dockerclient.CreateContainerRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.containers[name]; ok {
		return "", fmt.Errorf("status 409: name %s in use", name)
	}
	if f.failCreateImage != "" && req.Image == f.failCreateImage {
		return "", fmt.Errorf("status 500: falha simulada")
	}
	f.created[name] = req
	f.containers[name] = dockerclient.Container{ID: name, Names: []string{"/" + name}, Image: req.Image, State: "created", Labels: req.Labels}
	if req.Labels[LabelRole] == RoleJob {
		f.lastJobCmd = req.Cmd
	}
	return name, nil
}

func (f *fakeDocker) StartContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok {
		return fmt.Errorf("status 404: no such container %s", id)
	}
	c.State = "running"
	f.containers[id] = c
	return nil
}

func (f *fakeDocker) InspectContainer(_ context.Context, id string) (*dockerclient.ContainerInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok {
		return nil, fmt.Errorf("status 404: no such container %s", id)
	}
	var info dockerclient.ContainerInspect
	info.ID = c.ID
	info.Name = "/" + id
	info.Config.Image = c.Image
	info.Config.Labels = c.Labels
	info.State.Status = c.State
	info.State.Running = c.State == "running"
	info.State.StartedAt = time.Now().Add(-time.Minute)
	return &info, nil
}

func (f *fakeDocker) WaitContainer(context.Context, string) (int, error) { return f.exitCode, nil }

func (f *fakeDocker) ContainerLogs(context.Context, string, int) (string, error) { return f.logs, nil }

func (f *fakeDocker) CreateVolume(_ context.Context, name string, labels map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumes[name] = labels
	return nil
}

func (f *fakeDocker) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.volumes, name)
	return nil
}

func (f *fakeDocker) ListVolumesByLabel(_ context.Context, label string) ([]dockerclient.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, v, _ := strings.Cut(label, "=")
	var out []dockerclient.Volume
	for name, labels := range f.volumes {
		if labels[k] == v {
			out = append(out, dockerclient.Volume{Name: name, Labels: labels})
		}
	}
	return out, nil
}

func (f *fakeDocker) InspectImage(context.Context, string) (*dockerclient.ImageInspect, error) {
	return &dockerclient.ImageInspect{ID: "sha256:x"}, nil
}

func (f *fakeDocker) PullImage(context.Context, string) error { return nil }

func (f *fakeDocker) CreateLabeledNetwork(_ context.Context, name string, labels map[string]string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.networks[name]; ok {
		return "", fmt.Errorf("network with name %s already exists", name)
	}
	f.networks[name] = labels
	return "net-" + name, nil
}

func (f *fakeDocker) RemoveNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.networks, name)
	delete(f.connected, name)
	return nil
}

func (f *fakeDocker) ConnectNetwork(_ context.Context, network, container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected[network] = append(f.connected[network], container)
	return nil
}

func (f *fakeDocker) DisconnectNetwork(_ context.Context, network, container string) error {
	return nil
}

func (f *fakeDocker) StopContainer(context.Context, string, int) error { return nil }

func (f *fakeDocker) RemoveContainer(_ context.Context, id string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.containers[id]; !ok {
		return fmt.Errorf("status 404: no such container %s", id)
	}
	delete(f.containers, id)
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeDocker) ListContainers(_ context.Context, all bool, filters map[string][]string) ([]dockerclient.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []dockerclient.Container
	for _, c := range f.containers {
		if !all && c.State != "running" {
			continue
		}
		match := true
		for _, l := range filters["label"] {
			k, v, hasValue := strings.Cut(l, "=")
			got, ok := c.Labels[k]
			if !ok || (hasValue && got != v) {
				match = false
				break
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out, nil
}
