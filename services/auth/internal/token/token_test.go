package token

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func mustKey(t *testing.T, kid string) Key {
	t.Helper()
	k, err := GenerateKey(kid)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return k
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	key := mustKey(t, "k1")
	m := NewManager("auth.example", "example-api", time.Minute, key)

	tok, err := m.Sign("user-123")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	claims, err := m.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-123" {
		t.Fatalf("subject = %q, want %q", claims.Subject, "user-123")
	}
	if claims.Issuer != "auth.example" || claims.Audience != "example-api" {
		t.Fatalf("issuer/audience inesperados: %+v", claims)
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	key := mustKey(t, "k1")
	m := NewManager("auth.example", "example-api", time.Minute, key)

	tok, err := m.Sign("user-123")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	parts := strings.Split(tok, ".")
	// Troca o payload por outro (ainda base64url válido, JSON válido) sem
	// re-assinar -- simula um atacante tentando trocar o "sub" do token.
	forgedClaims := encodeSegment([]byte(`{"sub":"admin","iss":"auth.example","aud":"example-api","exp":9999999999,"iat":0}`))
	forged := parts[0] + "." + forgedClaims + "." + parts[2]

	if _, err := m.Verify(forged); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperava ErrInvalidToken pra payload adulterado, got %v", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	signingKey := mustKey(t, "k1")
	attackerKey := mustKey(t, "k1") // mesmo kid, chave privada diferente

	legit := NewManager("auth.example", "example-api", time.Minute, signingKey)
	attacker := NewManager("auth.example", "example-api", time.Minute, attackerKey)

	forged, err := attacker.Sign("user-123")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := legit.Verify(forged); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperava ErrInvalidToken pra token assinado com outra chave privada, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	key := mustKey(t, "k1")
	m := NewManager("auth.example", "example-api", -time.Second, key) // já nasce expirado

	tok, err := m.Sign("user-123")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := m.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperava ErrInvalidToken pra token expirado, got %v", err)
	}
}

func TestVerifyRejectsWrongAudienceAndIssuer(t *testing.T) {
	key := mustKey(t, "k1")
	issuerA := NewManager("issuer-a", "aud-a", time.Minute, key)
	issuerB := NewManager("issuer-b", "aud-a", time.Minute, key)
	audB := NewManager("issuer-a", "aud-b", time.Minute, key)

	tok, err := issuerA.Sign("user-123")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := issuerB.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperava rejeitar issuer diferente, got %v", err)
	}
	if _, err := audB.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperava rejeitar audience diferente, got %v", err)
	}
}

func TestVerifyRejectsUnknownKid(t *testing.T) {
	key := mustKey(t, "k1")
	other := mustKey(t, "k2")
	signer := NewManager("auth.example", "example-api", time.Minute, key)
	verifier := NewManager("auth.example", "example-api", time.Minute, other)

	tok, err := signer.Sign("user-123")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := verifier.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("esperava ErrInvalidToken pra kid desconhecido, got %v", err)
	}
}

func TestVerifyRejectsMalformedTokens(t *testing.T) {
	key := mustKey(t, "k1")
	m := NewManager("auth.example", "example-api", time.Minute, key)

	cases := []string{
		"",
		"not-a-jwt",
		"only.two-parts",
		"a.b.c.d",
		"!!!.bbbb.cccc",
	}
	for _, tok := range cases {
		if _, err := m.Verify(tok); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("token %q: esperava ErrInvalidToken, got %v", tok, err)
		}
	}
}

func TestKeyRotationAcceptsPreviousKey(t *testing.T) {
	oldKey := mustKey(t, "old")
	newKey := mustKey(t, "new")

	// Simula o momento logo após rodar a chave: quem assina agora usa a
	// nova, mas ainda precisa validar tokens emitidos com a antiga (ainda
	// não expiraram).
	oldManager := NewManager("auth.example", "example-api", time.Minute, oldKey)
	rotated := NewManager("auth.example", "example-api", time.Minute, newKey, oldKey)

	tokFromOld, err := oldManager.Sign("user-123")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := rotated.Verify(tokFromOld); err != nil {
		t.Fatalf("Verify token da chave antiga após rotação: %v", err)
	}

	tokFromNew, err := rotated.Sign("user-456")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := rotated.Verify(tokFromNew)
	if err != nil {
		t.Fatalf("Verify token da chave nova: %v", err)
	}
	if claims.Subject != "user-456" {
		t.Fatalf("subject = %q, want user-456", claims.Subject)
	}
}

func TestJWKSExposesAllKnownKeys(t *testing.T) {
	current := mustKey(t, "current")
	previous := mustKey(t, "previous")
	m := NewManager("auth.example", "example-api", time.Minute, current, previous)

	doc := m.JWKS()
	if len(doc.Keys) != 2 {
		t.Fatalf("esperava 2 chaves na JWKS, got %d", len(doc.Keys))
	}

	kids := map[string]bool{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Alg != "RS256" || k.Use != "sig" {
			t.Fatalf("JWK com campos inesperados: %+v", k)
		}
		if k.N == "" || k.E == "" {
			t.Fatalf("JWK sem modulus/expoente: %+v", k)
		}
		kids[k.Kid] = true
	}
	if !kids["current"] || !kids["previous"] {
		t.Fatalf("JWKS não contém os dois kids esperados: %+v", kids)
	}
}

func TestPrivateKeyPEMRoundTrip(t *testing.T) {
	key := mustKey(t, "k1")
	pemBytes := EncodePrivateKeyPEM(key)

	decoded, err := DecodePrivateKeyPEM("k1", pemBytes)
	if err != nil {
		t.Fatalf("DecodePrivateKeyPEM: %v", err)
	}

	m1 := NewManager("auth.example", "example-api", time.Minute, key)
	m2 := NewManager("auth.example", "example-api", time.Minute, decoded)

	tok, err := m1.Sign("user-123")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := m2.Verify(tok); err != nil {
		t.Fatalf("token assinado com a chave original não validou com a chave decodificada do PEM: %v", err)
	}
}

func TestDecodePrivateKeyPEMRejectsGarbage(t *testing.T) {
	if _, err := DecodePrivateKeyPEM("k1", []byte("not pem at all")); err == nil {
		t.Fatal("esperava erro pra PEM inválido")
	}
}
