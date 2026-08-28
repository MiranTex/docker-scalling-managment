package dockerclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// Container é a representação resumida que a API devolve em /containers/json.
// Trazemos só os campos que importam: identidade, imagem, estado, labels
// (é pelas labels que vamos saber a qual serviço/group um container
// pertence) e a(s) rede(s) a que está ligado -- só os nomes, sem IP nem
// mais nada (ver PrimaryNetwork), o suficiente para GET /v1/groups
// mostrar em que rede cada group está.
type Container struct {
	ID              string            `json:"Id"`
	Names           []string          `json:"Names"`
	Image           string            `json:"Image"`
	State           string            `json:"State"`  // "running", "exited", ...
	Status          string            `json:"Status"` // texto legível, ex: "Up 5 minutes"
	Labels          map[string]string `json:"Labels"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

// PrimaryNetwork devolve o nome de uma das redes do container -- só faz
// sentido chamar para um container que devia estar numa única rede
// definida pelo utilizador (ver internal/httpapi.GroupSummary), não para
// um com múltiplas redes.
func (c Container) PrimaryNetwork() string {
	for name := range c.NetworkSettings.Networks {
		return name
	}
	return ""
}

// NetworkNames devolve TODAS as redes a que o container está ligado --
// ao contrário de PrimaryNetwork, usado onde múltiplas redes importam
// (ver internal/httpapi.handleListAllContainers, que alimenta o picker
// de "ligar este container a outra rede").
func (c Container) NetworkNames() []string {
	names := make([]string, 0, len(c.NetworkSettings.Networks))
	for name := range c.NetworkSettings.Networks {
		names = append(names, name)
	}
	return names
}

// ListContainers lista containers do daemon. Por padrão a API só devolve os
// que estão rodando; passe all=true pra incluir parados/finalizados também.
// filters segue o formato de filtro do Docker, ex: {"label": {"autoscaler.service=web"}}.
func (c *Client) ListContainers(ctx context.Context, all bool, filters map[string][]string) ([]Container, error) {
	q := url.Values{}
	if all {
		q.Set("all", "true")
	}
	if len(filters) > 0 {
		encoded, err := json.Marshal(filters)
		if err != nil {
			return nil, fmt.Errorf("dockerclient: encoding filters: %w", err)
		}
		q.Set("filters", string(encoded))
	}

	path := "/containers/json"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var containers []Container
	if err := c.do(ctx, "GET", path, nil, &containers); err != nil {
		return nil, err
	}
	return containers, nil
}

// ContainerInspect é o subconjunto de /containers/{id}/json que precisamos
// pra saber como recriar um container equivalente (mesma imagem, comando,
// variáveis de ambiente e labels).
type ContainerInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Cmd    []string          `json:"Cmd"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		// Binds preserva os volumes montados (ex: código-fonte da app no
		// Laravel Sail) no formato "origem:destino[:modo]" que a própria API
		// aceita de volta em /containers/create.
		Binds []string `json:"Binds"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		// IPAddress só vem preenchido quando o container está na rede
		// "bridge" padrão. Containers em redes definidas pelo usuário (ex:
		// as que o docker-compose cria) só aparecem dentro de Networks.
		IPAddress string `json:"IPAddress"`
		Networks  map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// PrimaryNetwork devolve o nome de uma das redes do Docker em que o
// container está conectado (ex: a rede que o docker-compose cria pro
// projeto). Necessário pra recriar um container que precisa enxergar outros
// serviços (banco, cache) na mesma rede — sem isso, o Docker conectaria o
// clone na rede "bridge" padrão, isolado do resto do projeto.
//
// Se o container estiver em mais de uma rede, só a primeira encontrada é
// usada: a API só permite atribuir uma rede na criação, as demais exigiriam
// chamadas extras em /networks/{id}/connect depois de criar o container.
func (i *ContainerInspect) PrimaryNetwork() string {
	for name := range i.NetworkSettings.Networks {
		return name
	}
	return ""
}

// IPAddress devolve o primeiro endereço IP encontrado para o container,
// olhando primeiro a rede "bridge" padrão e depois qualquer rede definida
// pelo usuário em que o container esteja. Retorna "" se não achar nenhum
// (ex: container ainda não iniciou).
func (i *ContainerInspect) IPAddress() string {
	if i.NetworkSettings.IPAddress != "" {
		return i.NetworkSettings.IPAddress
	}
	for _, network := range i.NetworkSettings.Networks {
		if network.IPAddress != "" {
			return network.IPAddress
		}
	}
	return ""
}

// InspectContainer devolve a configuração completa de um container existente.
func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerInspect, error) {
	var inspect ContainerInspect
	if err := c.do(ctx, "GET", "/containers/"+id+"/json", nil, &inspect); err != nil {
		return nil, err
	}
	return &inspect, nil
}

// CreateContainerRequest é o subconjunto de opções que a API /containers/create
// aceita e que vamos precisar para criar réplicas de um serviço.
type CreateContainerRequest struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd,omitempty"`
	Env        []string          `json:"Env,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
	HostConfig *CreateHostConfig `json:"HostConfig,omitempty"`
}

// CreateHostConfig é o subconjunto de HostConfig que importa na criação: os
// volumes montados e a rede à qual o container deve se conectar. Deliberadamente
// não inclui publicação de porta pro host — réplicas de um mesmo serviço não
// podem competir pela mesma porta no host, então elas só devem ser
// alcançadas pelo IP interno (pelo load balancer), nunca via localhost direto.
type CreateHostConfig struct {
	Binds       []string `json:"Binds,omitempty"`
	NetworkMode string   `json:"NetworkMode,omitempty"`
	// ExtraHosts adiciona entradas estáticas em /etc/hosts do container,
	// formato "hostname:ip" (ex: "host.docker.internal:host-gateway") --
	// não tem relação com publicação de porta, então não conflita entre
	// réplicas do mesmo serviço.
	ExtraHosts []string `json:"ExtraHosts,omitempty"`

	Resources
}

// Resources são os limites de recursos aplicados ao container, derivados
// do "tipo de instância" escolhido no momento do lançamento (ver
// services/templatesadmin, catálogo service_templates.instance_types).
// São limites duros do cgroup: um container que tente passar do teto de
// CPU é estrangulado, e um que passe do teto de memória é morto pelo
// OOM-killer -- em ambos os casos sem arrastar o host nem os vizinhos.
type Resources struct {
	// NanoCPUs é a fração de CPU em bilionésimos: 1.5 vCPU = 1500000000.
	NanoCPUs int64 `json:"NanoCpus,omitempty"`
	// Memory é o teto de RAM em bytes. O daemon recusa valores abaixo de 6 MiB.
	Memory int64 `json:"Memory,omitempty"`
	// MemorySwap é memória + swap. Igual a Memory desativa o swap, que é o
	// que queremos por omissão: com swap, um container que estoura o limite
	// degrada o disco do host inteiro em vez de morrer depressa.
	MemorySwap int64 `json:"MemorySwap,omitempty"`
	// PidsLimit trava fork bombs. Ponteiro porque 0 é um valor legítimo
	// (ilimitado) e omitir é diferente de mandar zero.
	PidsLimit *int64 `json:"PidsLimit,omitempty"`
}

// CreateContainer cria (mas não inicia) um novo container e devolve seu ID.
// name pode ser vazio para deixar o Docker gerar um nome aleatório.
func (c *Client) CreateContainer(ctx context.Context, name string, req CreateContainerRequest) (string, error) {
	path := "/containers/create"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}

	var resp struct {
		ID       string   `json:"Id"`
		Warnings []string `json:"Warnings"`
	}
	if err := c.do(ctx, "POST", path, req, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// StartContainer inicia um container já criado.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/containers/"+id+"/start", nil, nil)
}

// StopContainer para um container em execução, esperando até timeoutSeconds
// pelo shutdown gracioso antes de matar o processo. timeoutSeconds < 0 usa o
// padrão do Docker.
func (c *Client) StopContainer(ctx context.Context, id string, timeoutSeconds int) error {
	path := "/containers/" + id + "/stop"
	if timeoutSeconds >= 0 {
		path += fmt.Sprintf("?t=%d", timeoutSeconds)
	}
	return c.do(ctx, "POST", path, nil, nil)
}

// RemoveContainer remove um container. force=true também remove containers
// em execução (equivalente a `docker rm -f`).
func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	path := "/containers/" + id
	if force {
		path += "?force=true"
	}
	return c.do(ctx, "DELETE", path, nil, nil)
}
