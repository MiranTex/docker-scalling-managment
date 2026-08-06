// Package crypto cifra/decifra valores de segredos com AES-256-GCM antes
// de irem para a base de dados -- o PRIMEIRO uso de cifra simétrica neste
// repositório (a chave RSA do auth service assina JWT, não cifra nada; ver
// services/auth/internal/keystore, que é deliberadamente plaintext-no-disco,
// só protegido por permissões de ficheiro). Isto introduz um padrão novo,
// não estende um já existente.
//
// A chave (SECRETS_MASTER_KEY, 32 bytes em base64 -- AES-256) vem de uma
// variável de ambiente, como toda a configuração deste repo. Não há
// rotação de chave nem KMS: perder a chave torna todos os segredos
// já gravados irrecuperáveis; trocar a chave sem migrar os valores
// existentes torna-os todos ilegíveis. Aceitável para este MVP (mesmo
// espírito de "documentar a limitação em vez de resolver rotação agora"
// já usado noutros sítios deste repo, ex: services/database/dbadmin/internal/audit).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

var ErrInvalidKey = errors.New("crypto: SECRETS_MASTER_KEY inválida (esperado 32 bytes em base64, AES-256)")

// Sealer cifra/decifra com uma chave AES-256-GCM fixa.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer constrói um Sealer a partir da chave em base64 (ver
// SECRETS_MASTER_KEY). Falha se a chave não decodificar para exatamente
// 32 bytes.
func NewSealer(masterKeyBase64 string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(masterKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: tem %d bytes, precisa de 32", ErrInvalidKey, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: construindo cifra AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: construindo GCM: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// Seal cifra plaintext, devolvendo (ciphertext, nonce) -- o nonce é gerado
// aqui, aleatório, um por chamada (nunca reutilizado com a mesma chave,
// requisito de segurança do GCM), e precisa ser guardado junto ao
// ciphertext para permitir decifrar depois.
func (s *Sealer) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: gerando nonce: %w", err)
	}
	ciphertext = s.aead.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Open decifra ciphertext com o nonce correspondente (o mesmo devolvido
// por Seal na altura). Falha (autenticação GCM) se ciphertext/nonce
// tiverem sido alterados ou não corresponderem à chave atual.
func (s *Sealer) Open(ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decifrando: %w", err)
	}
	return plaintext, nil
}
