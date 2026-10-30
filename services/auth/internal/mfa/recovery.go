package mfa

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// RecoveryCodes gera n códigos de recuperação ("xxxx-xxxx-xxxx", 60 bits
// cada) -- mostrados uma vez; guarda-se só o hash.
func RecoveryCodes(n int) ([]string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // sem 0/o, 1/l/i
	out := make([]string, n)
	for i := range out {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		var b strings.Builder
		for j, c := range raw {
			if j > 0 && j%4 == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(alphabet[int(c)%len(alphabet)])
		}
		out[i] = b.String()
	}
	return out, nil
}

// HashRecovery normaliza (maiúsculas, espaços, hífenes) e faz o hash.
func HashRecovery(code string) string {
	c := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	sum := sha256.Sum256([]byte(c))
	return hex.EncodeToString(sum[:])
}
