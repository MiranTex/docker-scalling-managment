package opaquetoken

import "testing"

func TestNewIsUniqueAndRightLength(t *testing.T) {
	a, err := New(32)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := New(32)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == b {
		t.Fatal("dois New(32) devolveram o mesmo segredo")
	}
}

func TestHashIsDeterministicAndDistinguishesInputs(t *testing.T) {
	h1 := Hash("segredo-a")
	h2 := Hash("segredo-a")
	h3 := Hash("segredo-b")

	if h1 != h2 {
		t.Fatal("Hash não é determinístico para a mesma entrada")
	}
	if h1 == h3 {
		t.Fatal("Hash de segredos diferentes colidiu")
	}
}
