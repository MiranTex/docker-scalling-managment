package oauth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"golang.org/x/oauth2/endpoints"
)

const (
	githubUserURL   = "https://api.github.com/user"
	githubEmailsURL = "https://api.github.com/user/emails"
)

// NewGitHubProvider monta o provider GitHub real (endpoints de produção).
// Precisa do scope "user:email" -- o endpoint /user sozinho pode devolver
// email nulo (depende da preferência de privacidade da conta), então
// buscamos em /user/emails, que sempre lista os e-mails cadastrados e
// diz qual é o principal e qual está verificado.
func NewGitHubProvider(clientID, clientSecret, redirectURL string) Provider {
	return NewProvider("github", endpoints.GitHub, clientID, clientSecret, redirectURL,
		[]string{"read:user", "user:email"}, fetchGitHubIdentity(githubUserURL, githubEmailsURL))
}

// fetchGitHubIdentity é parametrizado pelas URLs só para os testes deste
// pacote poderem apontar para um servidor falso.
func fetchGitHubIdentity(userURL, emailsURL string) FetchIdentityFunc {
	return func(ctx context.Context, client *http.Client) (Identity, error) {
		var user struct {
			ID int64 `json:"id"`
		}
		if err := decodeJSONGet(ctx, client, userURL, &user); err != nil {
			return Identity{}, err
		}

		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if err := decodeJSONGet(ctx, client, emailsURL, &emails); err != nil {
			return Identity{}, err
		}

		for _, e := range emails {
			if e.Primary {
				return Identity{
					ProviderUserID: strconv.FormatInt(user.ID, 10),
					Email:          e.Email,
					EmailVerified:  e.Verified,
				}, nil
			}
		}
		return Identity{}, fmt.Errorf("oauth: nenhum e-mail principal encontrado na conta GitHub")
	}
}
