package dockerclient

import (
	"context"
	"encoding/json"
	"net/url"
)

// PlatformLabel/PlatformLabelValue identificam um recurso (rede ou
// container) como pertencente a ESTA plataforma -- é o que impede o
// launcher de listar/gerir redes e containers de OUTRAS apps que
// partilhem o mesmo host Docker (ex: outro projeto docker-compose
// qualquer, sem relação nenhuma com este). Toda rede criada por
// CreateNetwork ganha esta label automaticamente; todo container criado
// por launchSolo/launchGroup também. Os recursos estáticos deste próprio
// stack (a rede "auth-net" e cada serviço de demo/docker-compose.yml)
// têm de carregar a mesma label à mão -- ver esse ficheiro.
const (
	PlatformLabel      = "base-stack.managed"
	PlatformLabelValue = "true"
)

// Network é o subconjunto de /networks que nos interessa: só o suficiente
// para listar redes definidas pelo utilizador e deixar escolher uma na
// hora de lançar uma instância -- nada de IPAM, subnets ou opções de
// driver (ver services/launcher/internal/httpapi.handleListNetworks,
// "sem levar isto para um gestor de VPC").
type Network struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	Scope  string            `json:"Scope"`
	Labels map[string]string `json:"Labels"`
}

// defaultNetworkNames são as três redes que o próprio Docker cria sempre,
// em qualquer host -- nunca teriam a nossa label, mas ficam aqui como
// segunda camada de defesa, não a única.
var defaultNetworkNames = map[string]bool{"bridge": true, "host": true, "none": true}

func platformFilter() string {
	encoded, _ := json.Marshal(map[string][]string{"label": {PlatformLabel + "=" + PlatformLabelValue}})
	return string(encoded)
}

// ListNetworks devolve só as redes desta plataforma (ver PlatformLabel)
// -- nunca as de outra app qualquer a partilhar o mesmo host, nem as três
// automáticas do Docker.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	q := url.Values{"filters": {platformFilter()}}

	var all []Network
	if err := c.do(ctx, "GET", "/networks?"+q.Encode(), nil, &all); err != nil {
		return nil, err
	}

	out := make([]Network, 0, len(all))
	for _, n := range all {
		if defaultNetworkNames[n.Name] {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

// CreateNetwork cria uma rede Docker nova (driver "bridge", sem subnet
// nem opções -- o Docker escolhe tudo isso sozinho, mesmo comportamento
// de `docker network create <nome>` na linha de comandos), já marcada
// com PlatformLabel, e devolve o seu ID.
func (c *Client) CreateNetwork(ctx context.Context, name string) (string, error) {
	var resp struct {
		ID string `json:"Id"`
	}
	body := map[string]any{
		"Name":   name,
		"Labels": map[string]string{PlatformLabel: PlatformLabelValue},
	}
	if err := c.do(ctx, "POST", "/networks/create", body, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// NetworkContainer é um container ligado a uma rede, tal como devolvido
// dentro de "Containers" por GET /networks/{id} -- só o suficiente para
// mostrar/desligar, nunca o IP ou MAC.
type NetworkContainer struct {
	ContainerID string `json:"containerId"`
	Name        string `json:"name"`
}

// NetworkDetail é uma rede com os containers atualmente ligados a ela --
// é o que a página de gestão de rede usa para "editar" (ligar/desligar
// containers), já que o Docker não permite editar nome/subnet/driver de
// uma rede depois de criada.
type NetworkDetail struct {
	Network
	Containers []NetworkContainer `json:"containers"`
}

// InspectNetwork devolve uma rede e os containers ligados a ela. id pode
// ser o nome ou o Id da rede -- a API do Docker aceita os dois.
func (c *Client) InspectNetwork(ctx context.Context, id string) (*NetworkDetail, error) {
	var raw struct {
		Network
		Containers map[string]struct {
			Name string `json:"Name"`
		} `json:"Containers"`
	}
	if err := c.do(ctx, "GET", "/networks/"+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}

	// Inicializado como slice vazio, não nil -- um nil aqui serializa como
	// JSON "null", e a UI trata "null" como "ainda a carregar", não como
	// "rede sem nenhum container ligado".
	detail := NetworkDetail{Network: raw.Network, Containers: []NetworkContainer{}}
	for containerID, info := range raw.Containers {
		detail.Containers = append(detail.Containers, NetworkContainer{ContainerID: containerID, Name: info.Name})
	}
	return &detail, nil
}

// RemoveNetwork apaga uma rede -- o Docker recusa (erro 4xx, repassado
// tal como veio) se ainda houver algum container ligado a ela.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/networks/"+url.PathEscape(id), nil, nil)
}

// ConnectNetwork liga um container já em execução a uma rede -- é isto
// que resolve, sem recriar o container, um caso como o de uma instância
// lançada sem rede definida (ver internal/httpapi.resolveNetwork).
func (c *Client) ConnectNetwork(ctx context.Context, networkID, containerID string) error {
	return c.do(ctx, "POST", "/networks/"+url.PathEscape(networkID)+"/connect", map[string]string{"Container": containerID}, nil)
}

// DisconnectNetwork desliga um container de uma rede -- não o pára nem o
// remove, só corta essa ligação específica (um container pode continuar
// ligado a outras redes).
func (c *Client) DisconnectNetwork(ctx context.Context, networkID, containerID string) error {
	return c.do(ctx, "POST", "/networks/"+url.PathEscape(networkID)+"/disconnect", map[string]string{"Container": containerID}, nil)
}
