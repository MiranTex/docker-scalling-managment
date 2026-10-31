// Package objstore é um cliente mínimo da API do S3 -- o suficiente para
// guardar backups no storage da plataforma (SeaweedFS): enviar, listar,
// ler e apagar objetos. Pedidos assinados com AWS Signature V4, endereços
// no estilo path (https://storage:8443/<bucket>/<chave>).
package objstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("objstore: objeto não existe")

type Client struct {
	// Endpoint: esquema, host e porta (ex: https://storage:8443).
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	HTTP      *http.Client
	now       func() time.Time
}

// New monta um cliente; caFile (opcional) é a CA que assina o certificado
// do endpoint, em vez das do sistema.
func New(endpoint, region, accessKey, secretKey, caFile string) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("objstore: lendo a CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("objstore: %s não tem certificados", caFile)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &Client{
		Endpoint: strings.TrimSuffix(endpoint, "/"), Region: region,
		AccessKey: accessKey, SecretKey: secretKey,
		HTTP: &http.Client{Transport: transport},
	}, nil
}

type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Put envia um objeto. size tem de ser o tamanho exato de body: o corpo
// vai em streaming, sem ser lido duas vezes para o assinar (UNSIGNED-PAYLOAD
// -- a integridade fica com o TLS).
func (c *Client) Put(ctx context.Context, bucket, key string, body io.Reader, size int64) error {
	req, err := c.request(ctx, http.MethodPut, bucket, key, nil, body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	res, err := c.do(req, "UNSIGNED-PAYLOAD")
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// Get devolve o conteúdo de um objeto; quem chama fecha-o.
func (c *Client) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	req, err := c.request(ctx, http.MethodGet, bucket, key, nil, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.do(req, emptySHA256)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

func (c *Client) Delete(ctx context.Context, bucket, key string) error {
	req, err := c.request(ctx, http.MethodDelete, bucket, key, nil, nil)
	if err != nil {
		return err
	}
	res, err := c.do(req, emptySHA256)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// List devolve todos os objetos com o prefixo, por ordem de chave.
func (c *Client) List(ctx context.Context, bucket, prefix string) ([]Object, error) {
	var out []Object
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		req, err := c.request(ctx, http.MethodGet, bucket, "", q, nil)
		if err != nil {
			return nil, err
		}
		res, err := c.do(req, emptySHA256)
		if err != nil {
			return nil, err
		}
		var page struct {
			Contents []struct {
				Key          string
				Size         int64
				LastModified time.Time
			}
			IsTruncated           bool
			NextContinuationToken string
		}
		err = xml.NewDecoder(res.Body).Decode(&page)
		res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("objstore: resposta da listagem ilegível: %w", err)
		}
		for _, o := range page.Contents {
			out = append(out, Object{Key: o.Key, Size: o.Size, LastModified: o.LastModified})
		}
		if !page.IsTruncated || page.NextContinuationToken == "" {
			return out, nil
		}
		token = page.NextContinuationToken
	}
}

func (c *Client) request(ctx context.Context, method, bucket, key string, q url.Values, body io.Reader) (*http.Request, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("objstore: endpoint inválido: %w", err)
	}
	u.Path = "/" + bucket
	if key != "" {
		u.Path += "/" + key
	}
	u.RawQuery = q.Encode()
	return http.NewRequestWithContext(ctx, method, u.String(), body)
}

func (c *Client) do(req *http.Request, payloadHash string) (*http.Response, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	c.sign(req, payloadHash, now().UTC())
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("objstore: %s %s: %w", req.Method, req.URL.Path, err)
	}
	if res.StatusCode/100 == 2 {
		return res, nil
	}
	defer res.Body.Close()
	var e struct{ Code, Message string }
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	_ = xml.Unmarshal(raw, &e)
	if res.StatusCode == http.StatusNotFound && e.Code != "NoSuchBucket" {
		return nil, ErrNotFound
	}
	if e.Code == "" {
		e.Code = res.Status
	}
	return nil, fmt.Errorf("objstore: %s %s: %s %s", req.Method, req.URL.Path, e.Code, e.Message)
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sign acrescenta os cabeçalhos da AWS Signature V4 (assina host, os
// x-amz-* e o range, se houver).
func (c *Client) sign(req *http.Request, payloadHash string, t time.Time) {
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	headers := map[string]string{"host": req.URL.Host}
	for name, v := range req.Header {
		n := strings.ToLower(name)
		if strings.HasPrefix(n, "x-amz-") || n == "range" || n == "content-type" {
			headers[n] = strings.TrimSpace(strings.Join(v, ","))
		}
	}
	names := make([]string, 0, len(headers))
	for n := range headers {
		names = append(names, n)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		canonHeaders.WriteString(n + ":" + headers[n] + "\n")
	}
	signed := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		escapePath(req.URL.Path),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signed,
		payloadHash,
	}, "\n")
	scope := day + "/" + c.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA256(canonical)

	key := hmacSHA256([]byte("AWS4"+c.SecretKey), day)
	key = hmacSHA256(key, c.Region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKey+"/"+scope+
		", SignedHeaders="+signed+", Signature="+signature)
}

// escapePath codifica cada segmento como a AWS (RFC 3986, "/" intacto).
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = uriEncode(s)
	}
	return strings.Join(parts, "/")
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k)+"="+uriEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

func uriEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' || ch == '~' {
			b.WriteByte(ch)
		} else {
			b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(ch)|0x100, 16)[1:]))
		}
	}
	return b.String()
}

func hexSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
