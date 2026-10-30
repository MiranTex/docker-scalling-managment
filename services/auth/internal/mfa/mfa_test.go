package mfa

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// Os vetores da RFC 6238 (apêndice B, SHA1), truncados a 6 dígitos.
func TestRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{
		59: "287082", 1111111109: "081804", 1111111111: "050471",
		1234567890: "005924", 2000000000: "279037", 20000000000: "353130",
	} {
		got, err := Code(secret, time.Unix(unix, 0))
		if err != nil || got != want {
			t.Errorf("t=%d: %q, queria %q (%v)", unix, got, want, err)
		}
	}
}

func TestVerifyAcceptsSkewAndRejectsReplay(t *testing.T) {
	secret, _ := NewSecret()
	now := time.Now()
	c, _ := Code(secret, now)
	s, ok := Verify(secret, c, now, 0)
	if !ok {
		t.Fatal("o código atual devia passar")
	}
	if _, ok := Verify(secret, c, now, s); ok {
		t.Error("o mesmo código não pode servir duas vezes")
	}
	prev, _ := Code(secret, now.Add(-period))
	if _, ok := Verify(secret, prev, now, 0); !ok {
		t.Error("o código do passo anterior (relógio atrasado) devia passar")
	}
	old, _ := Code(secret, now.Add(-3*period))
	if _, ok := Verify(secret, old, now, 0); ok {
		t.Error("um código de há 90s não pode passar")
	}
	if _, ok := Verify(secret, "12345", now, 0); ok {
		t.Error("5 dígitos")
	}
}

func TestBoxRoundTripAndTamper(t *testing.T) {
	b, err := NewBox("MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := b.Seal("JBSWY3DPEHPK3PXP")
	if got, err := b.Open(sealed); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Open: %q %v", got, err)
	}
	if _, err := b.Open(sealed[:len(sealed)-4] + "AAAA"); err == nil {
		t.Error("um segredo alterado não pode abrir")
	}
	if _, err := NewBox("curta"); err == nil {
		t.Error("chave inválida aceite")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, _ := RecoveryCodes(10)
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 14 || strings.Count(c, "-") != 2 || seen[c] {
			t.Errorf("código %q", c)
		}
		seen[c] = true
	}
	if HashRecovery(codes[0]) != HashRecovery(" "+strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))+" ") {
		t.Error("o hash deve ignorar maiúsculas, hífenes e espaços")
	}
	if !strings.HasPrefix(URI("base-stack", "ana@x.pt", "ABC"), "otpauth://totp/base-stack:ana@x.pt?") {
		t.Errorf("URI = %s", URI("base-stack", "ana@x.pt", "ABC"))
	}
}
