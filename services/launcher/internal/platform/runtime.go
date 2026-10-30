package platform

import (
	"context"
	"sync"
	"time"
)

// runtime é o estado que só existe em memória: o que foi observado pelo
// reconciliador (que réplicas passaram o health check) e o que está a
// meio (deploys, drenagens, decisões de autoscaling). Perdê-lo num
// restart é seguro -- tudo volta a ser observado no tick seguinte, e uma
// réplica só volta ao routing depois de passar outra vez no health check.
type runtime struct {
	mu        sync.Mutex
	deploying map[string]bool      // serviço -> deploy em curso
	ready     map[string]bool      // container -> passou no health check
	fails     map[string]int       // container -> falhas seguidas do health check
	draining  map[string]time.Time // container -> quando parar
	desired   map[string]int       // serviço -> réplicas decididas pelo autoscaling
	lastScale map[string]time.Time // serviço -> última mudança do autoscaling
	lastEval  map[string]time.Time // serviço -> última avaliação de CPU
	// problem é o último motivo por que o reconciliador não conseguiu pôr
	// o serviço como devia (ex: quota esgotada) -- mostrado na UI.
	problem map[string]string
	// jobs: cancelamento de cada job a correr neste processo.
	jobs map[string]context.CancelCauseFunc
	// nodeDrain: nó -> a ser esvaziado (true) ou o resultado do último.
	nodeDrain    map[string]bool
	nodeDrainMsg map[string]string
	kick         chan struct{}
}

func newRuntime() *runtime {
	return &runtime{
		deploying: map[string]bool{},
		ready:     map[string]bool{},
		fails:     map[string]int{},
		draining:  map[string]time.Time{},
		desired:   map[string]int{},
		lastScale: map[string]time.Time{},
		lastEval:  map[string]time.Time{},
		problem:   map[string]string{},
		jobs:      map[string]context.CancelCauseFunc{},
		nodeDrain: map[string]bool{}, nodeDrainMsg: map[string]string{},
		kick: make(chan struct{}, 1),
	}
}

// startDeploy marca o serviço; falso se já havia um deploy em curso.
func (r *runtime) startDeploy(svc string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deploying[svc] {
		return false
	}
	r.deploying[svc] = true
	return true
}

func (r *runtime) endDeploy(svc string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.deploying, svc)
}

func (r *runtime) isDeploying(svc string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deploying[svc]
}

func (r *runtime) isReady(container string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready[container] && r.draining[container].IsZero()
}

// observe regista o resultado de um health check. Entra no routing à
// primeira resposta boa; sai só ao fim de 3 falhas seguidas, para um
// pedido lento isolado não tirar uma réplica de serviço.
func (r *runtime) observe(container string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ok {
		r.ready[container] = true
		r.fails[container] = 0
		return
	}
	r.fails[container]++
	if r.fails[container] >= 3 || !r.ready[container] {
		r.ready[container] = false
	}
}

// observeSuspect é o observe de uma réplica de um nó sem sinal: uma falha
// basta para sair do routing (o nó provavelmente morreu).
func (r *runtime) observeSuspect(container string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready[container] = ok
	if ok {
		r.fails[container] = 0
	}
}

func (r *runtime) drain(container string, until time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.draining[container].IsZero() {
		r.draining[container] = until
	}
}

func (r *runtime) drainDeadline(container string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.draining[container]
	return t, ok
}

func (r *runtime) forget(container string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ready, container)
	delete(r.fails, container)
	delete(r.draining, container)
}

func (r *runtime) forgetDesired(svc string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.desired, svc)
}

func (r *runtime) setProblem(svc, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if msg == "" {
		delete(r.problem, svc)
		return
	}
	r.problem[svc] = msg
}

func (r *runtime) getProblem(svc string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.problem[svc]
}

func (r *runtime) registerJob(id string, cancel context.CancelCauseFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[id] = cancel
}

func (r *runtime) unregisterJob(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, id)
}

// cancelJob devolve false se o job não está a correr neste processo.
func (r *runtime) cancelJob(id string, cause error) bool {
	r.mu.Lock()
	cancel, ok := r.jobs[id]
	r.mu.Unlock()
	if ok {
		cancel(cause)
	}
	return ok
}

func (r *runtime) startNodeDrain(node string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nodeDrain[node] {
		return false
	}
	r.nodeDrain[node] = true
	r.nodeDrainMsg[node] = "a esvaziar..."
	return true
}

func (r *runtime) endNodeDrain(node, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodeDrain, node)
	r.nodeDrainMsg[node] = msg
}

// nodeDrainState: a esvaziar agora, e a mensagem do último (ou do atual).
func (r *runtime) nodeDrainState(node string) (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nodeDrain[node], r.nodeDrainMsg[node]
}
