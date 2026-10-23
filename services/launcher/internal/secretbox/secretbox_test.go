package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func newKey(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func TestRoundTrip(t *testing.T) {
	box, err := New(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := box.Seal([]byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := box.Open(ct, nonce)
	if err != nil || !bytes.Equal(got, []byte("s3cret")) {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestWrongKeyFails(t *testing.T) {
	a, _ := New(newKey(t))
	b, _ := New(newKey(t))
	ct, nonce, _ := a.Seal([]byte("x"))
	if _, err := b.Open(ct, nonce); err == nil {
		t.Fatal("abrir com outra chave devia falhar")
	}
}

func TestInvalidKey(t *testing.T) {
	if _, err := New("curta"); err == nil {
		t.Fatal("chave inválida devia falhar")
	}
}
