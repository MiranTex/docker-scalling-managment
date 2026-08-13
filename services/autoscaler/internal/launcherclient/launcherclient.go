// Package launcherclient pede ao services/launcher que crie UMA réplica
// nova a partir do launch template deste group -- é a chamada que, antes
// desta feature, cada cmd/group fazia sozinho, localmente, contra o Docker
// (ver services/autoscaler/internal/executor.scaleUpFromTemplate, removido)
// depois de resolver ${secret:NOME} ele mesmo (ver
// services/autoscaler/internal/secretsclient, também removido daqui).
//
// A vantagem de delegar: só o launcher precisa de credencial para falar
// com o secretsadmin -- este cliente manda o "env" tal como está, com
// referências ${secret:...} por resolver, e o launcher resolve do lado
// dele antes de criar o container.
//
// Autenticação: o mesmo desenho de sempre para uma conta de MÁQUINA (role
// "service", ver services/auth/internal/store/role.go) -- processo arranca
// já com um refresh token emitido administrativamente e passado via
// LAUNCHER_REFRESH_TOKEN, sem nunca fazer login por password.
package launcherclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// LaunchTemplate é o subconjunto do launch template deste group que o
// launcher precisa para criar a réplica -- mesmos campos de
// executor.LaunchTemplate, repetidos aqui para este pacote não depender de
// internal/executor (e vice-versa).
type LaunchTemplate struct {
	Image      string            `json:"image"`
	Cmd        []string          `json:"cmd,omitempty"`
	Env        []string          `json:"env,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Binds      []string          `json:"binds,omitempty"`
	Network    string            `json:"network,omitempty"`
	ExtraHosts []string          `json:"extraHosts,omitempty"`
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type createReplicaResponse struct {
	ContainerID string `json:"containerId"`
}

// Client pede réplicas novas ao launcher, gerindo o ciclo de vida do
// próprio access token por trás -- mesmo padrão de
// services/launcher/internal/secretsclient.Client, só que aqui contra o
// launcher em vez do secretsadmin.
type Client struct {
	launcherURL    string
	authServiceURL string
	httpClient     *http.Client

	mu           sync.Mutex
	refreshToken string
	accessToken  string
	accessExpiry time.Time
}

// New cria um Client. Se launcherURL ou initialRefreshToken estiverem
// vazios, CreateReplica falha explicitamente -- ao contrário do
// secretsclient, não há "caminho rápido sem chamada de rede" aqui: criar
// uma réplica SEMPRE precisa do launcher.
func New(launcherURL, authServiceURL, initialRefreshToken string) *Client {
	return &Client{
		launcherURL:    strings.TrimSuffix(launcherURL, "/"),
		authServiceURL: strings.TrimSuffix(authServiceURL, "/"),
		refreshToken:   initialRefreshToken,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

// CreateReplica pede ao launcher para criar+iniciar um container a partir
// de template e devolve o ID do container criado. template.Env pode
// conter referências ${secret:NOME} não resolvidas -- é o launcher quem
// resolve, nunca este cliente.
func (c *Client) CreateReplica(ctx context.Context, template LaunchTemplate) (string, error) {
	if c.launcherURL == "" || c.refreshToken == "" {
		return "", fmt.Errorf("launcherclient: LAUNCHER_SERVICE_URL/LAUNCHER_REFRESH_TOKEN não configurados")
	}

	token, err := c.accessTokenFor(ctx)
	if err != nil {
		return "", fmt.Errorf("launcherclient: obtendo access token: %w", err)
	}

	body, err := json.Marshal(template)
	if err != nil {
		return "", fmt.Errorf("launcherclient: codificando launch template: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.launcherURL+"/v1/replicas", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("launcherclient: chamando launcher: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusCreated {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(res.Body).Decode(&apiErr)
		return "", fmt.Errorf("launcherclient: launcher respondeu %d: %s", res.StatusCode, apiErr.Message)
	}

	var out createReplicaResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("launcherclient: decodificando resposta: %w", err)
	}
	return out.ContainerID, nil
}

// accessTokenFor devolve um access token válido, usando o cache em
// memória se ainda não estiver perto de expirar, ou pedindo um novo via
// refresh (com rotação) caso contrário. Idêntico a
// services/launcher/internal/secretsclient.Client.accessTokenFor -- não
// dá para partilhar código entre módulos Go independentes sem um módulo
// partilhado (ver jwtverify, copiado pelo mesmo motivo em vários serviços
// deste repo).
func (c *Client) accessTokenFor(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	const safetyMargin = 30 * time.Second
	if c.accessToken != "" && time.Now().Add(safetyMargin).Before(c.accessExpiry) {
		return c.accessToken, nil
	}

	body, _ := json.Marshal(map[string]string{"refresh_token": c.refreshToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.authServiceURL+"/v1/token/refresh", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("chamando %s/v1/token/refresh: %w", c.authServiceURL, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth service respondeu %d ao renovar o token -- LAUNCHER_REFRESH_TOKEN pode estar expirado/revogado", res.StatusCode)
	}

	var pair tokenPair
	if err := json.NewDecoder(res.Body).Decode(&pair); err != nil {
		return "", fmt.Errorf("decodificando resposta de refresh: %w", err)
	}

	c.accessToken = pair.AccessToken
	c.refreshToken = pair.RefreshToken
	c.accessExpiry = time.Now().Add(time.Duration(pair.ExpiresIn) * time.Second)
	return c.accessToken, nil
}
