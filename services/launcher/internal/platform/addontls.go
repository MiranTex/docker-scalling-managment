package platform

import (
	"archive/tar"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"
)

// TLS dos addons Postgres: muitas apps recusam, em produção, ligar-se à
// base sem TLS (ex: "DATABASE_URL must set sslmode to require"). Cada
// container ganha um certificado próprio, auto-assinado e novo a cada
// criação -- a ligação vai cifrada (sslmode=require); verificar o
// certificado (verify-ca/verify-full) exigiria dar uma CA às apps.

const (
	// LabelAddonTLS marca os containers Postgres que já arrancam com TLS --
	// o reconciliador recria os de antes.
	LabelAddonTLS = "base-stack.addon-tls"
	// addonTLSDir é onde o certificado entra no container (o tar extrai
	// bs-tls/ em /etc, antes de arrancar); o arranque copia-o para o dono
	// certo.
	addonTLSDir = "/etc/bs-tls"
)

// postgresTLSCmd arranca o Postgres com TLS. Corre como root antes do
// entrypoint da imagem: o Postgres só aceita a chave se for do utilizador
// dele com 0600, e esse uid muda entre imagens (alpine 70, debian 999).
var postgresTLSCmd = []string{"sh", "-c", `set -e
mkdir -p /var/lib/postgresql-tls
install -o postgres -g postgres -m 600 ` + addonTLSDir + `/server.key /var/lib/postgresql-tls/server.key
install -o postgres -g postgres -m 644 ` + addonTLSDir + `/server.crt /var/lib/postgresql-tls/server.crt
exec docker-entrypoint.sh postgres -c ssl=on \
  -c ssl_cert_file=/var/lib/postgresql-tls/server.crt -c ssl_key_file=/var/lib/postgresql-tls/server.key`}

// addonTLSArchive gera um certificado para host e devolve-o em tar
// (bs-tls/server.crt e bs-tls/server.key), para extrair em /etc.
func addonTLSArchive(host string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "bs-tls/", Typeflag: tar.TypeDir, Mode: 0o700, ModTime: time.Now()}); err != nil {
		return nil, err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"server.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})},
		{"server.key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: "bs-tls/" + f.name, Mode: 0o600, Size: int64(len(f.data)), ModTime: time.Now()}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
