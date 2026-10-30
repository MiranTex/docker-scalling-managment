package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// Cluster (Config.Cluster): a máquina da plataforma é manager de um swarm
// e cada nó remoto entra como worker -- só para as redes overlay, que
// levam a rede de cada ambiente a todos os nós (cifrada entre eles). Quem
// decide onde corre cada container continua a ser o escalonador.
//
// Sem cluster (uma máquina, ex: o Mac de desenvolvimento) as redes são
// bridge e os nós remotos não recebem apps.

type swarmState struct {
	mu          sync.Mutex
	clusterID   string
	manager     string // ip:2377
	workerToken string
	err         error
}

func (s *Service) clusterOn() bool { return s.cfg.Cluster }

// ensureSwarm inicia o swarm nesta máquina (se ainda não houver) e guarda o
// que os nós precisam para entrar.
func (s *Service) ensureSwarm(ctx context.Context) error {
	info, err := s.local.Info(ctx)
	if err != nil {
		return err
	}
	if info.Swarm.LocalNodeState == "inactive" {
		if s.cfg.AdvertiseAddr == "" {
			return errors.New("modo cluster sem LAUNCHER_ADVERTISE_ADDR (o IP desta máquina na rede)")
		}
		s.logger.Info("a iniciar o swarm (redes overlay)", "advertise", s.cfg.AdvertiseAddr)
		if err := s.local.SwarmInit(ctx, s.cfg.AdvertiseAddr); err != nil {
			return fmt.Errorf("iniciando o swarm: %w", err)
		}
		if info, err = s.local.Info(ctx); err != nil {
			return err
		}
	}
	if !info.Swarm.ControlAvailable || info.Swarm.Cluster == nil {
		return fmt.Errorf("esta máquina está num swarm (%s) mas não é manager dele -- sai dele (docker swarm leave) para a plataforma criar o seu", info.Swarm.LocalNodeState)
	}
	token, err := s.local.SwarmWorkerToken(ctx)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(info.Swarm.NodeAddr)
	if err != nil {
		host = info.Swarm.NodeAddr
	}
	s.swarm.mu.Lock()
	s.swarm.clusterID, s.swarm.manager, s.swarm.workerToken, s.swarm.err = info.Swarm.Cluster.ID, net.JoinHostPort(host, "2377"), token, nil
	s.swarm.mu.Unlock()
	return nil
}

// joinSwarm põe o Docker de um nó remoto no swarm da plataforma (pelo
// túnel). Devolve o id do nó no swarm.
func (s *Service) joinSwarm(ctx context.Context, node string, d Docker, address string) (string, error) {
	s.swarm.mu.Lock()
	cluster, manager, token := s.swarm.clusterID, s.swarm.manager, s.swarm.workerToken
	s.swarm.mu.Unlock()
	if cluster == "" {
		return "", errors.New("o swarm da plataforma ainda não está pronto")
	}
	info, err := d.Info(ctx)
	if err != nil {
		return "", err
	}
	switch {
	case info.Swarm.LocalNodeState == "active" && info.Swarm.Cluster != nil && info.Swarm.Cluster.ID == cluster:
		return info.Swarm.NodeID, nil
	case info.Swarm.LocalNodeState == "active" && info.Swarm.ControlAvailable:
		return "", errors.New("o Docker deste nó é manager de outro swarm -- não o tiro de lá sozinho")
	case info.Swarm.LocalNodeState != "inactive":
		// Worker de outro swarm (ou de um antigo desta plataforma): sai.
		if err := d.SwarmLeave(ctx); err != nil {
			return "", fmt.Errorf("saindo do swarm antigo: %w", err)
		}
	}
	if address == "" {
		address = info.Swarm.NodeAddr
	}
	s.logger.Info("nó a entrar no swarm", "node", node, "advertise", address, "manager", manager)
	if err := d.SwarmJoin(ctx, address, manager, token); err != nil {
		return "", fmt.Errorf("entrando no swarm da plataforma (%s): %w -- as portas 2377/tcp, 7946/tcp+udp e 4789/udp estão abertas entre as máquinas?", manager, err)
	}
	if info, err = d.Info(ctx); err != nil {
		return "", err
	}
	return info.Swarm.NodeID, nil
}

// leaveSwarm tira um nó do swarm: ele sai (se ainda estiver ligado) e o
// manager esquece-o.
func (s *Service) leaveSwarm(ctx context.Context, c *agentConn) {
	if c == nil || c.swarmNodeID == "" {
		return
	}
	if err := c.docker.SwarmLeave(ctx); err != nil {
		s.logger.Warn("o nó não saiu do swarm", "err", err)
	}
	if err := s.local.RemoveSwarmNode(ctx, c.swarmNodeID); err != nil {
		s.logger.Warn("removendo o nó do swarm", "err", err)
	}
}

// envNetworkOptions: overlay cifrada num cluster, bridge numa só máquina.
func (s *Service) envNetworkOptions(labels map[string]string) dockerclient.NetworkOptions {
	if !s.clusterOn() {
		return dockerclient.NetworkOptions{Driver: "bridge", Labels: labels}
	}
	return dockerclient.NetworkOptions{Driver: "overlay", Attachable: true, Options: map[string]string{"encrypted": "true"}, Labels: labels}
}

// ensureEnvNetworks garante a rede de cada ambiente com o tipo certo:
// cria a que falte e passa as que estão com o tipo errado (bridge ->
// overlay ao ligar o cluster, e o inverso). Os containers do ambiente
// voltam a entrar com os seus aliases -- um corte curto, sem reiniciar
// nada. Corre no arranque.
func (s *Service) ensureEnvNetworks(ctx context.Context) {
	envs, err := s.db.ListAllEnvironments(ctx)
	if err != nil {
		s.logger.Error("listando ambientes para verificar as redes", "err", err)
		return
	}
	want := s.envNetworkOptions(nil).Driver
	for _, e := range envs {
		p, err := s.db.GetProject(ctx, e.ProjectID)
		if err != nil {
			continue
		}
		detail, err := s.local.InspectNetwork(ctx, e.Network)
		switch {
		case err != nil && isNotFound(err):
			s.logger.Warn("a rede do ambiente não existe -- a recriar", "network", e.Network)
			err = s.recreateNetwork(ctx, p, e, nil)
		case err != nil:
			continue
		case detail.Driver != want:
			s.logger.Info("a migrar a rede do ambiente", "network", e.Network, "from", detail.Driver, "to", want)
			err = s.recreateNetwork(ctx, p, e, detail)
		}
		if err != nil {
			s.logger.Error("rede do ambiente", "network", e.Network, "err", err)
		}
	}
}

// recreateNetwork troca a rede de um ambiente por uma nova do tipo certo.
func (s *Service) recreateNetwork(ctx context.Context, p store.Project, e store.Environment, old *dockerclient.NetworkDetail) error {
	if old != nil {
		for _, c := range old.Containers {
			// O balanceador interno de uma overlay ("lb-<rede>") não é um
			// container: sai com a rede.
			if strings.HasPrefix(c.ContainerID, "lb-") {
				continue
			}
			if err := s.local.DisconnectNetwork(ctx, e.Network, c.ContainerID); err != nil && !isNotFound(err) {
				return fmt.Errorf("desligando %s: %w", c.Name, err)
			}
		}
		if err := s.local.RemoveNetwork(ctx, e.Network); err != nil && !isNotFound(err) {
			return fmt.Errorf("removendo a rede antiga: %w", err)
		}
	}
	// Num swarm, a remoção de uma overlay demora a propagar: criar logo
	// outra com o mesmo nome pode falhar por instantes.
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if _, err = s.local.CreateNetworkWith(ctx, e.Network, s.envNetworkOptions(environmentLabels(p, e))); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if err != nil {
		return fmt.Errorf("criando a rede nova: %w", err)
	}
	// As apps e os addons do ambiente (nesta máquina) voltam a entrar,
	// com o slug como alias; a plataforma volta pelo attachPlatform.
	containers, err := s.local.ListContainers(ctx, false, map[string][]string{"label": {LabelEnvironmentID + "=" + e.ID}})
	if err != nil {
		return err
	}
	for _, c := range containers {
		role := c.Labels[LabelRole]
		if role != RoleApp && role != RoleAddon {
			continue
		}
		if err := s.local.ConnectNetworkAliases(ctx, e.Network, c.ID, []string{c.Labels[LabelServiceSlug]}); err != nil && !alreadyAttached(err) {
			s.logger.Error("voltando a ligar à rede", "network", e.Network, "container", containerName(c), "err", err)
		}
	}
	s.attachPlatform(ctx, e.Network)
	return nil
}
