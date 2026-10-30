package dockerclient

import (
	"context"
	"net/url"
)

// Swarm: a plataforma só o usa para as redes overlay (redes de ambiente que
// atravessam os nós) -- quem decide onde corre cada container continua a
// ser o launcher, nunca os serviços do swarm.

// SwarmInit põe este Docker como manager de um swarm novo. advertise é o
// IP pelo qual os outros nós lhe chegam.
func (c *Client) SwarmInit(ctx context.Context, advertise string) error {
	return c.do(ctx, "POST", "/swarm/init", map[string]any{"ListenAddr": "0.0.0.0:2377", "AdvertiseAddr": advertise}, nil)
}

// SwarmWorkerToken é o token com que um nó entra no swarm (só num manager).
func (c *Client) SwarmWorkerToken(ctx context.Context) (string, error) {
	var sw struct {
		JoinTokens struct {
			Worker string `json:"Worker"`
		} `json:"JoinTokens"`
	}
	if err := c.do(ctx, "GET", "/swarm", nil, &sw); err != nil {
		return "", err
	}
	return sw.JoinTokens.Worker, nil
}

// SwarmJoin põe este Docker como worker do swarm do manager.
func (c *Client) SwarmJoin(ctx context.Context, advertise, manager, token string) error {
	return c.do(ctx, "POST", "/swarm/join", map[string]any{
		"ListenAddr": "0.0.0.0:2377", "AdvertiseAddr": advertise, "RemoteAddrs": []string{manager}, "JoinToken": token,
	}, nil)
}

func (c *Client) SwarmLeave(ctx context.Context) error {
	return c.do(ctx, "POST", "/swarm/leave?force=true", nil, nil)
}

// InspectSwarmNode: erro se o manager não conhece o nó (só num manager).
func (c *Client) InspectSwarmNode(ctx context.Context, id string) error {
	return c.do(ctx, "GET", "/nodes/"+url.PathEscape(id), nil, nil)
}

// RemoveSwarmNode tira um nó da lista do manager (depois de ele sair).
func (c *Client) RemoveSwarmNode(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/nodes/"+url.PathEscape(id)+"?force=true", nil, nil)
}

// NetworkOptions: as redes dos ambientes num cluster são overlay,
// "attachable" (containers soltos podem entrar, não só serviços do swarm)
// e cifradas entre nós.
type NetworkOptions struct {
	Driver     string
	Attachable bool
	Options    map[string]string
	Labels     map[string]string
}

func (c *Client) CreateNetworkWith(ctx context.Context, name string, o NetworkOptions) (string, error) {
	all := map[string]string{PlatformLabel: PlatformLabelValue}
	for k, v := range o.Labels {
		all[k] = v
	}
	var resp struct {
		ID string `json:"Id"`
	}
	body := map[string]any{"Name": name, "Labels": all, "Driver": o.Driver, "Attachable": o.Attachable, "Options": o.Options}
	if err := c.do(ctx, "POST", "/networks/create", body, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ConnectNetworkAliases liga um container a uma rede com aliases (os nomes
// por que os outros o encontram).
func (c *Client) ConnectNetworkAliases(ctx context.Context, networkID, containerID string, aliases []string) error {
	return c.do(ctx, "POST", "/networks/"+url.PathEscape(networkID)+"/connect", map[string]any{
		"Container": containerID, "EndpointConfig": map[string]any{"Aliases": aliases},
	}, nil)
}
