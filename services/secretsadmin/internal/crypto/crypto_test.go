package crypto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
}

func TestSealOpenRoundTrip(t *testing.T) {
	s, err := NewSealer(testKey())
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	plaintext := []byte("postgres://auth:auth@database:5432/app")
	ciphertext, nonce, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatalf("ciphertext igual ao plaintext -- não cifrou nada")
	}

	got, err := s.Open(ciphertext, nonce)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Open devolveu %q, want %q", got, plaintext)
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	s, _ := NewSealer(testKey())
	ciphertext, nonce, _ := s.Seal([]byte("valor secreto"))

	tampered := append([]byte{}, ciphertext...)
	tampered[0] ^= 0xFF

	if _, err := s.Open(tampered, nonce); err == nil {
		t.Fatalf("Open aceitou ciphertext adulterado")
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	s1, _ := NewSealer(testKey())
	otherKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x24}, 32))
	s2, _ := NewSealer(otherKey)

	ciphertext, nonce, _ := s1.Seal([]byte("valor secreto"))
	if _, err := s2.Open(ciphertext, nonce); err == nil {
		t.Fatalf("Open com chave errada devolveu sucesso")
	}
}

func TestNewSealerRejectsWrongKeyLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("chave-curta-demais"))
	if _, err := NewSealer(short); err == nil {
		t.Fatalf("NewSealer aceitou chave com tamanho errado")
	}
}

func TestNewSealerRejectsInvalidBase64(t *testing.T) {
	if _, err := NewSealer("isto não é base64 válido!!"); err == nil {
		t.Fatalf("NewSealer aceitou base64 inválido")
	}
}
