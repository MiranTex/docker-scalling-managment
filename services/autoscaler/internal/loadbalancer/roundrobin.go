// Package loadbalancer distribui requisições HTTP entre os containers vivos
// de um serviço. A lista de backends é atualizada de fora (quem descobre os
// containers é o dockerclient); este pacote só sabe escolher "qual é o
// próximo" e fazer o proxy da requisição.
package loadbalancer

import "sync"

// Backend é um destino possível para o proxy: o container que o originou
// (só pra debug/log) e o endereço "host:porta" já pronto pra usar em uma URL.
type Backend struct {
	ContainerID string
	Addr        string
}

// RoundRobin escolhe o próximo backend em sequência circular. É seguro pra
// uso concorrente: cada requisição HTTP chama Next() de sua própria
// goroutine.
type RoundRobin struct {
	mu       sync.Mutex
	backends []Backend
	next     int
}

// NewRoundRobin cria um balanceador vazio; use SetBackends para populá-lo.
func NewRoundRobin() *RoundRobin {
	return &RoundRobin{}
}

// SetBackends substitui a lista de backends disponíveis. Chamado
// periodicamente por quem descobre os containers do serviço.
func (r *RoundRobin) SetBackends(backends []Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends = backends
	// não resetamos "next": se a lista encolher, o índice se ajusta no
	// próximo Next() via módulo, sem favorecer sempre o backend 0.
}

// HasBackends diz se há ao menos um backend disponível, sem consumir uma
// posição da rotação (ao contrário de Next).
func (r *RoundRobin) HasBackends() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.backends) > 0
}

// Next devolve o próximo backend na rotação. ok é false se não há nenhum
// backend disponível no momento.
func (r *RoundRobin) Next() (backend Backend, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.backends) == 0 {
		return Backend{}, false
	}

	backend = r.backends[r.next%len(r.backends)]
	r.next++
	return backend, true
}
