// Package opaquetoken gera e faz hash de segredos opacos (não-JWT) — a
// mesma necessidade aparece em refresh tokens e em API keys: um segredo
// aleatório é entregue ao cliente uma única vez, e só o seu hash SHA-256 é
// persistido, nunca o valor em si (igual a como senhas nunca são
// guardadas em texto plano).
package opaquetoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// New gera um segredo aleatório de n bytes, codificado em base64url.
func New(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("opaquetoken: gerando segredo: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Hash devolve o SHA-256 do segredo, em hex — é isto (nunca o segredo em
// si) que deve ser persistido e usado para buscar o token de volta.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
