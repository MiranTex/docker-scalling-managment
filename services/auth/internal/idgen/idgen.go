// Package idgen gera identificadores aleatórios no formato UUIDv4
// (RFC 4122), usando só crypto/rand da stdlib — sem depender de um pacote
// externo só pra isto. Usado em todo o serviço sempre que é preciso um ID
// de linha (users, refresh tokens, chaves) que bata com a coluna Postgres
// do tipo "uuid".
package idgen

import (
	"crypto/rand"
	"fmt"
)

// New gera um novo UUIDv4 aleatório.
func New() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("idgen: lendo bytes aleatórios: %w", err)
	}
	// Bits de versão (4) e variante (RFC 4122) — é o que diferencia um
	// UUIDv4 de 16 bytes aleatórios quaisquer.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
