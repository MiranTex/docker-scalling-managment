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
	drivers    map[string]string            // rede -> driver
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
	// blockWait: WaitContainer só volta quando o contexto acabar.
	blockWait bool
	// failStartState: estado em que fica um container depois de arrancar
	// (ex: "exited", para simular um processo que termina logo).
	failStartState string
	// noShell: containers sem /bin/sh (o exec de teste sai com 127).
	noShell map[string]bool
	// execs: exec id -> container onde foi criado.
	execs map[string]string
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

// find aceita id ou nome, como a API do Docker. Chamar com f.mu já tomado.
func (f *fakeDocker) find(idOrName string) (string, bool) {
	if _, ok := f.containers[idOrName]; ok {
		return idOrName, true
	}
	for id, c := range f.containers {
		if len(c.Names) > 0 && c.Names[0] == "/"+idOrName {
			return id, true
		}
	}
	return "", false
}

// byName devolve o container com este nome (para os testes).
func (f *fakeDocker) byName(name string) (dockerclient.Container, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.find(name)
	return f.containers[id], ok
}

// setState muda o estado de um container (ex: simular um "docker stop").
func (f *fakeDocker) setState(name, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.find(name); ok {
		c := f.containers[id]
		c.State = state
		f.containers[id] = c
	}
}

func (f *fakeDocker) CreateContainer(_ context.Context, name string, req dockerclient.CreateContainerRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(name); ok {
		return "", fmt.Errorf("status 409: name %s in use", name)
	}
	if f.failCreateImage != "" && req.Image == f.failCreateImage {
		return "", fmt.Errorf("status 500: falha simulada")
	}
	f.created[name] = req
	id := "cid-" + name
	f.containers[id] = dockerclient.Container{ID: id, Names: []string{"/" + name}, Image: req.Image, State: "created", Labels: req.Labels, Created: time.Now().UnixNano()}
	if req.Labels[LabelRole] == RoleJob {
		f.lastJobCmd = req.Cmd
	}
	return id, nil
}

func (f *fakeDocker) StartContainer(_ context.Context, idOrName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.find(idOrName)
	if !ok {
		return fmt.Errorf("status 404: no such container %s", idOrName)
	}
	c := f.containers[id]
	c.State = "running"
	if f.failStartState != "" && c.Labels[LabelRole] == RoleApp {
		c.State = f.failStartState
	}
	f.containers[id] = c
	return nil
}

func (f *fakeDocker) InspectContainer(_ context.Context, idOrName string) (*dockerclient.ContainerInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.find(idOrName)
	if !ok {
		return nil, fmt.Errorf("status 404: no such container %s", idOrName)
	}
	c := f.containers[id]
	var info dockerclient.ContainerInspect
	info.ID = c.ID
	info.Name = c.Names[0]
	info.Config.Image = c.Image
	info.Config.Labels = c.Labels
	info.State.Status = c.State
	info.State.Running = c.State == "running"
	info.State.StartedAt = time.Now().Add(-time.Minute)
	return &info, nil
}

// WaitContainer devolve logo, a não ser que blockWait esteja ligado: aí
// só volta quando o contexto acabar (um job que nunca termina sozinho).
func (f *fakeDocker) WaitContainer(ctx context.Context, _ string) (int, error) {
	if f.blockWait {
		<-ctx.Done()
		return -1, ctx.Err()
	}
	return f.exitCode, nil
}

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

// VolumesWithUsage devolve os volumes com um tamanho fixo de 1 MiB.
func (f *fakeDocker) VolumesWithUsage(context.Context) ([]dockerclient.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []dockerclient.Volume
	for name, labels := range f.volumes {
		v := dockerclient.Volume{Name: name, Labels: labels, Driver: "local"}
		v.UsageData = &struct {
			Size     int64 `json:"Size"`
			RefCount int64 `json:"RefCount"`
		}{Size: 1 << 20}
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeDocker) InspectImage(context.Context, string) (*dockerclient.ImageInspect, error) {
	return &dockerclient.ImageInspect{ID: "sha256:x"}, nil
}

func (f *fakeDocker) PullImage(context.Context, string) error { return nil }

// Info: a máquina do nó local nos testes.
func (f *fakeDocker) Info(context.Context) (*dockerclient.SystemInfo, error) {
	return &dockerclient.SystemInfo{Name: "teste", NCPU: 4, MemTotal: 16 << 30, Architecture: "x86_64", OSType: "linux", ServerVersion: "29.0.0"}, nil
}

// CreateNetworkWith guarda o driver pedido (bridge ou overlay).
func (f *fakeDocker) CreateNetworkWith(ctx context.Context, name string, o dockerclient.NetworkOptions) (string, error) {
	id, err := f.CreateLabeledNetwork(ctx, name, o.Labels)
	if err == nil {
		f.mu.Lock()
		if f.drivers == nil {
			f.drivers = map[string]string{}
		}
		f.drivers[name] = o.Driver
		f.mu.Unlock()
	}
	return id, err
}

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
	for _, c := range f.connected[network] {
		if c == container {
			return fmt.Errorf("endpoint with name %s already exists in network %s", container, network)
		}
	}
	f.connected[network] = append(f.connected[network], container)
	return nil
}

func (f *fakeDocker) DisconnectNetwork(_ context.Context, network, container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep []string
	for _, c := range f.connected[network] {
		if c != container {
			keep = append(keep, c)
		}
	}
	f.connected[network] = keep
	return nil
}

// InspectNetwork: membros = containers da plataforma ligados com
// ConnectNetwork + containers criados com esta rede como NetworkMode.
func (f *fakeDocker) InspectNetwork(_ context.Context, name string) (*dockerclient.NetworkDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	labels, ok := f.networks[name]
	if !ok {
		return nil, fmt.Errorf("status 404: network %s not found", name)
	}
	d := &dockerclient.NetworkDetail{Network: dockerclient.Network{Name: name, Labels: labels}, Subnet: "172.30.0.0/16", Gateway: "172.30.0.1"}
	for _, c := range f.connected[name] {
		d.Containers = append(d.Containers, dockerclient.NetworkContainer{ContainerID: "id-" + c, Name: c})
	}
	for id, c := range f.containers {
		if req, ok := f.created[strings.TrimPrefix(c.Names[0], "/")]; ok && req.HostConfig != nil && req.HostConfig.NetworkMode == name {
			d.Containers = append(d.Containers, dockerclient.NetworkContainer{ContainerID: id, Name: strings.TrimPrefix(c.Names[0], "/")})
		}
	}
	return d, nil
}

func (f *fakeDocker) StopContainer(context.Context, string, int) error { return nil }

func (f *fakeDocker) RemoveContainer(_ context.Context, idOrName string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.find(idOrName)
	if !ok {
		return fmt.Errorf("status 404: no such container %s", idOrName)
	}
	delete(f.containers, id)
	f.removed = append(f.removed, idOrName)
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
