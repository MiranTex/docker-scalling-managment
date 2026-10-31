package objstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// O exemplo "GET Object" da documentação da AWS Signature V4 (bucket
// examplebucket, objeto test.txt, Range bytes=0-9).
func TestSignMatchesAWSExample(t *testing.T) {
	c := &Client{Region: "us-east-1", AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	c.sign(req, emptySHA256, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization =\n%s\nqueria\n%s", got, want)
	}
}

// O exemplo de link assinado da documentação da AWS (GET test.txt,
// válido 86400 s).
func TestPresignMatchesAWSExample(t *testing.T) {
	c := &Client{Endpoint: "https://examplebucket.s3.amazonaws.com", Region: "us-east-1", AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", now: func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }}
	got, err := c.PresignGet("", "test.txt", 86400*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404") {
		t.Errorf("link = %s", got)
	}
}

func TestEscaping(t *testing.T) {
	if got := escapePath("/b/a b/ç+x"); got != "/b/a%20b/%C3%A7%2Bx" {
		t.Errorf("escapePath = %s", got)
	}
}

// Contra um storage a sério: OBJSTORE_TEST_ENDPOINT=https://... mais
// OBJSTORE_TEST_{KEY,SECRET,BUCKET,CA}.
func TestAgainstRealStorage(t *testing.T) {
	endpoint := os.Getenv("OBJSTORE_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("sem OBJSTORE_TEST_ENDPOINT")
	}
	c, err := New(endpoint, "us-east-1", os.Getenv("OBJSTORE_TEST_KEY"), os.Getenv("OBJSTORE_TEST_SECRET"), os.Getenv("OBJSTORE_TEST_CA"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, bucket := context.Background(), os.Getenv("OBJSTORE_TEST_BUCKET")
	body := strings.Repeat("backup ", 200000)
	key := "teste/pasta com espaço/" + time.Now().Format("150405.000") + ".dump"
	if err := c.Put(ctx, bucket, key, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	objs, err := c.List(ctx, bucket, "teste/pasta com espaço/")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range objs {
		if o.Key == key && o.Size == int64(len(body)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("o objeto não aparece na listagem: %+v", objs)
	}
	r, err := c.Get(ctx, bucket, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if string(got) != body {
		t.Errorf("leu %d bytes, enviou %d", len(got), len(body))
	}
	if err := c.Delete(ctx, bucket, key); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, bucket, key); !errors.Is(err, ErrNotFound) {
		t.Errorf("depois de apagar: %v", err)
	}
	bad := *c
	bad.SecretKey = "errada"
	if _, err := bad.List(ctx, bucket, ""); err == nil {
		t.Error("uma chave errada foi aceite")
	}
}
