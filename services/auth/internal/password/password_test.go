package password

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	if err := Verify("correct horse battery staple", hash); err != nil {
		t.Fatalf("Verify da senha certa falhou: %v", err)
	}
}

func TestVerifyWrongPassword(t *testing.T) {
	hash, err := Hash("senha-certa")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	if err := Verify("senha-errada", hash); err != ErrMismatch {
		t.Fatalf("esperava ErrMismatch, got %v", err)
	}
}

func TestHashIsSaltedDifferently(t *testing.T) {
	h1, err := Hash("mesma-senha")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	h2, err := Hash("mesma-senha")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	if h1 == h2 {
		t.Fatal("dois hashes da mesma senha vieram idênticos -- salt não está a variar")
	}

	// Mas ambos devem continuar a verificar contra a senha original.
	if err := Verify("mesma-senha", h1); err != nil {
		t.Fatalf("Verify h1: %v", err)
	}
	if err := Verify("mesma-senha", h2); err != nil {
		t.Fatalf("Verify h2: %v", err)
	}
}

func TestVerifyCorruptedHash(t *testing.T) {
	cases := map[string]string{
		"vazio":               "",
		"sem campos":          "not-a-hash",
		"algoritmo errado":    "$bcrypt$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"versão não numérica": "$argon2id$v=abc$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"parâmetros faltando": "$argon2id$v=19$m=1$c2FsdA$aGFzaA",
		"salt não é base64":   "$argon2id$v=19$m=65536,t=2,p=4$!!!$aGFzaA",
		"key não é base64":    "$argon2id$v=19$m=65536,t=2,p=4$c2FsdA$!!!",
	}

	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Verify("qualquer-senha", h); err == nil {
				t.Fatal("esperava erro, obteve nil")
			} else if err == ErrMismatch {
				t.Fatal("hash corrompido não devia dar ErrMismatch, e sim um erro de formato")
			}
		})
	}
}

func TestHashFormat(t *testing.T) {
	hash, err := Hash("x")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=2,p=4$") {
		t.Fatalf("formato inesperado: %s", hash)
	}
}
