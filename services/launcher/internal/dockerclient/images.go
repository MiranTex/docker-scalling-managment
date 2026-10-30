package dockerclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ImageInspect é o subconjunto de /images/{name}/json que precisamos: se a
// imagem existe no daemon local e para que plataforma é.
type ImageInspect struct {
	ID           string `json:"Id"`
	Os           string `json:"Os"`
	Architecture string `json:"Architecture"`
	Config       struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

// InspectImage verifica se uma imagem já existe localmente no daemon.
// Devolve erro se não existir (404) ou se a comunicação falhar.
func (c *Client) InspectImage(ctx context.Context, ref string) (*ImageInspect, error) {
	var inspect ImageInspect
	if err := c.do(ctx, "GET", "/images/"+url.PathEscape(ref)+"/json", nil, &inspect); err != nil {
		return nil, err
	}
	return &inspect, nil
}

// pullProgressLine é uma linha do stream que /images/create devolve -- a
// resposta não é um único JSON, é um objeto por linha de progresso.
type pullProgressLine struct {
	Error string `json:"error"`
}

// PullImage baixa uma imagem do registry e só devolve quando o pull
// termina (bloqueante) ou falha (ex: tag inexistente, registry
// inacessível). Usado pra validar o launch template no arranque do
// group, antes de precisar dele de verdade num scale up.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	return c.PullImageWithAuth(ctx, ref, nil)
}

// RegistryAuth são as credenciais de um registry privado, no formato que
// a API do Docker espera (base64 de JSON no cabeçalho X-Registry-Auth).
type RegistryAuth struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	ServerAddress string `json:"serveraddress"`
}

// PullImageWithAuth é PullImage com credenciais (auth nil = anónimo).
func (c *Client) PullImageWithAuth(ctx context.Context, ref string, auth *RegistryAuth) error {
	repo, tag := splitImageRef(ref)
	q := url.Values{"fromImage": {repo}, "tag": {tag}}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/images/create?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("dockerclient: montando request de pull: %w", err)
	}
	if auth != nil {
		encoded, err := json.Marshal(auth)
		if err != nil {
			return fmt.Errorf("dockerclient: codificando credenciais: %w", err)
		}
		req.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString(encoded))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("dockerclient: pull da imagem %q: %w", ref, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dockerclient: pull da imagem %q: status %d: %s", ref, resp.StatusCode, string(raw))
	}

	// O corpo é um stream de objetos JSON (um por linha de progresso do
	// pull), não um único JSON -- por isso decodificamos em loop até
	// esgotar, checando se alguma linha reporta erro no meio do caminho.
	dec := json.NewDecoder(resp.Body)
	for {
		var line pullProgressLine
		if err := dec.Decode(&line); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("dockerclient: lendo progresso do pull de %q: %w", ref, err)
		}
		if line.Error != "" {
			return fmt.Errorf("dockerclient: pull da imagem %q: %s", ref, line.Error)
		}
	}
}

// splitImageRef separa "repo:tag" em (repo, tag), sem confundir a porta de
// um registry privado (ex: "localhost:5000/app") com uma tag -- só o ':'
// depois da última '/' conta como separador de tag. Sem tag, assume "latest".
func splitImageRef(ref string) (repo, tag string) {
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// RegistryHost devolve o registry de uma referência de imagem -- o
// primeiro componente se parecer um host (tem "." ou ":", ou é
// "localhost"), senão o Docker Hub.
func RegistryHost(ref string) string {
	first, _, found := strings.Cut(ref, "/")
	if found && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first
	}
	return "docker.io"
}
