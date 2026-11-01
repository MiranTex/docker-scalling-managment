package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"launcher/internal/objstore"
	"launcher/internal/store"
)

// Acesso público aos addons storage. Três níveis:
//   - interno: só os serviços do ambiente (o de sempre);
//   - endereço público (public_endpoint): o addon fica em
//     https://<addon>-<ambiente>-<projeto>.<domínio>, pelo Traefik -- os
//     ficheiros continuam privados, abre-os quem tiver um link assinado;
//   - bucket público: leitura anónima dos objetos desse bucket (CDN),
//     no endereço público e nos domínios próprios do bucket.
//
// Escrever exige sempre as credenciais, e listar um bucket nunca é
// público. O estado dos buckets (policy, CORS) vive no próprio SeaweedFS;
// a base guarda o que se pediu, para o mostrar.

var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

const maxCORSOrigins = 20

// BucketView é um bucket com os endereços por onde se lê.
type BucketView struct {
	store.StorageBucket
	// URL: <endereço público>/<bucket>, com o endereço público ligado.
	URL     string       `json:"url,omitempty"`
	Domains []DomainView `json:"domains"`
}

type BucketInput struct {
	Name        string   `json:"name"`
	Public      bool     `json:"public"`
	CORSOrigins []string `json:"corsOrigins"`
}

type BucketUpdate struct {
	Public      *bool     `json:"public"`
	CORSOrigins *[]string `json:"corsOrigins"`
}

// addonHost é o endereço público de um addon: o mesmo esquema dos
// serviços (um só nível, para o DNS wildcard).
func addonHost(p store.Project, e store.Environment, a store.Addon, base string) string {
	return a.Slug + "-" + e.Slug + "-" + p.Slug + "." + base
}

// addonPublicURL: vazio sem endereço público.
func (s *Service) addonPublicURL(p store.Project, e store.Environment, a store.Addon) string {
	if a.Kind != store.AddonStorage || !a.PublicEndpoint || s.cfg.PublicBaseDomain == "" {
		return ""
	}
	return s.scheme() + "://" + addonHost(p, e, a, s.cfg.PublicBaseDomain)
}

func publicURLVar(a store.Addon) string { return a.EnvVar + "_PUBLIC_URL" }

func (s *Service) storageScope(ctx context.Context, projectSlug, envSlug, addonSlug string) (store.Project, store.Environment, store.Addon, error) {
	p, e, a, err := s.addonScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return p, e, a, err
	}
	if a.Kind != store.AddonStorage {
		return p, e, a, invalid("o addon %s não é um storage", a.Slug)
	}
	return p, e, a, nil
}

// storageClient fala com a API do addon com as credenciais dele.
func (s *Service) storageClient(ctx context.Context, p store.Project, e store.Environment, a store.Addon) (*objstore.Client, error) {
	if err := errAddonStopped(a); err != nil {
		return nil, err
	}
	user, pw, err := s.addonLogin(ctx, e.ID, a)
	if err != nil {
		return nil, err
	}
	return objstore.New(s.storageURL(p, e, a), storageRegion, user, pw, "")
}

func (s *Service) ListBuckets(ctx context.Context, projectSlug, envSlug, addonSlug string) ([]BucketView, error) {
	p, e, a, err := s.storageScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return nil, err
	}
	buckets, err := s.db.ListBuckets(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	out := make([]BucketView, 0, len(buckets))
	for _, b := range buckets {
		v, err := s.bucketView(ctx, p, e, a, b)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) bucketView(ctx context.Context, p store.Project, e store.Environment, a store.Addon, b store.StorageBucket) (BucketView, error) {
	v := BucketView{StorageBucket: b, Domains: []DomainView{}}
	if u := s.addonPublicURL(p, e, a); u != "" {
		v.URL = u + "/" + b.Name
	}
	domains, err := s.db.ListBucketDomains(ctx, a.ID, b.Name)
	if err != nil {
		return v, err
	}
	for _, d := range domains {
		v.Domains = append(v.Domains, s.bucketDomainView(p, e, a, d))
	}
	return v, nil
}

func (s *Service) bucketDomainView(p store.Project, e store.Environment, a store.Addon, d store.Domain) DomainView {
	v := DomainView{Domain: d, ExpectedIPs: s.cfg.PublicIPs}
	if v.ExpectedIPs == nil {
		v.ExpectedIPs = []string{}
	}
	if s.cfg.PublicBaseDomain != "" {
		v.ExpectedCNAME = addonHost(p, e, a, s.cfg.PublicBaseDomain)
	}
	return v
}

func validCORSOrigins(origins []string) ([]string, error) {
	if len(origins) > maxCORSOrigins {
		return nil, invalid("no máximo %d origens de CORS", maxCORSOrigins)
	}
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o == "" {
			continue
		}
		if o != "*" {
			u, err := url.Parse(o)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
				return nil, invalid("origem de CORS %q inválida -- ex: https://app.loja.pt (ou * para todas)", o)
			}
		}
		out = append(out, o)
	}
	return out, nil
}

// applyBucket põe no SeaweedFS a visibilidade e o CORS do bucket.
func applyBucket(ctx context.Context, c *objstore.Client, b store.StorageBucket) error {
	if err := c.SetPublicRead(ctx, b.Name, b.Public); err != nil {
		return fmt.Errorf("visibilidade do bucket %s: %w", b.Name, err)
	}
	if err := c.SetCORS(ctx, b.Name, b.CORSOrigins); err != nil {
		return fmt.Errorf("CORS do bucket %s: %w", b.Name, err)
	}
	return nil
}

func (s *Service) CreateBucket(ctx context.Context, projectSlug, envSlug, addonSlug string, in BucketInput, by string) (BucketView, error) {
	p, e, a, err := s.storageScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return BucketView{}, err
	}
	if !bucketNameRe.MatchString(in.Name) {
		return BucketView{}, invalid("nome de bucket %q inválido -- 3 a 63 caracteres: letras minúsculas, números e \"-\"", in.Name)
	}
	cors, err := validCORSOrigins(in.CORSOrigins)
	if err != nil {
		return BucketView{}, err
	}
	b := store.StorageBucket{AddonID: a.ID, Name: in.Name, Public: in.Public, CORSOrigins: cors, CreatedBy: by}
	if err := s.db.CreateBucket(ctx, b); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return BucketView{}, fmt.Errorf("%w: o bucket %s já existe", ErrConflict, in.Name)
		}
		return BucketView{}, err
	}
	c, err := s.storageClient(ctx, p, e, a)
	if err == nil {
		if err = c.CreateBucket(ctx, b.Name); err == nil {
			err = applyBucket(ctx, c, b)
		}
	}
	if err != nil {
		_ = s.db.DeleteBucket(ctx, a.ID, b.Name)
		return BucketView{}, fmt.Errorf("criando o bucket no storage (está a correr?): %w", err)
	}
	b, _ = s.db.GetBucket(ctx, a.ID, b.Name)
	return s.bucketView(ctx, p, e, a, b)
}

func (s *Service) UpdateBucket(ctx context.Context, projectSlug, envSlug, addonSlug, name string, in BucketUpdate) (BucketView, error) {
	p, e, a, err := s.storageScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return BucketView{}, err
	}
	b, err := s.db.GetBucket(ctx, a.ID, name)
	if err != nil {
		return BucketView{}, mapStoreErr(err)
	}
	if in.Public != nil {
		b.Public = *in.Public
	}
	if in.CORSOrigins != nil {
		if b.CORSOrigins, err = validCORSOrigins(*in.CORSOrigins); err != nil {
			return BucketView{}, err
		}
	}
	c, err := s.storageClient(ctx, p, e, a)
	if err != nil {
		return BucketView{}, err
	}
	if err := applyBucket(ctx, c, b); err != nil {
		return BucketView{}, err
	}
	if err := s.db.UpdateBucket(ctx, b); err != nil {
		return BucketView{}, err
	}
	// Um bucket que deixa de ser público sai dos domínios próprios.
	s.syncRouting(ctx)
	return s.bucketView(ctx, p, e, a, b)
}

// DeleteBucket só apaga buckets vazios: os dados não vão por engano. O
// bucket app fica sempre -- é o das variáveis do ambiente.
func (s *Service) DeleteBucket(ctx context.Context, projectSlug, envSlug, addonSlug, name string) error {
	p, e, a, err := s.storageScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return err
	}
	if name == storageBucket {
		return invalid("o bucket %s é o das variáveis %s_* e não se apaga", storageBucket, a.EnvVar)
	}
	if _, err := s.db.GetBucket(ctx, a.ID, name); err != nil {
		return mapStoreErr(err)
	}
	c, err := s.storageClient(ctx, p, e, a)
	if err != nil {
		return err
	}
	if err := c.DeleteBucket(ctx, name); err != nil && !errors.Is(err, objstore.ErrNotFound) {
		if strings.Contains(err.Error(), "BucketNotEmpty") {
			return invalid("o bucket %s tem ficheiros -- apaga-os primeiro", name)
		}
		return err
	}
	if err := s.db.DeleteBucket(ctx, a.ID, name); err != nil {
		return mapStoreErr(err)
	}
	s.syncRouting(ctx)
	return nil
}

// SetStoragePublic liga ou desliga o endereço público do addon, e a
// variável <PREFIXO>_PUBLIC_URL dos serviços (no próximo deploy).
func (s *Service) SetStoragePublic(ctx context.Context, projectSlug, envSlug, addonSlug string, on bool, by string) (AddonView, error) {
	p, e, a, err := s.storageScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return AddonView{}, err
	}
	if on && s.cfg.PublicBaseDomain == "" {
		return AddonView{}, invalid("a plataforma não tem domínio base -- sem ele, não há endereço público")
	}
	if on {
		if existing, err := s.db.GetSecret(ctx, e.ID, publicURLVar(a)); err == nil && existing.ManagedBy != "addon:"+a.Slug {
			return AddonView{}, fmt.Errorf("%w: já existe o segredo %s neste ambiente", ErrConflict, publicURLVar(a))
		}
	}
	if err := s.db.SetAddonPublicEndpoint(ctx, a.ID, on); err != nil {
		return AddonView{}, err
	}
	a.PublicEndpoint = on
	if on {
		err = s.putSecret(ctx, e.ID, publicURLVar(a), s.addonPublicURL(p, e, a), "addon:"+a.Slug, by)
	} else {
		err = s.db.DeleteSecret(ctx, e.ID, publicURLVar(a))
		if errors.Is(err, store.ErrSecretNotFound) {
			err = nil
		}
	}
	if err != nil {
		return AddonView{}, err
	}
	s.syncRouting(ctx)
	return s.addonView(ctx, p, e, a), nil
}

func (s *Service) bucketScope(ctx context.Context, projectSlug, envSlug, addonSlug, bucket string) (store.Project, store.Environment, store.Addon, store.StorageBucket, error) {
	p, e, a, err := s.storageScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return p, e, a, store.StorageBucket{}, err
	}
	b, err := s.db.GetBucket(ctx, a.ID, bucket)
	return p, e, a, b, mapStoreErr(err)
}

// AddBucketDomain: um domínio próprio servido por um bucket público (ex:
// cdn.loja.pt -> o bucket media; cdn.loja.pt/foto.jpg é media/foto.jpg).
func (s *Service) AddBucketDomain(ctx context.Context, projectSlug, envSlug, addonSlug, bucket, hostname, by string) (DomainView, error) {
	p, e, a, b, err := s.bucketScope(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return DomainView{}, err
	}
	if !b.Public {
		return DomainView{}, invalid("só um bucket público tem domínio próprio -- torna o %s público primeiro", b.Name)
	}
	if hostname, err = s.normalizeHostname(hostname); err != nil {
		return DomainView{}, err
	}
	err = s.db.CreateBucketDomain(ctx, store.Domain{Hostname: hostname, AddonID: a.ID, Bucket: b.Name, CreatedBy: by})
	if errors.Is(err, store.ErrConflict) {
		return DomainView{}, fmt.Errorf("%w: o domínio %s já está associado a um serviço ou bucket", ErrConflict, hostname)
	}
	if err != nil {
		return DomainView{}, err
	}
	s.verifyDomain(ctx, hostname)
	return s.getBucketDomain(ctx, p, e, a, b, hostname)
}

func (s *Service) getBucketDomain(ctx context.Context, p store.Project, e store.Environment, a store.Addon, b store.StorageBucket, hostname string) (DomainView, error) {
	domains, err := s.db.ListBucketDomains(ctx, a.ID, b.Name)
	if err != nil {
		return DomainView{}, err
	}
	for _, d := range domains {
		if d.Hostname == hostname {
			return s.bucketDomainView(p, e, a, d), nil
		}
	}
	return DomainView{}, fmt.Errorf("%w: domínio %s", ErrNotFound, hostname)
}

func (s *Service) VerifyBucketDomain(ctx context.Context, projectSlug, envSlug, addonSlug, bucket, hostname string) (DomainView, error) {
	p, e, a, b, err := s.bucketScope(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return DomainView{}, err
	}
	if _, err := s.getBucketDomain(ctx, p, e, a, b, hostname); err != nil {
		return DomainView{}, err
	}
	s.verifyDomain(ctx, hostname)
	return s.getBucketDomain(ctx, p, e, a, b, hostname)
}

func (s *Service) DeleteBucketDomain(ctx context.Context, projectSlug, envSlug, addonSlug, bucket, hostname string) error {
	_, _, a, b, err := s.bucketScope(ctx, projectSlug, envSlug, addonSlug, bucket)
	if err != nil {
		return err
	}
	found, err := s.db.DeleteBucketDomain(ctx, a.ID, b.Name, hostname)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: domínio %s", ErrNotFound, hostname)
	}
	s.syncRouting(ctx)
	return nil
}
