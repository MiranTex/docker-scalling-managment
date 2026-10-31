package platform

import (
	"context"
	"io"
	"strings"
	"sync"

	"launcher/internal/dockerclient"
)

// clusterDocker é o Docker que o resto do pacote vê: o local mais o de cada
// nó remoto ligado (pelo túnel do agente). Listar junta todos; uma operação
// sobre um container vai para o nó onde ele está; redes, volumes e imagens
// ficam no local (a máquina da plataforma). Sem nós remotos é exatamente o
// Docker local.
//
// Criar réplicas noutro nó não passa por aqui: o escalonador escolhe o nó
// e usa dockerOn(nó) diretamente.
type clusterDocker struct {
	Docker // o local -- tudo o que não é de um container específico
	agents *agents

	mu    sync.Mutex
	where map[string]string // id ou nome do container (ou id de exec) -> nó
}

func newClusterDocker(local Docker, a *agents) *clusterDocker {
	return &clusterDocker{Docker: local, agents: a, where: map[string]string{}}
}

func (c *clusterDocker) remember(key, node string) {
	c.mu.Lock()
	c.where[strings.TrimPrefix(key, "/")] = node
	c.mu.Unlock()
}

func (c *clusterDocker) forget(key string) {
	c.mu.Lock()
	delete(c.where, key)
	c.mu.Unlock()
}

func (c *clusterDocker) dockerOf(node string) Docker {
	if node != LocalNode {
		if a := c.agents.get(node); a != nil {
			return a.docker
		}
	}
	return c.Docker
}

// locate diz em que Docker está um container: o que se sabe de listagens
// anteriores, ou procurando primeiro no local e depois em cada nó ligado.
func (c *clusterDocker) locate(ctx context.Context, key string) Docker {
	c.mu.Lock()
	node, ok := c.where[key]
	c.mu.Unlock()
	if ok {
		if node == LocalNode || c.agents.get(node) != nil {
			return c.dockerOf(node)
		}
		c.forget(key)
	}
	if _, err := c.Docker.InspectContainer(ctx, key); err == nil {
		c.remember(key, LocalNode)
		return c.Docker
	}
	for _, name := range c.agents.names() {
		d := c.dockerOf(name)
		if _, err := d.InspectContainer(ctx, key); err == nil {
			c.remember(key, name)
			return d
		}
	}
	return c.Docker
}

func (c *clusterDocker) ListContainers(ctx context.Context, all bool, filters map[string][]string) ([]dockerclient.Container, error) {
	out, err := c.Docker.ListContainers(ctx, all, filters)
	if err != nil {
		return nil, err
	}
	for _, name := range c.agents.names() {
		remote, err := c.dockerOf(name).ListContainers(ctx, all, filters)
		if err != nil {
			// Um nó que caiu a meio não impede de ver o resto.
			continue
		}
		for i := range remote {
			if remote[i].Labels == nil {
				remote[i].Labels = map[string]string{}
			}
			if remote[i].Labels[LabelNode] == "" {
				remote[i].Labels[LabelNode] = name
			}
		}
		out = append(out, remote...)
	}
	for _, ct := range out {
		node := nodeOf(ct)
		c.remember(ct.ID, node)
		for _, n := range ct.Names {
			c.remember(n, node)
		}
	}
	return out, nil
}

// CreateContainer: no local, a não ser que partilhe a rede de outro
// container (o debug do terminal) -- aí tem de ir para o nó dele.
func (c *clusterDocker) CreateContainer(ctx context.Context, name string, req dockerclient.CreateContainerRequest) (string, error) {
	d := c.Docker
	node := LocalNode
	if req.HostConfig != nil {
		if target, ok := strings.CutPrefix(req.HostConfig.NetworkMode, "container:"); ok {
			d = c.locate(ctx, target)
			c.mu.Lock()
			if n, ok := c.where[target]; ok {
				node = n
			}
			c.mu.Unlock()
		}
	}
	id, err := d.CreateContainer(ctx, name, req)
	if err == nil {
		c.remember(id, node)
		c.remember(name, node)
	}
	return id, err
}

func (c *clusterDocker) StartContainer(ctx context.Context, id string) error {
	return c.locate(ctx, id).StartContainer(ctx, id)
}

func (c *clusterDocker) StopContainer(ctx context.Context, id string, timeout int) error {
	return c.locate(ctx, id).StopContainer(ctx, id, timeout)
}

func (c *clusterDocker) RemoveContainer(ctx context.Context, id string, force bool) error {
	err := c.locate(ctx, id).RemoveContainer(ctx, id, force)
	if err == nil {
		c.forget(id)
	}
	return err
}

func (c *clusterDocker) InspectContainer(ctx context.Context, id string) (*dockerclient.ContainerInspect, error) {
	return c.locate(ctx, id).InspectContainer(ctx, id)
}

func (c *clusterDocker) WaitContainer(ctx context.Context, id string) (int, error) {
	return c.locate(ctx, id).WaitContainer(ctx, id)
}

func (c *clusterDocker) ContainerLogs(ctx context.Context, id string, tail int) (string, error) {
	return c.locate(ctx, id).ContainerLogs(ctx, id, tail)
}

func (c *clusterDocker) CopyFromContainer(ctx context.Context, id, path string) (io.ReadCloser, error) {
	return c.locate(ctx, id).CopyFromContainer(ctx, id, path)
}

func (c *clusterDocker) ContainerStats(ctx context.Context, id string) (*dockerclient.Stats, error) {
	return c.locate(ctx, id).ContainerStats(ctx, id)
}

func (c *clusterDocker) ConnectNetwork(ctx context.Context, network, id string) error {
	return c.locate(ctx, id).ConnectNetwork(ctx, network, id)
}

func (c *clusterDocker) DisconnectNetwork(ctx context.Context, network, id string) error {
	return c.locate(ctx, id).DisconnectNetwork(ctx, network, id)
}

func (c *clusterDocker) ConnectNetworkAliases(ctx context.Context, network, id string, aliases []string) error {
	return c.locate(ctx, id).ConnectNetworkAliases(ctx, network, id, aliases)
}

// Exec: o id do exec fica associado ao nó do container.
func (c *clusterDocker) CreateExec(ctx context.Context, containerID string, cfg dockerclient.ExecConfig) (string, error) {
	d := c.locate(ctx, containerID)
	id, err := d.CreateExec(ctx, containerID, cfg)
	if err == nil {
		c.mu.Lock()
		c.where["exec:"+id] = c.where[containerID]
		c.mu.Unlock()
	}
	return id, err
}

func (c *clusterDocker) execDocker(id string) Docker {
	c.mu.Lock()
	node := c.where["exec:"+id]
	c.mu.Unlock()
	return c.dockerOf(node)
}

func (c *clusterDocker) StartExec(ctx context.Context, id string, tty bool) (io.ReadWriteCloser, error) {
	return c.execDocker(id).StartExec(ctx, id, tty)
}

func (c *clusterDocker) StartExecDetached(ctx context.Context, id string) error {
	return c.execDocker(id).StartExecDetached(ctx, id)
}

func (c *clusterDocker) ResizeExec(ctx context.Context, id string, rows, cols int) error {
	return c.execDocker(id).ResizeExec(ctx, id, rows, cols)
}

func (c *clusterDocker) InspectExec(ctx context.Context, id string) (*dockerclient.ExecInspect, error) {
	return c.execDocker(id).InspectExec(ctx, id)
}
