package token

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// EncodePrivateKeyPEM serializa a chave privada de k em PKCS#1 PEM, pronta
// pra escrever num ficheiro/secret. Sem isto, cada restart do serviço geraria
// uma chave nova e invalidaria instantaneamente todo token/JWKS já emitido.
func EncodePrivateKeyPEM(k Key) []byte {
	der := x509.MarshalPKCS1PrivateKey(k.Private)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return pem.EncodeToMemory(block)
}

// DecodePrivateKeyPEM lê de volta uma chave gerada por EncodePrivateKeyPEM,
// associando o kid informado (o kid não faz parte do PEM em si).
func DecodePrivateKeyPEM(kid string, pemBytes []byte) (Key, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return Key{}, fmt.Errorf("token: PEM inválido para a chave %q", kid)
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return Key{}, fmt.Errorf("token: parseando chave privada %q: %w", kid, err)
	}
	return Key{KID: kid, Private: priv}, nil
}

// PublicKey devolve a chave pública correspondente, útil pra quem só
// precisa validar (nunca assinar).
func (k Key) PublicKey() *rsa.PublicKey {
	return &k.Private.PublicKey
}
