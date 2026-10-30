package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func loginFrom(t *testing.T, mux http.Handler, ip, email, pass string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(loginRequest{Email: email, Password: pass})
	req := httptest.NewRequest(http.MethodPost, "/v1/login", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// Depois de 5 passwords erradas, a conta espera; a espera dobra a cada
// falha seguinte; uma certa (depois da espera) limpa o contador.
func TestLoginSlowsDownPasswordGuessing(t *testing.T) {
	deps := newTestDeps()
	clock := time.Now()
	deps.handler.limiter.now = func() time.Time { return clock }
	mux := deps.handler.Routes()
	doRequest(t, mux, http.MethodPost, "/v1/register", registerRequest{Email: "ana@x.pt", Password: "senha-certa"})

	for i := 0; i < accountFreeFailures; i++ {
		if rec := loginFrom(t, mux, "203.0.113.1", "ana@x.pt", "errada"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("tentativa %d: %d", i+1, rec.Code)
		}
	}
	// Agora espera -- mesmo com a password certa, e de outro IP.
	rec := loginFrom(t, mux, "198.51.100.7", "ana@x.pt", "senha-certa")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("depois de 5 falhas: %d %s", rec.Code, rec.Body)
	}
	// Outra conta não é afetada.
	if rec := loginFrom(t, mux, "198.51.100.7", "outra@x.pt", "x"); rec.Code != http.StatusUnauthorized {
		t.Errorf("outra conta: %d", rec.Code)
	}

	clock = clock.Add(firstWait + time.Second)
	if rec := loginFrom(t, mux, "203.0.113.1", "ana@x.pt", "errada"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("depois da espera: %d", rec.Code)
	}
	// A 6.ª falha manda esperar 2 minutos.
	clock = clock.Add(firstWait + time.Second)
	if rec := loginFrom(t, mux, "203.0.113.1", "ana@x.pt", "senha-certa"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a espera devia ter dobrado: %d", rec.Code)
	}
	clock = clock.Add(firstWait)
	if rec := loginFrom(t, mux, "203.0.113.1", "ana@x.pt", "senha-certa"); rec.Code != http.StatusOK {
		t.Fatalf("password certa depois da espera: %d %s", rec.Code, rec.Body)
	}
	if rec := loginFrom(t, mux, "203.0.113.1", "ana@x.pt", "errada"); rec.Code != http.StatusUnauthorized {
		t.Errorf("depois de entrar, o contador recomeça: %d", rec.Code)
	}
}

// Muitas contas a partir do mesmo IP também abrandam (adivinhar e-mails).
func TestLoginSlowsDownOneIPTryingManyAccounts(t *testing.T) {
	deps := newTestDeps()
	mux := deps.handler.Routes()
	for i := 0; i < ipFreeFailures; i++ {
		loginFrom(t, mux, "203.0.113.9", "conta"+itoaTest(i)+"@x.pt", "x")
	}
	if rec := loginFrom(t, mux, "203.0.113.9", "mais-uma@x.pt", "x"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("IP com %d falhas: %d", ipFreeFailures, rec.Code)
	}
	if rec := loginFrom(t, mux, "198.51.100.1", "mais-uma@x.pt", "x"); rec.Code != http.StatusUnauthorized {
		t.Errorf("outro IP: %d", rec.Code)
	}
}

func itoaTest(i int) string { return string(rune('a'+i/26)) + string(rune('a'+i%26)) }
