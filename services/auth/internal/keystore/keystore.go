// Package keystore persiste a chave RSA de assinatura de tokens num
// ficheiro local, pra sobreviver a restarts do processo. Sem isto, cada
// restart geraria uma chave nova (token.GenerateKey), o que invalidaria
// instantaneamente todo access token e toda a JWKS já publicada — pior
// ainda, cada instância de um deployment com várias réplicas teria uma
// chave diferente da outra.
//
// Fase 1 guarda só a chave atual num ficheiro; rotação de chaves (manter
// várias, aposentar as antigas aos poucos) fica pra uma fase futura, com
// suporte próprio de storage (tabela signing_keys), quando isso for
// realmente preciso.
package keystore

import (
	"encoding/json"
	"fmt"
	"os"

	"auth/internal/idgen"
	"auth/internal/token"
)

type keyFile struct {
	KID           string `json:"kid"`
	PrivateKeyPEM string `json:"privateKeyPem"`
}

// LoadOrGenerate lê a chave de assinatura persistida em path. Se o
// ficheiro não existir, gera uma chave nova, persiste em path (permissão
// 0600 -- é uma chave privada) e devolve essa.
func LoadOrGenerate(path string) (token.Key, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		return decode(raw)
	}
	if !os.IsNotExist(err) {
		return token.Key{}, fmt.Errorf("keystore: lendo %s: %w", path, err)
	}

	key, err := generate()
	if err != nil {
		return token.Key{}, err
	}
	if err := persist(path, key); err != nil {
		return token.Key{}, err
	}
	return key, nil
}

func generate() (token.Key, error) {
	kid, err := idgen.New()
	if err != nil {
		return token.Key{}, fmt.Errorf("keystore: gerando kid: %w", err)
	}
	key, err := token.GenerateKey(kid)
	if err != nil {
		return token.Key{}, fmt.Errorf("keystore: gerando chave: %w", err)
	}
	return key, nil
}

func persist(path string, key token.Key) error {
	kf := keyFile{KID: key.KID, PrivateKeyPEM: string(token.EncodePrivateKeyPEM(key))}
	raw, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return fmt.Errorf("keystore: codificando chave: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("keystore: escrevendo %s: %w", path, err)
	}
	return nil
}

func decode(raw []byte) (token.Key, error) {
	var kf keyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		return token.Key{}, fmt.Errorf("keystore: ficheiro de chave corrompido: %w", err)
	}
	return token.DecodePrivateKeyPEM(kf.KID, []byte(kf.PrivateKeyPEM))
}
