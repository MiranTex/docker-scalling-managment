// Package password faz hash e verificação de senhas com Argon2id — a KDF
// recomendada hoje pela OWASP pra armazenar senhas (ganhou o Password
// Hashing Competition; resiste melhor a ataques com GPU/ASIC que bcrypt
// porque também é custosa em memória, não só em CPU).
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// params define o custo do Argon2id. Valores seguem a recomendação da OWASP
// pra Argon2id com um único thread (memory=19MiB, iterations=2) ajustada
// pra um pouco mais de memória (64MiB) já que este serviço não compete por
// RAM com mais nada relevante — mais memória = mais caro pra quem ataca
// offline com hardware paralelo.
type params struct {
	memoryKiB  uint32
	iterations uint32
	threads    uint8
	saltLen    uint32
	keyLen     uint32
}

var defaultParams = params{
	memoryKiB:  64 * 1024, // 64 MiB
	iterations: 2,
	threads:    4,
	saltLen:    16,
	keyLen:     32,
}

// ErrMismatch é devolvido por Verify quando a senha não bate com o hash.
var ErrMismatch = errors.New("password: senha não confere")

// Hash gera um novo hash Argon2id para a senha em texto plano. O resultado
// é uma string autocontida (formato inspirado no do próprio Argon2 de
// referência) que inclui os parâmetros de custo e o salt, então basta
// guardar essa string inteira — Verify não precisa de mais nada.
func Hash(plain string) (string, error) {
	salt := make([]byte, defaultParams.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: gerando salt: %w", err)
	}

	key := argon2.IDKey([]byte(plain), salt, defaultParams.iterations, defaultParams.memoryKiB, defaultParams.threads, defaultParams.keyLen)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		defaultParams.memoryKiB,
		defaultParams.iterations,
		defaultParams.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// Verify confere se plain corresponde ao hash gerado por Hash. Devolve
// ErrMismatch se a senha estiver errada, ou outro erro se o hash estiver
// corrompido/em formato inesperado (ex: veio de outro algoritmo).
func Verify(plain, encoded string) error {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return err
	}

	candidate := argon2.IDKey([]byte(plain), salt, p.iterations, p.memoryKiB, p.threads, uint32(len(key)))

	// subtle.ConstantTimeCompare evita que o tempo de resposta vaze
	// informação sobre quantos bytes do hash batem -- uma comparação byte a
	// byte com "==" pararia no primeiro byte diferente, dando a um atacante
	// um oráculo de timing pra adivinhar o hash certo byte a byte.
	if subtle.ConstantTimeCompare(candidate, key) != 1 {
		return ErrMismatch
	}
	return nil
}

func decode(encoded string) (params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// parts[0] é "" (string começa com "$"); layout esperado:
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<key>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return params{}, nil, nil, errors.New("password: formato de hash inválido")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return params{}, nil, nil, fmt.Errorf("password: versão inválida: %w", err)
	}
	if version != argon2.Version {
		return params{}, nil, nil, fmt.Errorf("password: versão do argon2 incompatível: %d", version)
	}

	var p params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memoryKiB, &p.iterations, &p.threads); err != nil {
		return params{}, nil, nil, fmt.Errorf("password: parâmetros inválidos: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return params{}, nil, nil, fmt.Errorf("password: salt inválido: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return params{}, nil, nil, fmt.Errorf("password: hash inválido: %w", err)
	}

	return p, salt, key, nil
}
