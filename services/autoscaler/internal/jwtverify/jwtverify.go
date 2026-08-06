// Package jwtverify valida access tokens (JWT RS256) emitidos pelo
// services/auth, sem nunca precisar da chave privada nem de um segredo
// partilhado -- só a chave PÚBLICA, publicada em
// GET /.well-known/jwks.json (ver services/auth/internal/token.JWKS).
//
// Cópia deliberada de services/database/dbadmin/internal/jwtverify (mesmo
// motivo: autoscaler e dbadmin são módulos Go independentes, sem um módulo
// partilhado no repo, e esta lógica de verificação já foi duplicada uma vez
// por esse motivo -- ver o comentário lá sobre não importar
// services/auth/internal/token diretamente). Extrair um módulo partilhado
// só se a duplicação vier a causar um bug corrigido só numa cópia.
package jwtverify

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Claims replica services/auth/internal/token.Claims -- mesmos nomes de
// campo (claims registados na RFC 7519 + a claim customizada "role").
type Claims struct {
	Subject   string `json:"sub"`
	Issuer    string `json:"iss,omitempty"`
	Audience  string `json:"aud,omitempty"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	Role      string `json:"role,omitempty"`
}

// ErrInvalidToken é a causa raiz de qualquer falha de validação --
// permite errors.Is(err, ErrInvalidToken) sem se importar com o motivo
// exato (mesmo padrão do token.Manager que este pacote espelha).
var ErrInvalidToken = errors.New("jwtverify: token inválido")

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// Verifier busca e mantém em cache as chaves públicas do auth service,
// revalidando-as periodicamente -- assim uma rotação de chave do lado do
// auth service (novo kid) é vista sem reiniciar este processo, e o preço
// de validar um token nunca é uma chamada de rede (só o refresh
// periódico é).
type Verifier struct {
	jwksURL  string
	issuer   string
	audience string
	ttl      time.Duration
	client   *http.Client

	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// NewVerifier cria um Verifier que busca chaves em
// "<authServiceURL>/.well-known/jwks.json", aceitando só tokens com o
// issuer/audience indicados (têm de bater com AUTH_ISSUER/AUTH_AUDIENCE
// configurados no auth service). refreshInterval controla de quanto em
// quanto tempo o cache de chaves é considerado velho o suficiente para
// buscar de novo.
func NewVerifier(authServiceURL, issuer, audience string, refreshInterval time.Duration) *Verifier {
	return &Verifier{
		jwksURL:  strings.TrimSuffix(authServiceURL, "/") + "/.well-known/jwks.json",
		issuer:   issuer,
		audience: audience,
		ttl:      refreshInterval,
		client:   &http.Client{Timeout: 5 * time.Second},
		keys:     map[string]*rsa.PublicKey{},
	}
}

// Verify confirma assinatura, algoritmo, issuer/audience e validade
// temporal de tokenString, devolvendo os claims se tudo bater.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return Claims{}, fmt.Errorf("%w: formato inesperado", ErrInvalidToken)
	}

	headerJSON, err := decodeSegment(parts[0])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: header não é base64url válido: %v", ErrInvalidToken, err)
	}
	var h header
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return Claims{}, fmt.Errorf("%w: header não é JSON válido: %v", ErrInvalidToken, err)
	}
	if h.Alg != "RS256" {
		return Claims{}, fmt.Errorf("%w: algoritmo não suportado: %q", ErrInvalidToken, h.Alg)
	}

	pub, err := v.publicKey(ctx, h.Kid)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	sig, err := decodeSegment(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: assinatura não é base64url válida: %v", ErrInvalidToken, err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return Claims{}, fmt.Errorf("%w: assinatura não confere", ErrInvalidToken)
	}

	claimsJSON, err := decodeSegment(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: claims não são base64url válido: %v", ErrInvalidToken, err)
	}
	var claims Claims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return Claims{}, fmt.Errorf("%w: claims não são JSON válido: %v", ErrInvalidToken, err)
	}
	if claims.Issuer != v.issuer {
		return Claims{}, fmt.Errorf("%w: issuer inesperado: %q", ErrInvalidToken, claims.Issuer)
	}
	if claims.Audience != v.audience {
		return Claims{}, fmt.Errorf("%w: audience inesperada: %q", ErrInvalidToken, claims.Audience)
	}
	if time.Now().Unix() >= claims.ExpiresAt {
		return Claims{}, fmt.Errorf("%w: expirado", ErrInvalidToken)
	}

	return claims, nil
}

// publicKey devolve a chave pública para kid, buscando/atualizando o
// cache de JWKS se necessário. Um kid desconhecido força um refresh
// imediato (fora do intervalo normal) antes de desistir -- cobre o caso
// de o auth service ter rodado a chave de assinatura há pouco.
func (v *Verifier) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	pub, ok := v.keys[kid]
	stale := time.Since(v.fetched) > v.ttl
	v.mu.RUnlock()
	if ok && !stale {
		return pub, nil
	}

	if err := v.refresh(ctx); err != nil {
		if ok {
			// Já tínhamos esta chave em cache; um refresh falho (auth
			// service temporariamente inalcançável) não deve derrubar
			// tokens que ainda validam com o que já sabemos.
			return pub, nil
		}
		return nil, fmt.Errorf("buscando JWKS: %w", err)
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	pub, ok = v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("chave desconhecida: %q", kid)
	}
	return pub, nil
}

func (v *Verifier) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	res, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("resposta %d de %s", res.StatusCode, v.jwksURL)
	}

	var doc jwksDocument
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decodificando JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nBytes, err := decodeSegment(k.N)
		if err != nil {
			continue
		}
		eBytes, err := decodeSegment(k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: int(new(big.Int).SetBytes(eBytes).Int64()),
		}
	}

	v.mu.Lock()
	v.keys = keys
	v.fetched = time.Now()
	v.mu.Unlock()
	return nil
}

func decodeSegment(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
