package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"launcher/internal/objstore"
	"launcher/internal/store"
)

// Os ficheiros dentro dos buckets, pelo portal: o launcher fica no meio
// (as credenciais do storage nunca chegam ao browser) e tudo passa em
// streaming.

const (
	// MaxUploadBytes: o limite de um envio pelo portal -- mais do que isso
	// vai pela CLI (bs port-forward) ou pela própria app.
	MaxUploadBytes = 1 << 30
	// signedLinkTTL: quanto dura um link assinado de um bucket privado.
	signedLinkTTL = time.Hour
	maxKeyBytes   = 1024
)

// FileListing é uma pasta de um bucket: as subpastas e os ficheiros.
type FileListing struct {
	Prefix  string     `json:"prefix"`
	Folders []string   `json:"folders"`
	Files   []FileView `json:"files"`
}

type FileView struct {
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified"`
}

// validKey: o nome de um ficheiro no bucket (pode ter pastas: a/b/c.jpg).
func validKey(key string) error {
	switch {
	case key == "" || len(key) > maxKeyBytes || !utf8.ValidString(key):
		return invalid("nome de ficheiro inválido")
	case strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") || strings.Contains(key, "//"):
		return invalid("nome de ficheiro %q inválido -- sem \"/\" no início, no fim ou repetido", key)
	case strings.ContainsAny(key, "\x00\r\n\\"):
		return invalid("nome de ficheiro %q inválido", key)
	}
	for _, part := range strings.Split(key, "/") {
		if part == "." || part == ".." {
			return invalid("nome de ficheiro %q inválido -- sem \".\" nem \"..\"", key)
		}
	}
	return nil
}

// validPrefix: uma pasta ("" é a raiz; as outras acabam em "/").
func validPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if !strings.HasSuffix(prefix, "/") {
		return invalid("pasta %q inválida -- acaba em \"/\"", prefix)
	}
	return validKey(strings.TrimSuffix(prefix, "/"))
}

func (s *Service) bucketClient(ctx context.Context, projectSlug, envSlug, addonSlug, bucket string) (*objstore.Client, store.StorageBucket, error) {
	p, e, a, b, err := s.bucketScope(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return nil, b, err
	}
	c, err := s.storageClient(ctx, p, e, a)
	return c, b, err
}

func (s *Service) ListFiles(ctx context.Context, projectSlug, envSlug, addonSlug, bucket, prefix string) (FileListing, error) {
	if err := validPrefix(prefix); err != nil {
		return FileListing{}, err
	}
	c, b, err := s.bucketClient(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return FileListing{}, err
	}
	dirs, objs, err := c.ListDir(ctx, b.Name, prefix)
	if err != nil {
		return FileListing{}, fmt.Errorf("listando o bucket (o storage está a correr?): %w", err)
	}
	out := FileListing{Prefix: prefix, Folders: []string{}, Files: []FileView{}}
	for _, d := range dirs {
		out.Folders = append(out.Folders, strings.TrimSuffix(strings.TrimPrefix(d, prefix), "/"))
	}
	for _, o := range objs {
		// Um objeto com o nome da própria pasta (a "pasta vazia" de alguns
		// clientes) não é um ficheiro.
		if o.Key == prefix {
			continue
		}
		out.Files = append(out.Files, FileView{Key: o.Key, Name: path.Base(o.Key), Size: o.Size, LastModified: o.LastModified})
	}
	sort.Strings(out.Folders)
	return out, nil
}

func (s *Service) UploadFile(ctx context.Context, projectSlug, envSlug, addonSlug, bucket, key string, body io.Reader, size int64, contentType string) error {
	if err := validKey(key); err != nil {
		return err
	}
	switch {
	case size < 0:
		return invalid("o envio precisa do tamanho do ficheiro (Content-Length)")
	case size > MaxUploadBytes:
		return invalid("o ficheiro tem mais de 1 GB -- envia-o pela CLI (bs port-forward) ou pela app")
	}
	c, b, err := s.bucketClient(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return err
	}
	if err := c.PutObject(ctx, b.Name, key, body, size, contentType); err != nil {
		return fmt.Errorf("enviando para o storage: %w", err)
	}
	return nil
}

// OpenFile devolve o conteúdo de um ficheiro (o download pelo portal);
// quem chama fecha-o.
func (s *Service) OpenFile(ctx context.Context, projectSlug, envSlug, addonSlug, bucket, key string) (io.ReadCloser, int64, string, error) {
	if err := validKey(key); err != nil {
		return nil, 0, "", err
	}
	c, b, err := s.bucketClient(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return nil, 0, "", err
	}
	body, size, contentType, err := c.Open(ctx, b.Name, key)
	if errors.Is(err, objstore.ErrNotFound) {
		return nil, 0, "", fmt.Errorf("%w: ficheiro %s", ErrNotFound, key)
	}
	return body, size, contentType, err
}

func (s *Service) DeleteFile(ctx context.Context, projectSlug, envSlug, addonSlug, bucket, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	c, b, err := s.bucketClient(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return err
	}
	return c.Delete(ctx, b.Name, key)
}

// FileLink é o endereço para partilhar um ficheiro: num bucket público, o
// direto (pelo domínio próprio, se houver um ativo); num privado, um link
// assinado válido uma hora -- os dois só com o endereço público ligado
// (ou um domínio), senão não há por onde lá chegar de fora.
func (s *Service) FileLink(ctx context.Context, projectSlug, envSlug, addonSlug, bucket, key string) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	p, e, a, b, err := s.bucketScope(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return "", err
	}
	if b.Public {
		domains, err := s.db.ListBucketDomains(ctx, a.ID, b.Name)
		if err != nil {
			return "", err
		}
		for _, d := range domains {
			if d.Status == store.DomainActive {
				return s.scheme() + "://" + d.Hostname + "/" + escapeKey(key), nil
			}
		}
	}
	public := s.addonPublicURL(p, e, a)
	if public == "" {
		return "", invalid("sem endereço público, este ficheiro só é acessível dentro do ambiente -- liga-o em \"Buckets e acesso\"")
	}
	if b.Public {
		return public + "/" + b.Name + "/" + escapeKey(key), nil
	}
	user, pw, err := s.addonLogin(ctx, e.ID, a)
	if err != nil {
		return "", err
	}
	signer, err := objstore.New(public, storageRegion, user, pw, "")
	if err != nil {
		return "", err
	}
	return signer.PresignGet(b.Name, key, signedLinkTTL)
}

// escapeKey codifica cada parte do nome, mantendo as "/".
func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		// "+" no caminho seria um espaço para alguns servidores.
		parts[i] = strings.ReplaceAll(url.PathEscape(part), "+", "%2B")
	}
	return strings.Join(parts, "/")
}
