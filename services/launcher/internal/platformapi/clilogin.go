package platformapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Login da CLI pelo browser (o "device flow" do GitHub CLI): o bs pede um
// par de códigos, abre o portal no código curto, a pessoa entra como
// sempre e autoriza; o portal cria-lhe uma API key e entrega-a aqui; o bs,
// que entretanto pergunta com o código longo (secreto), recebe-a uma vez.
// Tudo em memória: um pedido vive minutos e um restart só obriga a
// recomeçar.
//
//	POST /v1/cli/login               {name}      -> {deviceCode, userCode, verificationUrl, ...}
//	POST /v1/cli/login/poll          {deviceCode} -> 200 {apiKey} | 428 pendente | 410 expirado
//	GET  /v1/cli/login/{code}        (sessão)    -> {name, expiresAt}   (a página do portal)
//	POST /v1/cli/login/{code}/approve (sessão)   {apiKey}

const (
	cliLoginTTL      = 10 * time.Minute
	cliLoginInterval = 2 // segundos entre polls
	cliLoginMax      = 100
)

type cliLogin struct {
	deviceHash string
	name       string
	expires    time.Time
	apiKey     string // depois de aprovado, até o bs o levar
	approvedBy string
}

type cliLogins struct {
	mu        sync.Mutex
	byCode    map[string]*cliLogin // código curto -> pedido
	portalURL string
}

func newCLILogins() *cliLogins { return &cliLogins{byCode: map[string]*cliLogin{}} }

// WithPortalURL diz onde está o portal (a página de autorização).
func (a *API) WithPortalURL(url string) *API {
	a.cli.portalURL = strings.TrimRight(url, "/")
	return a
}

func (c *cliLogins) prune(now time.Time) {
	for code, l := range c.byCode {
		if now.After(l.expires) {
			delete(c.byCode, code)
		}
	}
}

// userCode: 8 letras sem ambiguidades (sem 0/O, 1/I), como "BXKM-7QRT" --
// para comparar de olhos entre o terminal e o browser.
func userCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:])
}

func hashCode(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (a *API) cliLoginStart(w http.ResponseWriter, r *http.Request) {
	if a.cli.portalURL == "" {
		writeError(w, http.StatusNotFound, "not_found", "o login pelo browser não está ligado nesta instalação")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if len(name) > 80 {
		name = name[:80]
	}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	device := base64.RawURLEncoding.EncodeToString(raw)

	now := time.Now()
	a.cli.mu.Lock()
	a.cli.prune(now)
	if len(a.cli.byCode) >= cliLoginMax {
		a.cli.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "too_many", "demasiados pedidos de login pendentes -- tenta daqui a uns minutos")
		return
	}
	code := userCode()
	for a.cli.byCode[code] != nil {
		code = userCode()
	}
	a.cli.byCode[code] = &cliLogin{deviceHash: hashCode(device), name: name, expires: now.Add(cliLoginTTL)}
	a.cli.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"deviceCode": device, "userCode": code,
		"verificationUrl": a.cli.portalURL + "/cli-login?code=" + code,
		"expiresIn":       int(cliLoginTTL / time.Second), "interval": cliLoginInterval,
	})
}

func (a *API) cliLoginPoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"deviceCode"`
	}
	if !decode(w, r, &body) {
		return
	}
	h := hashCode(body.DeviceCode)
	now := time.Now()
	a.cli.mu.Lock()
	defer a.cli.mu.Unlock()
	for code, l := range a.cli.byCode {
		if subtle.ConstantTimeCompare([]byte(l.deviceHash), []byte(h)) != 1 {
			continue
		}
		if now.After(l.expires) {
			delete(a.cli.byCode, code)
			writeError(w, http.StatusGone, "expired", "o pedido de login expirou -- corre o login outra vez")
			return
		}
		if l.apiKey == "" {
			writeError(w, http.StatusPreconditionRequired, "authorization_pending", "à espera da autorização no browser")
			return
		}
		// A key sai uma vez só.
		delete(a.cli.byCode, code)
		writeJSON(w, http.StatusOK, map[string]string{"apiKey": l.apiKey})
		return
	}
	writeError(w, http.StatusGone, "expired", "pedido de login desconhecido ou expirado -- corre o login outra vez")
}

// cliLoginGet: o que a página de autorização mostra.
func (a *API) cliLoginGet(w http.ResponseWriter, r *http.Request, _ principal) {
	code := strings.ToUpper(r.PathValue("code"))
	a.cli.mu.Lock()
	l := a.cli.byCode[code]
	a.cli.mu.Unlock()
	if l == nil || time.Now().After(l.expires) {
		writeError(w, http.StatusNotFound, "not_found", "código desconhecido ou expirado -- corre o login outra vez no terminal")
		return
	}
	// A impressão digital da CA: o bs mostra a mesma ao oferecer-se para
	// pôr o docker a confiar no registry.
	caHash := ""
	if a.agents != nil {
		caHash = a.agents.caHash
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": l.name, "expiresAt": l.expires, "approved": l.apiKey != "", "caHash": caHash})
}

// cliLoginApprove recebe a API key que o portal criou para quem autorizou.
func (a *API) cliLoginApprove(w http.ResponseWriter, r *http.Request, who principal) {
	var body struct {
		APIKey string `json:"apiKey"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !strings.HasPrefix(body.APIKey, "ak_") {
		writeError(w, http.StatusBadRequest, "invalid", "apiKey inválida")
		return
	}
	code := strings.ToUpper(r.PathValue("code"))
	a.cli.mu.Lock()
	l := a.cli.byCode[code]
	if l == nil || time.Now().After(l.expires) || l.apiKey != "" {
		a.cli.mu.Unlock()
		writeError(w, http.StatusNotFound, "not_found", "código desconhecido, expirado ou já usado")
		return
	}
	l.apiKey, l.approvedBy = body.APIKey, who.Subject
	a.cli.mu.Unlock()
	a.audit.Log(who.Subject, "cli.login", l.name, "autorizado")
	w.WriteHeader(http.StatusNoContent)
}
