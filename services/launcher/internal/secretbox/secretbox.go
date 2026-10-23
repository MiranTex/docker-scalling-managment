// Package secretbox cifra segredos de ambiente em repouso com AES-256-GCM
// -- mesma construção de services/secretsadmin/internal/crypto, mas com a
// chave do launcher (LAUNCHER_SECRETS_KEY): são cofres separados, e perder
// uma chave não compromete o outro.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

var ErrInvalidKey = errors.New("secretbox: LAUNCHER_SECRETS_KEY inválida (esperado 32 bytes em base64, AES-256)")

type Box struct {
	aead cipher.AEAD
}

// New recebe a chave em base64 (`openssl rand -base64 32`).
func New(keyBase64 string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: tem %d bytes, precisa de 32", ErrInvalidKey, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: construindo cifra AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: construindo GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal devolve o cifrado e um nonce aleatório novo -- nunca reutilizado.
func (b *Box) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("secretbox: gerando nonce: %w", err)
	}
	return b.aead.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func (b *Box) Open(ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secretbox: decifrando: %w", err)
	}
	return plaintext, nil
}
