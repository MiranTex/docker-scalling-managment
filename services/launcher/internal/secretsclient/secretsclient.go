// Package secretsclient resolve referências ${secret:NOME} dentro do
// "env" de um launch template contra o secretsadmin
// (services/secretsadmin), autenticando como uma conta de MÁQUINA (role
// "service", ver services/auth/internal/store/role.go) através do mesmo
// fluxo de login+refresh do auth service que qualquer cliente humano usa
// -- só que aqui o "login" nunca acontece (não há password), o processo
// arranca já com um refresh token emitido administrativamente
// (POST /v1/admin/users/{id}/tokens no auth service) e passado via
// LAUNCHER_SECRETS_REFRESH_TOKEN.
//
// Movido de services/autoscaler/internal/secretsclient: antes, era cada
// cmd/group (um processo POR serviço autoscalado) que precisava da sua
// própria conta de máquina e do seu próprio SECRETS_REFRESH_TOKEN para
// resolver segredos antes de criar uma réplica. Agora só o launcher tem
// essa credencial -- é ele quem efetivamente cria o container (ver
// services/launcher/internal/httpapi, POST /v1/replicas), então só ele
// precisa saber falar com o secretsadmin. Um cmd/group que precise de uma
// réplica nova manda o launch template (ainda com ${secret:...} por
// resolver) para o launcher via services/autoscaler/internal/launcherclient,
// em vez de resolver ele mesmo.
//
// Isto também elimina o deadlock que existia antes: um group cujas
// réplicas dependessem dele mesmo para obter token (ex: group-authd, cujo
// AUTH_SERVICE_URL aponta pra ele próprio) só conseguia resolver segredos
// se já tivesse pelo menos uma réplica de pé -- e criar essa réplica
// dependia de resolver os segredos primeiro. Como o launcher é um
// processo separado, sempre de pé independente de qualquer group, esse
// ciclo não existe mais para a operação de CRIAR uma réplica (o launcher
// ainda depende do auth service estar alcançável para renovar o SEU
// próprio token, mas essa é uma dependência normal, não circular).
//
// Limitação aceite (documentada, não resolvida nesta iteração): o refresh
// token roda a cada uso (rotate-on-use, ver services/auth/internal/refresh)
// -- o valor novo só existe em memória deste processo. Se o CONTAINER do
// launcher for recriado depois de já ter rodado o token pelo menos uma
// vez, o LAUNCHER_SECRETS_REFRESH_TOKEN original já não é válido e a
// resolução de segredos passa a falhar até alguém emitir um token novo.
// Persistir o refresh token mais recente num ficheiro em volume (mesmo
// padrão de services/auth/internal/keystore) resolveria isto; fica para
// quando isso se mostrar um problema real, não preventivamente.
package secretsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// refRe casa ${secret:NOME} dentro de qualquer string -- o mesmo charset
// de nome aceite pelo secretsadmin (ver internal/httpapi.nameRe lá).
var refRe = regexp.MustCompile(`\$\{secret:([A-Za-z0-9_.-]+)\}`)

// ExtractNames devolve, sem duplicados, os nomes de segredo referenciados
// em qualquer entrada de env (formato "KEY=VALUE", mas a referência pode
// estar em qualquer parte do valor, ex. embutida numa URL).
func ExtractNames(env []string) []string {
	seen := map[string]struct{}{}
	var names []string
	for _, e := range env {
		for _, m := range refRe.FindAllStringSubmatch(e, -1) {
			name := m[1]
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	return names
}

func substitute(env string, values map[string]string) string {
	return refRe.ReplaceAllStringFunc(env, func(match string) string {
		name := refRe.FindStringSubmatch(match)[1]
		return values[name]
	})
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type resolveResponse struct {
	Values map[string]string `json:"values"`
}

// Client resolve nomes de segredo em valores, contra secretsadmin, gerindo
// o ciclo de vida do próprio access token por trás (refresh sob demanda,
// nunca exposto a quem chama ResolveEnv).
type Client struct {
	secretsAdminURL string
	authServiceURL  string
	httpClient      *http.Client

	mu           sync.Mutex
	refreshToken string
	accessToken  string
	accessExpiry time.Time
}

// New cria um Client. Se secretsAdminURL ou initialRefreshToken estiverem
// vazios, o Client resultante ainda funciona para launch templates SEM
// nenhuma referência ${secret:...} (ResolveEnv devolve env inalterado sem
// nenhuma chamada de rede); só falha ao encontrar uma referência de
// verdade sem estar configurado.
func New(secretsAdminURL, authServiceURL, initialRefreshToken string) *Client {
	return &Client{
		secretsAdminURL: strings.TrimSuffix(secretsAdminURL, "/"),
		authServiceURL:  strings.TrimSuffix(authServiceURL, "/"),
		refreshToken:    initialRefreshToken,
		httpClient:      &http.Client{Timeout: 10 * time.Second},
	}
}

// ResolveEnv devolve uma cópia de env com toda referência ${secret:NOME}
// substituída pelo valor atual desse segredo. Caminho rápido: se não há
// nenhuma referência, devolve env como veio, sem tocar na rede -- um
// launch template sem segredos nunca depende de secretsadmin estar de pé.
func (c *Client) ResolveEnv(ctx context.Context, env []string) ([]string, error) {
	names := ExtractNames(env)
	if len(names) == 0 {
		return env, nil
	}
	if c.secretsAdminURL == "" || c.refreshToken == "" {
		return nil, fmt.Errorf("secretsclient: %d referência(s) a segredo em env, mas SECRETSADMIN_SERVICE_URL/LAUNCHER_SECRETS_REFRESH_TOKEN não configurados", len(names))
	}

	values, err := c.resolve(ctx, names)
	if err != nil {
		return nil, err
	}

	resolved := make([]string, len(env))
	for i, e := range env {
		resolved[i] = substitute(e, values)
	}
	return resolved, nil
}

func (c *Client) resolve(ctx context.Context, names []string) (map[string]string, error) {
	token, err := c.accessTokenFor(ctx)
	if err != nil {
		return nil, fmt.Errorf("secretsclient: obtendo access token: %w", err)
	}

	body, _ := json.Marshal(map[string][]string{"names": names})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.secretsAdminURL+"/v1/secrets/resolve", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("secretsclient: chamando secretsadmin: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(res.Body).Decode(&apiErr)
		return nil, fmt.Errorf("secretsclient: secretsadmin respondeu %d: %s", res.StatusCode, apiErr.Message)
	}

	var out resolveResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("secretsclient: decodificando resposta: %w", err)
	}
	return out.Values, nil
}

// accessTokenFor devolve um access token válido, usando o cache em
// memória se ainda não estiver perto de expirar, ou pedindo um novo via
// refresh (com rotação) caso contrário.
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
		return "", fmt.Errorf("auth service respondeu %d ao renovar o token -- LAUNCHER_SECRETS_REFRESH_TOKEN pode estar expirado/revogado, ver limitação de rotação documentada no package doc", res.StatusCode)
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
