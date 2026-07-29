package loadbalancer

import (
	"context"
	"net"
	"net/http"
	"time"
)

// TCPHealthy faz o check de saúde mais básico possível: tenta abrir uma
// conexão TCP no endereço e fecha em seguida. Não valida o conteúdo da
// resposta (isso exigiria saber o protocolo da aplicação), só que algo está
// de fato escutando e aceitando conexões — suficiente pra filtrar containers
// que subiram mas cuja aplicação ainda não terminou de inicializar.
func TCPHealthy(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// HTTPHealthy faz um GET em path (ex: "/health") e considera saudável
// qualquer resposta com status < 400. Mais preciso que TCPHealthy pra
// serviços HTTP: um container pode aceitar a conexão TCP e ainda assim
// devolver erro (app subiu mas não terminou de inicializar, dependência
// indisponível, etc).
func HTTPHealthy(ctx context.Context, addr, path string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode < 400
}
