package loadbalancer

import (
	"net"
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
