// Package mfa é o segundo fator do login: códigos TOTP (RFC 6238, os das
// apps autenticadoras -- 6 dígitos a cada 30 segundos), o segredo de cada
// conta cifrado em repouso, e códigos de recuperação de uso único.
package mfa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	period = 30 * time.Second
	digits = 6
	// skew: aceita o passo anterior e o seguinte (relógios desacertados
	// até ~30s, e quem escreve o código no limite do passo).
	skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret gera um segredo TOTP (160 bits, como recomenda a RFC 4226),
// em base32 -- o formato que as apps leem.
func NewSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return b32.EncodeToString(raw), nil
}

// URI é o otpauth:// que o código QR leva (issuer e conta aparecem na app).
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(digits)}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// step é o número do passo de 30s em que t cai.
func step(t time.Time) int64 { return t.Unix() / int64(period/time.Second) }

func code(secret []byte, s int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(s))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", digits, n%1000000)
}

// Code é o código de 6 dígitos do segredo no instante t.
func Code(secret string, t time.Time) (string, error) {
	raw, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	return code(raw, step(t)), nil
}

// Verify confere um código e devolve o passo em que bateu. Um passo igual
// ou anterior a lastStep já foi usado: recusa -- cada código serve uma vez
// (quem o viu por cima do ombro não o reaproveita).
func Verify(secret, input string, now time.Time, lastStep int64) (int64, bool) {
	raw, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	input = strings.ReplaceAll(strings.TrimSpace(input), " ", "")
	if len(input) != digits {
		return 0, false
	}
	cur := step(now)
	for s := cur - skew; s <= cur+skew; s++ {
		if s <= lastStep {
			continue
		}
		if hmac.Equal([]byte(code(raw, s)), []byte(input)) {
			return s, true
		}
	}
	return 0, false
}
