package main

import (
	"sync"

	"autoscaler/internal/dockerclient"
)

// drainSet rastreia containers que já foram escolhidos para scale down mas
// ainda não foram efetivamente parados: primeiro saem dos backends do load
// balancer, só depois de um tempo de drenagem é que são parados/removidos.
// Precisa ser thread-safe porque a remoção roda numa goroutine separada do
// reconcile loop.
type drainSet struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func newDrainSet() *drainSet {
	return &drainSet{ids: make(map[string]struct{})}
}

func (d *drainSet) add(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ids[id] = struct{}{}
}

func (d *drainSet) remove(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.ids, id)
}

func (d *drainSet) contains(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.ids[id]
	return ok
}

func (d *drainSet) len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.ids)
}

// excludeDraining devolve members sem os containers que já estão em
// drenagem — usados para não escalar para baixo o mesmo container duas
// vezes e para não oferecê-lo mais como backend do load balancer.
func (d *drainSet) excludeDraining(members []dockerclient.Container) []dockerclient.Container {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.ids) == 0 {
		return members
	}
	filtered := make([]dockerclient.Container, 0, len(members))
	for _, m := range members {
		if _, draining := d.ids[m.ID]; !draining {
			filtered = append(filtered, m)
		}
	}
	return filtered
}
