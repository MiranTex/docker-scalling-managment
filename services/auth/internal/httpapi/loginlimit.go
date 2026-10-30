package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limite de tentativas de password: depois de algumas falhas seguidas,
// uma chave (a conta, ou o IP de onde vêm os pedidos) fica a aguardar
// antes da tentativa seguinte -- 1 minuto, depois 2, 4... até 15. Uma
// password certa limpa a conta. Esperas em vez de um bloqueio: quem
// adivinha passwords fica lento demais para valer a pena, e quem quisesse
// bloquear a conta de alguém (ex: o admin) só a atrasa uns minutos.
//
// Em memória: um restart limpa os contadores -- aceitável para o que é.

const (
	accountFreeFailures = 5  // falhas por conta até ter de esperar
	ipFreeFailures      = 20 // por IP (várias contas a partir do mesmo sítio)
	firstWait           = time.Minute
	maxWait             = 15 * time.Minute
	forgetAfter         = time.Hour
)

type failures struct {
	count int
	until time.Time // não aceita tentativas antes disto
	last  time.Time
}

type loginLimiter struct {
	mu   sync.Mutex
	keys map[string]*failures
	now  func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{keys: map[string]*failures{}, now: time.Now}
}

// wait diz quanto falta para a próxima tentativa (0 = pode tentar).
func (l *loginLimiter) wait(keys ...string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var longest time.Duration
	for _, k := range keys {
		if f := l.keys[k]; f != nil && f.until.After(now) {
			longest = max(longest, f.until.Sub(now))
		}
	}
	return longest
}

// fail regista uma password errada em cada chave; free é quantas falhas
// cada uma tem antes de começar a esperar.
func (l *loginLimiter) fail(free map[string]int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	for k, allowed := range free {
		f := l.keys[k]
		if f == nil {
			f = &failures{}
			l.keys[k] = f
		}
		f.count++
		f.last = now
		// A falha número "allowed" já manda esperar 1 minuto; cada uma a
		// seguir dobra a espera.
		if over := f.count - allowed + 1; over > 0 {
			wait := firstWait << min(over-1, 10)
			f.until = now.Add(min(wait, maxWait))
		}
	}
}

func (l *loginLimiter) reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.keys, k)
	}
}

func (l *loginLimiter) prune(now time.Time) {
	if len(l.keys) < 1000 {
		return
	}
	for k, f := range l.keys {
		if now.Sub(f.last) > forgetAfter && !f.until.After(now) {
			delete(l.keys, k)
		}
	}
}

// clientIP: o IP de quem pediu. O auth só é alcançável pela rede interna
// (o portal chama-o); o portal passa o IP real que o Traefik lhe deu.
func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		ip, _, _ := strings.Cut(f, ",")
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func accountKey(email string) string { return "account:" + strings.ToLower(strings.TrimSpace(email)) }
func ipKey(ip string) string         { return "ip:" + ip }

// tooManyAttempts responde 429 com o tempo de espera.
func tooManyAttempts(w http.ResponseWriter, wait time.Duration) {
	minutes := int(wait.Minutes()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	msg := "demasiadas tentativas falhadas: tenta outra vez dentro de 1 minuto"
	if minutes > 1 {
		msg = "demasiadas tentativas falhadas: tenta outra vez dentro de " + strconv.Itoa(minutes) + " minutos"
	}
	writeError(w, http.StatusTooManyRequests, "too_many_attempts", msg)
}
