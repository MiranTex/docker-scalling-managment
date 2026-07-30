package oauth

import (
	"context"
	"net/http"

	"golang.org/x/oauth2/google"
)

// googleUserInfoURL é o endpoint OIDC padrão do Google -- devolve as
// claims do utilizador autenticado (incluindo email_verified, que a
// lógica de account linking depende).
const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

// NewGoogleProvider monta o provider Google real (endpoints de produção).
func NewGoogleProvider(clientID, clientSecret, redirectURL string) Provider {
	return NewProvider("google", google.Endpoint, clientID, clientSecret, redirectURL,
		[]string{"openid", "email"}, fetchGoogleIdentity(googleUserInfoURL))
}

// fetchGoogleIdentity é parametrizado pelo userInfoURL só para os testes
// deste pacote poderem apontar para um servidor falso -- em produção é
// sempre googleUserInfoURL (ver NewGoogleProvider).
func fetchGoogleIdentity(userInfoURL string) FetchIdentityFunc {
	return func(ctx context.Context, client *http.Client) (Identity, error) {
		var body struct {
			Sub           string `json:"sub"`
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
		}
		if err := decodeJSONGet(ctx, client, userInfoURL, &body); err != nil {
			return Identity{}, err
		}
		return Identity{ProviderUserID: body.Sub, Email: body.Email, EmailVerified: body.EmailVerified}, nil
	}
}
