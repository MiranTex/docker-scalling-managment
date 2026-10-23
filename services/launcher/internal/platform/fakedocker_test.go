package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		networks:   map[string]map[string]string{},
		connected:  map[string][]string{},
		containers: map[string]dockerclient.Container{},
	}
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
	delete(f.containers, id)
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeDocker) ListContainers(_ context.Context, _ bool, filters map[string][]string) ([]dockerclient.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []dockerclient.Container
	for _, c := range f.containers {
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
