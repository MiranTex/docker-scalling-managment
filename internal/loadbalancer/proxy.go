package loadbalancer

import (
	"log"
	"net/http"
	"net/http/httputil"
)

// NewProxy monta um handler HTTP que encaminha cada requisição para o
// próximo backend escolhido pelo balancer. Usamos um único httputil.ReverseProxy
// com Director dinâmico (em vez de criar um ReverseProxy por requisição) pra
// reaproveitar transport/conexões.
func NewProxy(balancer *RoundRobin) http.Handler {
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			backend, ok := balancer.Next()
			if !ok {
				// Sem backend: deixamos o Host vazio, o que faz o
				// RoundTripper falhar adiante e o ErrorHandler abaixo
				// devolver 502 pro cliente.
				return
			}
			req.URL.Scheme = "http"
			req.URL.Host = backend.Addr
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("loadbalancer: erro encaminhando requisição: %v", err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !balancer.HasBackends() {
			http.Error(w, "service unavailable: nenhum backend saudável", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
