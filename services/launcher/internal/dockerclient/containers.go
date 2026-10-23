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
	// State é o estado de execução -- Health só existe se o container
	// tiver healthcheck.
	State struct {
		Status   string `json:"Status"` // "created", "running", "exited", ...
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
		Health   *struct {
			Status string `json:"Status"` // "starting", "healthy", "unhealthy"
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	HostConfig   struct {
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
	// Healthcheck substitui o HEALTHCHECK da imagem -- usado pelos addons
	// (pg_isready, redis-cli ping), cujo estado se lê depois em
	// ContainerInspect.State.Health.
	Healthcheck *Healthcheck `json:"Healthcheck,omitempty"`
	// NetworkingConfig dá aliases DNS ao container na sua rede (ex: "db"
	// para um addon), além do nome do container.
	NetworkingConfig *NetworkingConfig `json:"NetworkingConfig,omitempty"`
}

// Healthcheck segue Config.Healthcheck da API: durações em nanossegundos.
type Healthcheck struct {
	Test        []string `json:"Test"`
	Interval    int64    `json:"Interval,omitempty"`
	Timeout     int64    `json:"Timeout,omitempty"`
	Retries     int      `json:"Retries,omitempty"`
	StartPeriod int64    `json:"StartPeriod,omitempty"`
}

type NetworkingConfig struct {
	EndpointsConfig map[string]EndpointConfig `json:"EndpointsConfig"`
}

type EndpointConfig struct {
	Aliases []string `json:"Aliases,omitempty"`
}

// WithAliases devolve o NetworkingConfig que dá estes aliases na rede.
func WithAliases(network string, aliases ...string) *NetworkingConfig {
	return &NetworkingConfig{EndpointsConfig: map[string]EndpointConfig{network: {Aliases: aliases}}}
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
	// RestartPolicy diz ao daemon o que fazer quando o container morre ou
	// o próprio daemon reinicia (ex: reboot do host). Ponteiro porque
	// omitir = "no", o default do Docker.
	RestartPolicy *RestartPolicy `json:"RestartPolicy,omitempty"`
	// SecurityOpt, ex: "no-new-privileges" -- ver AppSecurityOpt.
	SecurityOpt []string `json:"SecurityOpt,omitempty"`

	Resources
}

// AppSecurityOpt é o que todo container de APLICAÇÃO recebe: um processo
// lá dentro nunca ganha mais privilégios do que arrancou com (setuid,
// sudo, file capabilities). Baixar privilégios (ex: gosu de root para um
// utilizador) continua a funcionar.
func AppSecurityOpt() []string {
	return []string{"no-new-privileges:true"}
}

// RestartPolicy segue o formato de HostConfig.RestartPolicy da API do Docker.
type RestartPolicy struct {
	// Name: "no", "always", "unless-stopped" ou "on-failure".
	Name string `json:"Name"`
	// MaximumRetryCount só é respeitado com "on-failure".
	MaximumRetryCount int `json:"MaximumRetryCount,omitempty"`
}

// UnlessStopped volta a subir o container se ele morrer ou se o daemon
// reiniciar, mas NÃO depois de um stop explícito (scale down, "parar" no
// portal) -- é o que torna um stop pedido por nós definitivo e um crash ou
// reboot do host transparente.
func UnlessStopped() *RestartPolicy {
	return &RestartPolicy{Name: "unless-stopped"}
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

// WaitContainer bloqueia até o container terminar e devolve o exit code.
func (c *Client) WaitContainer(ctx context.Context, id string) (int, error) {
	var resp struct {
		StatusCode int `json:"StatusCode"`
		Error      *struct {
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := c.do(ctx, "POST", "/containers/"+id+"/wait", nil, &resp); err != nil {
		return -1, err
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return resp.StatusCode, fmt.Errorf("dockerclient: wait %s: %s", id, resp.Error.Message)
	}
	return resp.StatusCode, nil
}

// ContainerLogs devolve stdout+stderr do container (as últimas tail
// linhas; tail <= 0 = tudo), já sem os cabeçalhos de multiplexação.
func (c *Client) ContainerLogs(ctx context.Context, id string, tail int) (string, error) {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}}
	if tail > 0 {
		q.Set("tail", fmt.Sprint(tail))
	}
	raw, err := c.doRaw(ctx, "GET", "/containers/"+id+"/logs?"+q.Encode())
	if err != nil {
		return "", err
	}
	return demuxLogs(raw), nil
}

// demuxLogs tira os cabeçalhos de 8 bytes que o Docker põe em cada frame
// de stdout/stderr quando o container não tem TTY. Se o formato não bater
// (container com TTY, stream já cru), devolve o texto tal como veio.
func demuxLogs(raw []byte) string {
	var out []byte
	for len(raw) >= 8 {
		stream := raw[0]
		if (stream != 0 && stream != 1 && stream != 2) || raw[1] != 0 || raw[2] != 0 || raw[3] != 0 {
			return string(raw)
		}
		size := int(raw[4])<<24 | int(raw[5])<<16 | int(raw[6])<<8 | int(raw[7])
		if 8+size > len(raw) {
			return string(append(out, raw[8:]...))
		}
		out = append(out, raw[8:8+size]...)
		raw = raw[8+size:]
	}
	return string(out)
}
