package keystore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"auth/internal/token"
)

func TestLoadOrGenerateCreatesThenReuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing-key.json")

	first, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("LoadOrGenerate (gerar): %v", err)
	}
	if first.KID == "" {
		t.Fatal("chave gerada sem kid")
	}

	second, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("LoadOrGenerate (reutilizar): %v", err)
	}
	if second.KID != first.KID {
		t.Fatalf("kid mudou entre chamadas: %q != %q", first.KID, second.KID)
	}

	// Prova mais forte que "os KIDs batem": um token assinado com a chave
	// da primeira chamada tem que validar com a chave devolvida na
	// segunda -- confirma que é literalmente a mesma chave privada, não só
	// o mesmo rótulo.
	m1 := token.NewManager("iss", "aud", time.Minute, first)
	m2 := token.NewManager("iss", "aud", time.Minute, second)

	tok, err := m1.Sign("user-1", "user")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := m2.Verify(tok); err != nil {
		t.Fatalf("token assinado com a chave original não validou com a chave recarregada: %v", err)
	}
}

func TestLoadOrGenerateRejectsCorruptedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing-key.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := LoadOrGenerate(path); err == nil {
		t.Fatal("esperava erro pra ficheiro corrompido")
	}
}
