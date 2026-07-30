// Package token emite e valida JSON Web Tokens assinados com RS256
// (RSASSA-PKCS1-v1_5 usando SHA-256), do zero, só com a stdlib
// (crypto/rsa, crypto/sha256, encoding/json, encoding/base64) — sem
// biblioteca de JWT. Um JWT é só três partes separadas por ".",
// cada uma em base64url: header, payload (claims) e assinatura sobre
// "header.payload".
//
// RS256 (par de chaves assimétrico) em vez de HS256 (segredo partilhado)
// é a escolha certa aqui porque este serviço é pensado pra ser consumido
// por vários projetos ao mesmo tempo: cada um valida tokens com a chave
// PÚBLICA (via JWKS, ver Manager.JWKS), sem nunca precisar conhecer a
// chave privada nem coordenar um segredo partilhado com o auth service.
package token

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// KeySize é o tamanho, em bits, das chaves RSA geradas por GenerateKey.
// 2048 é o mínimo hoje considerado seguro (NIST/OWASP) e o que a maioria
// das bibliotecas/serviços usa por default para RS256.
const KeySize = 2048

// Key é um par de chaves RSA identificado por um "kid" (key ID) — o kid vai
// no header de todo JWT assinado com esta chave, e é como o Manager sabe
// qual chave pública usar pra validar, mesmo depois de rotações.
type Key struct {
	KID     string
	Private *rsa.PrivateKey
}

// GenerateKey cria um novo par de chaves RSA identificado por kid. Uso
// típico: uma vez no arranque do serviço (ou ao rodar as chaves), guardando
// o resultado (ver MarshalPKCS1PrivateKeyPEM em keys.go) pra persistir.
func GenerateKey(kid string) (Key, error) {
	priv, err := rsa.GenerateKey(rand.Reader, KeySize)
	if err != nil {
		return Key{}, fmt.Errorf("token: gerando chave RSA: %w", err)
	}
	return Key{KID: kid, Private: priv}, nil
}

// Claims são as informações carregadas por um access token. Os nomes de
// campo seguem os claims registados na RFC 7519 (sub/iss/aud/exp/iat) --
// qualquer biblioteca JWT de qualquer linguagem os reconhece.
type Claims struct {
	Subject   string `json:"sub"`
	Issuer    string `json:"iss,omitempty"`
	Audience  string `json:"aud,omitempty"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
}

// ErrInvalidToken é a causa raiz de todo erro de validação de token
// (assinatura errada, expirado, chave desconhecida, formato inválido) --
// permite ao chamador fazer errors.Is(err, ErrInvalidToken) sem se importar
// com o motivo exato, enquanto a mensagem encadeada (%w) ainda carrega o
// detalhe pra log/debug.
var ErrInvalidToken = errors.New("token: token inválido")

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// Manager assina tokens com uma chave "atual" (signing) e valida tokens
// assinados com qualquer chave que conheça (a atual e as anteriores ainda
// não descartadas) -- é isso que permite rodar a chave de assinatura sem
// invalidar instantaneamente tokens já emitidos com a anterior.
type Manager struct {
	issuer   string
	audience string
	ttl      time.Duration
	signing  Key
	verify   map[string]*rsa.PublicKey
}

// NewManager cria um Manager que assina com signing e também aceita
// tokens assinados por qualquer chave em previous (tipicamente chaves
// rodadas recentemente, mantidas só até o último token emitido com elas
// expirar). issuer/audience vão em todo token emitido e são conferidos na
// validação; ttl é por quanto tempo um access token emitido por Sign é
// válido.
func NewManager(issuer, audience string, ttl time.Duration, signing Key, previous ...Key) *Manager {
	verify := make(map[string]*rsa.PublicKey, len(previous)+1)
	verify[signing.KID] = &signing.Private.PublicKey
	for _, k := range previous {
		verify[k.KID] = &k.Private.PublicKey
	}
	return &Manager{issuer: issuer, audience: audience, ttl: ttl, signing: signing, verify: verify}
}

// Sign emite um novo access token para subject (tipicamente o ID do
// utilizador), com issuer/audience/exp/iat preenchidos automaticamente.
func (m *Manager) Sign(subject string) (string, error) {
	now := time.Now()
	claims := Claims{
		Subject:   subject,
		Issuer:    m.issuer,
		Audience:  m.audience,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(m.ttl).Unix(),
	}
	return m.sign(claims)
}

func (m *Manager) sign(claims Claims) (string, error) {
	h := header{Alg: "RS256", Typ: "JWT", Kid: m.signing.KID}
	headerJSON, err := json.Marshal(h)
	if err != nil {
		return "", fmt.Errorf("token: codificando header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("token: codificando claims: %w", err)
	}

	signingInput := encodeSegment(headerJSON) + "." + encodeSegment(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.signing.Private, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("token: assinando: %w", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify valida a assinatura, o algoritmo, a chave (kid), o issuer/audience
// e a validade temporal (exp) de um token, devolvendo os claims se tudo
// bater. Qualquer falha devolve um erro que satisfaz errors.Is(err,
// ErrInvalidToken).
func (m *Manager) Verify(tokenString string) (Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return Claims{}, fmt.Errorf("%w: formato inesperado (esperava 3 segmentos)", ErrInvalidToken)
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

	pub, ok := m.verify[h.Kid]
	if !ok {
		return Claims{}, fmt.Errorf("%w: chave desconhecida: %q", ErrInvalidToken, h.Kid)
	}

	sig, err := decodeSegment(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: assinatura não é base64url válida: %v", ErrInvalidToken, err)
	}
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
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

	if claims.Issuer != m.issuer {
		return Claims{}, fmt.Errorf("%w: issuer inesperado: %q", ErrInvalidToken, claims.Issuer)
	}
	if claims.Audience != m.audience {
		return Claims{}, fmt.Errorf("%w: audience inesperada: %q", ErrInvalidToken, claims.Audience)
	}
	if time.Now().Unix() >= claims.ExpiresAt {
		return Claims{}, fmt.Errorf("%w: expirado", ErrInvalidToken)
	}

	return claims, nil
}

func encodeSegment(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeSegment(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// JWK é a representação de uma chave pública RSA no formato do RFC 7517,
// o que qualquer biblioteca JWT/JWKS de qualquer stack sabe consumir.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKSDocument é o corpo servido em /.well-known/jwks.json.
type JWKSDocument struct {
	Keys []JWK `json:"keys"`
}

// JWKS devolve as chaves públicas de todas as chaves que este Manager
// conhece (a de assinatura atual e as anteriores ainda aceites em Verify) —
// é isto que permite qualquer serviço validar tokens sem partilhar segredo
// nenhum com o auth service.
func (m *Manager) JWKS() JWKSDocument {
	doc := JWKSDocument{Keys: make([]JWK, 0, len(m.verify))}
	for kid, pub := range m.verify {
		doc.Keys = append(doc.Keys, JWK{
			Kty: "RSA",
			Use: "sig",
			Alg: "RS256",
			Kid: kid,
			N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		})
	}
	return doc
}
