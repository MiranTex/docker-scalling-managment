// Package oauth implementa login social: o utilizador autentica-se num
// provider externo (Google, GitHub, ...) e este serviço troca isso por
// uma conta própria -- criando uma nova, ou ligando à conta já existente
// com o mesmo e-mail (account linking).
//
// A troca do código de autorização por um token usa golang.org/x/oauth2
// (mantido pela própria equipa do Go) em vez de reimplementar o protocolo
// -- ao contrário do JWT/hashing de senha, aqui não há ganho educativo que
// compense o risco de um erro sutil no fluxo OAuth2.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
)

// Identity é o que um provider nos diz sobre quem acabou de se autenticar.
type Identity struct {
	Provider       string
	ProviderUserID string
	Email          string
	// EmailVerified importa MUITO para a lógica de account linking (ver
	// link.go): só ligamos a uma conta já existente se o próprio provider
	// confirma que o e-mail é verificado -- senão, alguém poderia criar
	// uma identidade OAuth com um e-mail que não é dele e assumir a conta
	// de outra pessoa.
	EmailVerified bool
}

// FetchIdentityFunc busca e interpreta os dados do utilizador autenticado
// -- cada provider tem um endpoint e um formato de resposta diferentes,
// então esta função é o único ponto específico de cada provider (tudo o
// resto do fluxo OAuth2 em si é genérico).
type FetchIdentityFunc func(ctx context.Context, client *http.Client) (Identity, error)

// Provider é um provider OAuth2/OIDC configurado e pronto pra uso.
type Provider struct {
	Name          string
	config        *oauth2.Config
	fetchIdentity FetchIdentityFunc
}

// NewProvider monta um Provider genérico -- usado por NewGoogleProvider e
// NewGitHubProvider com os endpoints reais, e diretamente pelos testes
// deste pacote com um endpoint falso (httptest.Server), pra exercitar o
// fluxo inteiro sem depender de rede externa nenhuma.
func NewProvider(name string, endpoint oauth2.Endpoint, clientID, clientSecret, redirectURL string, scopes []string, fetchIdentity FetchIdentityFunc) Provider {
	return Provider{
		Name: name,
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     endpoint,
			RedirectURL:  redirectURL,
			Scopes:       scopes,
		},
		fetchIdentity: fetchIdentity,
	}
}

// AuthCodeURL devolve o URL para onde redirecionar o utilizador, com state
// embutido (o chamador é responsável por gerar um state imprevisível e
// validá-lo de volta no callback -- ver httpapi, que usa um cookie para
// isso).
func (p Provider) AuthCodeURL(state string) string {
	return p.config.AuthCodeURL(state)
}

// exchangeAndFetch troca o código de autorização por um token de acesso
// junto ao provider e usa esse token para buscar a identidade de quem
// autenticou.
func (p Provider) exchangeAndFetch(ctx context.Context, code string) (Identity, error) {
	tok, err := p.config.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("oauth: trocando código por token (%s): %w", p.Name, err)
	}

	client := p.config.Client(ctx, tok)
	identity, err := p.fetchIdentity(ctx, client)
	if err != nil {
		return Identity{}, fmt.Errorf("oauth: buscando identidade (%s): %w", p.Name, err)
	}
	identity.Provider = p.Name
	return identity, nil
}

// decodeJSONGet é um helper comum aos FetchIdentityFunc de cada provider:
// GET num endpoint autenticado (client já carrega o token OAuth2) e
// decodifica a resposta JSON em dst.
func decodeJSONGet(ctx context.Context, client *http.Client, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("oauth: montando requisição para %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("oauth: chamando %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("oauth: %s devolveu status %d: %s", url, resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("oauth: decodificando resposta de %s: %w", url, err)
	}
	return nil
}
