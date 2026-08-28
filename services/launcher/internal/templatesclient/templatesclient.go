// Package templatesclient busca um modelo de serviço (launch template +
// config de grupo) no templatesadmin (services/templatesadmin), pelo nome.
//
// Não tem credencial própria: o templatesadmin não tem role "service"
// (nada ali é segredo, ver seu package doc) -- só humanos/infra-admin
// conseguem ler modelos. Por isso este cliente reenvia o MESMO bearer
// token de quem chamou o launcher (ver internal/httpapi.handleCreateInstance)
// em vez de se autenticar como conta de máquina: se o chamador do launcher
// já provou ser infra-admin para criar a instância, esse mesmo token
// também é válido para o templatesadmin.
package templatesclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Template é store.Template (services/templatesadmin) tal como exposto
// pela API -- só o que o container da aplicação precisa (imagem, comando,
// env, labels, volumes, rede). Não tem nenhum campo de config de
// autoscaling (réplicas, thresholds de CPU, target service, portas de
// host) -- essa config é decidida no pedido de criação da instância, não
// faz parte do modelo (ver httpapi.createInstanceRequest, campos usados
// só quando Kind="group").
type Template struct {
	Name       string            `json:"name"`
	Image      string            `json:"image"`
	Cmd        []string          `json:"cmd"`
	Env        []string          `json:"env"`
	Labels     map[string]string `json:"labels"`
	Binds      []string          `json:"binds"`
	Network    string            `json:"network"`
	ExtraHosts []string          `json:"extraHosts"`
	// DefaultInstanceType é o tamanho aplicado quando o pedido de
	// lançamento não escolhe nenhum. Vazio cai no default global do
	// launcher (LAUNCHER_DEFAULT_INSTANCE_TYPE).
	DefaultInstanceType string `json:"defaultInstanceType"`
}

// InstanceType é store.InstanceType (services/templatesadmin) tal como
// exposto pela API -- o par (vCPU, memória) que o launcher traduz para
// limites de cgroup ao criar o container.
type InstanceType struct {
	Name         string  `json:"name"`
	DisplayName  string  `json:"displayName"`
	VCPU         float64 `json:"vcpu"`
	MemoryMB     int64   `json:"memoryMb"`
	MemorySwapMB *int64  `json:"memorySwapMb"`
	PidsLimit    int64   `json:"pidsLimit"`
	Enabled      bool    `json:"enabled"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func New(templatesAdminURL string) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(templatesAdminURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Get devolve o modelo chamado name, autenticando com o bearerToken (o
// token de quem chamou o launcher, reenviado tal como veio).
func (c *Client) Get(ctx context.Context, name, bearerToken string) (Template, error) {
	var t Template
	err := c.get(ctx, "/v1/templates/"+name, bearerToken, &t)
	return t, err
}

// GetInstanceType devolve um tipo do catálogo de tamanhos.
func (c *Client) GetInstanceType(ctx context.Context, name, bearerToken string) (InstanceType, error) {
	var it InstanceType
	err := c.get(ctx, "/v1/instance-types/"+name, bearerToken, &it)
	return it, err
}

func (c *Client) get(ctx context.Context, path, bearerToken string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("templatesclient: chamando templatesadmin: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(res.Body).Decode(&apiErr)
		return fmt.Errorf("templatesclient: templatesadmin respondeu %d: %s", res.StatusCode, apiErr.Message)
	}

	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("templatesclient: decodificando resposta: %w", err)
	}
	return nil
}
