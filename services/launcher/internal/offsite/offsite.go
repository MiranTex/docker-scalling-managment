// Package offsite espelha o storage da plataforma (os buckets de backup)
// para um destino fora desta máquina -- qualquer serviço com a API do S3.
// Cada objeto vai cifrado com age para a chave pública da instalação: o
// fornecedor nunca vê os dados, e só a chave privada (que fica com quem
// opera) os abre.
//
// O espelho apaga no destino o que desapareceu localmente (as rotações
// do pgBackRest e dos dumps), mas nunca em massa: se uma passagem fosse
// apagar mais de maxDeleteShare do destino, ou o storage local aparecer
// vazio, não apaga nada -- um storage local apagado ou comprometido não
// leva a cópia de fora atrás.
package offsite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"filippo.io/age"

	"launcher/internal/objstore"
)

// Buckets são os do storage da plataforma que se copiam.
var Buckets = []string{"database", "config", "addon-backups"}

const (
	// Suffix marca os objetos cifrados no destino.
	Suffix = ".age"
	// StatusKey: no destino, a hora da última passagem completa.
	StatusKey = "last-sync"
	// ManifestKey: no destino, a versão local (data e tamanho) de cada
	// objeto copiado -- compara-se cá com cá, nunca com o relógio do
	// fornecedor. Só tem nomes de chaves, que a listagem já mostra.
	ManifestKey = "manifest.json"

	maxDeleteShare = 0.5
	// Até aqui apaga-se sem olhar à proporção (destinos pequenos).
	freeDeletes = 5
)

type Target struct {
	Store  *objstore.Client
	Bucket string
	// Prefix: onde, dentro do bucket, fica esta instalação (ex: basestack/).
	Prefix string
}

// Key é a chave no destino de um objeto local.
func (t Target) Key(bucket, key string) string { return t.Prefix + bucket + "/" + key + Suffix }

type Mirror struct {
	Local     *objstore.Client
	Target    Target
	Recipient age.Recipient
	Logger    *slog.Logger
	// TempDir: onde se cifra cada objeto antes de o enviar (o envio
	// precisa do tamanho final). Vazio = o do sistema.
	TempDir string
}

// Report é o resultado de uma passagem.
type Report struct {
	Copied, Deleted int
	Bytes           int64
	// Held: apagamentos que a proteção travou, por bucket.
	Held map[string]int
}

// Run faz uma passagem logo (depois de first) e depois a cada every.
func (m *Mirror) Run(ctx context.Context, first, every time.Duration) {
	wait := first
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = every
		start := time.Now()
		r, err := m.Once(ctx)
		if err != nil {
			m.Logger.Error("cópia para fora: falhou", "err", err, "copiados", r.Copied)
			continue
		}
		m.Logger.Info("cópia para fora: feita", "copiados", r.Copied, "bytes", r.Bytes, "apagados", r.Deleted,
			"travados", r.Held, "duracao", time.Since(start).Round(time.Second).String())
	}
}

// Once compara os buckets locais com o destino e copia o que falta ou
// mudou. Só marca a passagem como completa (StatusKey) se tudo correu bem.
func (m *Mirror) Once(ctx context.Context) (Report, error) {
	r := Report{Held: map[string]int{}}
	man, err := m.manifest(ctx)
	if err != nil {
		return r, err
	}
	var errs []error
	for _, bucket := range Buckets {
		if err := m.mirrorBucket(ctx, bucket, man, &r); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", bucket, err))
		}
	}
	// O manifesto grava-se mesmo com falhas: guarda o que já foi.
	if err := m.saveManifest(ctx, man); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return r, errors.Join(errs...)
	}
	stamp := []byte(time.Now().UTC().Format(time.RFC3339))
	return r, m.Target.Store.Put(ctx, m.Target.Bucket, m.Target.Prefix+StatusKey, strings.NewReader(string(stamp)), int64(len(stamp)))
}

// manifest: "bucket/chave" -> versão local copiada.
type manifest map[string]string

func version(o objstore.Object) string {
	return o.LastModified.UTC().Format(time.RFC3339Nano) + " " + fmt.Sprint(o.Size)
}

func (m *Mirror) manifest(ctx context.Context) (manifest, error) {
	body, err := m.Target.Store.Get(ctx, m.Target.Bucket, m.Target.Prefix+ManifestKey)
	if errors.Is(err, objstore.ErrNotFound) {
		return manifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lendo o manifesto do destino: %w", err)
	}
	defer body.Close()
	man := manifest{}
	if err := json.NewDecoder(body).Decode(&man); err != nil {
		// Ilegível: recomeça -- copia tudo outra vez, mas não perde nada.
		m.Logger.Warn("cópia para fora: manifesto ilegível, a recomeçar", "err", err)
		return manifest{}, nil
	}
	return man, nil
}

func (m *Mirror) saveManifest(ctx context.Context, man manifest) error {
	raw, _ := json.Marshal(man)
	return m.Target.Store.Put(ctx, m.Target.Bucket, m.Target.Prefix+ManifestKey, bytes.NewReader(raw), int64(len(raw)))
}

// stale: o objeto local não está no destino, ou está noutra versão.
func stale(o objstore.Object, bucket string, remote map[string]objstore.Object, man manifest) bool {
	_, ok := remote[o.Key]
	return !ok || man[bucket+"/"+o.Key] != version(o)
}

func (m *Mirror) mirrorBucket(ctx context.Context, bucket string, man manifest, r *Report) error {
	local, err := m.Local.List(ctx, bucket, "")
	if err != nil {
		return fmt.Errorf("listando o storage local: %w", err)
	}
	remote, err := m.remote(ctx, bucket)
	if err != nil {
		return err
	}
	inLocal := make(map[string]bool, len(local))
	var errs []error
	for _, o := range local {
		inLocal[o.Key] = true
		// Uma versão nova cá (ex: o backup.info do pgBackRest) volta a ir.
		if !stale(o, bucket, remote, man) {
			continue
		}
		n, err := m.copy(ctx, bucket, o)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", o.Key, err))
			continue
		}
		man[bucket+"/"+o.Key] = version(o)
		r.Copied++
		r.Bytes += n
	}

	var gone []string
	for key := range remote {
		if !inLocal[key] {
			gone = append(gone, key)
		}
	}
	switch {
	case len(gone) == 0:
	case len(local) == 0 || len(gone) > freeDeletes && float64(len(gone)) > maxDeleteShare*float64(len(remote)):
		r.Held[bucket] = len(gone)
		m.Logger.Warn("cópia para fora: apagamentos travados -- o storage local perdeu demasiado de uma vez",
			"bucket", bucket, "a_apagar", len(gone), "no_destino", len(remote), "locais", len(local))
	default:
		for _, key := range gone {
			if err := m.Target.Store.Delete(ctx, m.Target.Bucket, m.Target.Key(bucket, key)); err != nil {
				errs = append(errs, fmt.Errorf("apagando %s: %w", key, err))
				continue
			}
			delete(man, bucket+"/"+key)
			r.Deleted++
		}
	}
	return errors.Join(errs...)
}

// remote lista o destino de um bucket, pela chave local (sem prefixo nem
// sufixo).
func (m *Mirror) remote(ctx context.Context, bucket string) (map[string]objstore.Object, error) {
	prefix := m.Target.Prefix + bucket + "/"
	objs, err := m.Target.Store.List(ctx, m.Target.Bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("listando o destino: %w", err)
	}
	out := make(map[string]objstore.Object, len(objs))
	for _, o := range objs {
		key, ok := strings.CutSuffix(strings.TrimPrefix(o.Key, prefix), Suffix)
		if ok {
			out[key] = o
		}
	}
	return out, nil
}

// copy lê um objeto local, cifra-o para um ficheiro temporário (o envio
// precisa do tamanho) e envia-o. Devolve os bytes enviados.
func (m *Mirror) copy(ctx context.Context, bucket string, o objstore.Object) (int64, error) {
	src, err := m.Local.Get(ctx, bucket, o.Key)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(m.TempDir, "offsite-*.age")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	w, err := age.Encrypt(tmp, m.Recipient)
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(w, src); err != nil {
		return 0, fmt.Errorf("lendo do storage local: %w", err)
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := m.Target.Store.Put(ctx, m.Target.Bucket, m.Target.Key(bucket, o.Key), tmp, size); err != nil {
		return 0, err
	}
	return size, nil
}

// Status compara o local com o destino sem copiar nada: quantos objetos
// faltam (ou estão desatualizados) lá, por bucket, e a última passagem
// completa.
type Status struct {
	LastSync time.Time
	Missing  map[string]int
	Remote   map[string]int
}

func (m *Mirror) Status(ctx context.Context) (Status, error) {
	st := Status{Missing: map[string]int{}, Remote: map[string]int{}}
	man, err := m.manifest(ctx)
	if err != nil {
		return st, err
	}
	for _, bucket := range Buckets {
		local, err := m.Local.List(ctx, bucket, "")
		if err != nil {
			return st, fmt.Errorf("%s: listando o storage local: %w", bucket, err)
		}
		remote, err := m.remote(ctx, bucket)
		if err != nil {
			return st, fmt.Errorf("%s: %w", bucket, err)
		}
		st.Remote[bucket] = len(remote)
		for _, o := range local {
			if stale(o, bucket, remote, man) {
				st.Missing[bucket]++
			}
		}
	}
	if body, err := m.Target.Store.Get(ctx, m.Target.Bucket, m.Target.Prefix+StatusKey); err == nil {
		raw, _ := io.ReadAll(io.LimitReader(body, 64))
		body.Close()
		st.LastSync, _ = time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	} else if !errors.Is(err, objstore.ErrNotFound) {
		return st, err
	}
	return st, nil
}
