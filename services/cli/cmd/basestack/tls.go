package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// A instalação tem a sua própria CA (tls/ca.crt): assina o certificado
// HTTPS do registry (o docker recusa registries sem HTTPS fora de
// localhost). Cada máquina que usa o registry tem de confiar nela: no Mac
// em ~/.docker/certs.d (trustCA), no Linux em /etc/docker/certs.d e nas
// CAs do sistema (internal/trust). Com um domínio público e ACME, o
// Let's Encrypt serve o registry e a CA deixa de ser precisa.
//
// tls/token.{key,crt} assinam os tokens do registry (o launcher assina, o
// registry verifica). tls/traefik/ tem só o que o Traefik serve.

type tlsFiles struct {
	// RegistryCert/Key: nomes dentro de tls/traefik/, com o hash do
	// certificado -- mudar de certificado muda o nome, e isso faz o
	// launcher reescrever o routing e o Traefik recarregá-lo.
	RegistryCert, RegistryKey string
	// StorageCert/Key: o HTTPS do storage da plataforma (tls/storage/),
	// também com o hash -- um certificado novo muda a montagem no compose
	// e o container é recriado com ele.
	StorageCert, StorageKey string
}

// keyMode: as chaves privadas só para o dono. Os containers que as leem
// (launcher, Traefik, storage) correm como root, que as lê na mesma.
const keyMode = 0o600

// tightenKeys põe a keyMode as chaves de tls/ -- as instalações antigas
// tinham algumas a 0644.
func tightenKeys(tdir string) error {
	return filepath.WalkDir(tdir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".key" {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm() != keyMode {
			return os.Chmod(path, keyMode)
		}
		return nil
	})
}

func ensureTLS(dir string, s Settings) (tlsFiles, error) {
	tdir := filepath.Join(dir, "tls")
	if err := os.MkdirAll(filepath.Join(tdir, "traefik"), 0o755); err != nil {
		return tlsFiles{}, err
	}
	caCert, caKey, err := loadOrCreateCA(tdir)
	if err != nil {
		return tlsFiles{}, err
	}
	if err := ensureTokenKey(tdir); err != nil {
		return tlsFiles{}, err
	}
	files, err := ensureRegistryCert(tdir, []string{s.RegistryHost(), "api." + s.Domain}, caCert, caKey)
	if err != nil {
		return tlsFiles{}, err
	}
	files.StorageCert, files.StorageKey, err = ensureServerCert(tdir, "storage", []string{"storage", "basestack-storage"}, caCert, caKey)
	if err != nil {
		return files, err
	}
	return files, tightenKeys(tdir)
}

// ensureServerCert emite (ou reaproveita) um certificado da CA da
// instalação para um serviço interno (tls/<nome>.crt) e copia-o para
// tls/<nome>/ com o hash no nome. Renova-se como o do registry.
func ensureServerCert(tdir, name string, hosts []string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) (cert, key string, err error) {
	certPath, keyPath := filepath.Join(tdir, name+".crt"), filepath.Join(tdir, name+".key")
	der, err := readPEM(certPath)
	if c, perr := x509.ParseCertificate(der); err != nil || perr != nil || !coversAll(c, hosts) || time.Until(c.NotAfter) < 30*24*time.Hour {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return "", "", err
		}
		tmpl := &x509.Certificate{
			SerialNumber: serial(), Subject: pkix.Name{CommonName: hosts[0]}, DNSNames: hosts,
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(2, 0, 0),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		if der, err = x509.CreateCertificate(rand.Reader, tmpl, ca, &k.PublicKey, caKey); err != nil {
			return "", "", err
		}
		kder, _ := x509.MarshalECPrivateKey(k)
		if err := writePEM(keyPath, "EC PRIVATE KEY", kder, 0o600); err != nil {
			return "", "", err
		}
		if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
			return "", "", err
		}
	}
	keyDER, err := readPEM(keyPath)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(der)
	tag := hex.EncodeToString(sum[:6])
	cert, key = name+"-"+tag+".crt", name+"-"+tag+".key"
	dir := filepath.Join(tdir, name)
	old, _ := filepath.Glob(filepath.Join(dir, name+"-*"))
	for _, o := range old {
		if b := filepath.Base(o); b != cert && b != key {
			_ = os.Remove(o)
		}
	}
	if err := writePEM(filepath.Join(dir, cert), "CERTIFICATE", der, 0o644); err != nil {
		return "", "", err
	}
	return cert, key, writePEM(filepath.Join(dir, key), "EC PRIVATE KEY", keyDER, keyMode)
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	return writeFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode)
}

func readPEM(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		return nil, fmt.Errorf("%s não está em PEM", path)
	}
	return b.Bytes, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}

func loadOrCreateCA(tdir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath, keyPath := filepath.Join(tdir, "ca.crt"), filepath.Join(tdir, "ca.key")
	if der, err := readPEM(certPath); err == nil {
		kder, err := readPEM(keyPath)
		if err != nil {
			return nil, nil, err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, nil, err
		}
		key, err := x509.ParseECPrivateKey(kder)
		return cert, key, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "base-stack CA (" + host + ")"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	if err := writePEM(keyPath, "EC PRIVATE KEY", kder, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

// ensureTokenKey: RSA (o registry verifica RS256) e um certificado
// auto-assinado, que é o que o registry tem como confiável.
func ensureTokenKey(tdir string) error {
	keyPath, certPath := filepath.Join(tdir, "token.key"), filepath.Join(tdir, "token.crt")
	if _, err := os.Stat(certPath); err == nil {
		return nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "base-stack registry tokens"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kder, _ := x509.MarshalPKCS8PrivateKey(key)
	if err := writePEM(keyPath, "PRIVATE KEY", kder, keyMode); err != nil {
		return err
	}
	return writePEM(certPath, "CERTIFICATE", der, 0o644)
}

// ensureRegistryCert emite (ou reaproveita) o certificado HTTPS da
// instalação: registry.<domínio> e api.<domínio> (por onde os agentes dos
// nós se ligam). Renova-se 30 dias antes de expirar, ou se falta um nome.
func ensureRegistryCert(tdir string, hosts []string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) (tlsFiles, error) {
	current := filepath.Join(tdir, "registry.crt")
	if der, err := readPEM(current); err == nil {
		if cert, err := x509.ParseCertificate(der); err == nil && coversAll(cert, hosts) && time.Until(cert.NotAfter) > 30*24*time.Hour {
			return registryFiles(tdir, der)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tlsFiles{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: hosts[0]}, DNSNames: hosts,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(2, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return tlsFiles{}, err
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	if err := writePEM(filepath.Join(tdir, "registry.key"), "EC PRIVATE KEY", kder, 0o600); err != nil {
		return tlsFiles{}, err
	}
	if err := writePEM(current, "CERTIFICATE", der, 0o644); err != nil {
		return tlsFiles{}, err
	}
	return registryFiles(tdir, der)
}

func coversAll(cert *x509.Certificate, hosts []string) bool {
	for _, h := range hosts {
		if cert.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

// registryFiles copia o par atual para tls/traefik/ com o hash no nome e
// apaga as versões antigas.
func registryFiles(tdir string, der []byte) (tlsFiles, error) {
	sum := sha256.Sum256(der)
	tag := hex.EncodeToString(sum[:6])
	f := tlsFiles{RegistryCert: "registry-" + tag + ".crt", RegistryKey: "registry-" + tag + ".key"}
	keyDER, err := readPEM(filepath.Join(tdir, "registry.key"))
	if err != nil {
		return tlsFiles{}, err
	}
	old, _ := filepath.Glob(filepath.Join(tdir, "traefik", "registry-*"))
	for _, o := range old {
		if b := filepath.Base(o); b != f.RegistryCert && b != f.RegistryKey {
			_ = os.Remove(o)
		}
	}
	if err := writePEM(filepath.Join(tdir, "traefik", f.RegistryCert), "CERTIFICATE", der, 0o644); err != nil {
		return tlsFiles{}, err
	}
	return f, writePEM(filepath.Join(tdir, "traefik", f.RegistryKey), "EC PRIVATE KEY", keyDER, keyMode)
}

// trustCA (Mac) põe a CA em ~/.docker/certs.d/<host>/ca.crt -- sem isto,
// docker push/pull recusa o HTTPS do registry. O Docker Desktop só a copia
// para a sua VM ao arrancar: changed diz que é preciso reiniciá-lo uma vez.
// No Linux é o internal/trust.
func trustCA(dir, host string) (path string, changed bool, err error) {
	ca, err := os.ReadFile(filepath.Join(dir, "tls", "ca.crt"))
	if err != nil {
		return "", false, err
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	path = filepath.Join(h, ".docker", "certs.d", host, "ca.crt")
	if current, err := os.ReadFile(path); err == nil && string(current) == string(ca) {
		return path, false, nil
	}
	return path, true, writeFile(path, ca, 0o644)
}
