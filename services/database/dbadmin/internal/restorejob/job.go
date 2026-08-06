// Package restorejob rastreia um PITR do início ao fim: parar o
// Postgres, restaurar via pgbackrest, arrancar de novo em recovery
// pausado, e só terminar quando alguém confirmar (ou a operação falhar
// nalgum passo). Mais fases que backupjob porque um restore, ao
// contrário de um backup, para deliberadamente a meio à espera de
// confirmação humana (ver services/database/README.md, "Restaurar para
// um instante específico").
package restorejob

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Status string

const (
	StatusRunning              Status = "running"               // a parar o Postgres / a correr pgbackrest restore
	StatusAwaitingConfirmation Status = "awaiting_confirmation" // Postgres em recovery pausado, à espera de confirmação
	StatusSucceeded            Status = "succeeded"             // confirmado e promovido, backup full de reancoragem feito
	StatusFailed               Status = "failed"
)

type Job struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"` // time | xid | lsn | name
	Target      string    `json:"target"`
	Status      Status    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	StartedBy   string    `json:"started_by"`
	ConfirmedBy string    `json:"confirmed_by,omitempty"`
}

var (
	ErrAlreadyRunning = errors.New("restorejob: já existe uma operação de restore/backup em andamento")
	ErrNotFound       = errors.New("restorejob: job desconhecido")
	ErrWrongState     = errors.New("restorejob: job não está à espera de confirmação")
)

// Runner executa as três fases físicas do restore -- implementado por
// *httpapi's orquestração real (stop, pgbackrest restore, start) para
// que este pacote fique testável sem precisar de um Postgres de
// verdade. Confirm faz o pg_wal_replay_resume() + backup full de
// reancoragem.
type Runner interface {
	Restore(ctx context.Context, restoreType, target string) error
	Confirm(ctx context.Context) error
}

// Lock é a mesma exclusão mútua que backupjob.Manager usa para backups --
// passada de fora para que restore e backup nunca corram ao mesmo tempo
// (ambos mexem no PGDATA/pgBackRest da mesma stanza). Ver
// httpapi.NewHandler, que passa o *backupjob.Manager como Lock.
type Lock interface {
	TryAcquire() bool
	Release()
}

type Manager struct {
	runner Runner
	lock   Lock

	mu     sync.Mutex
	jobs   map[string]*Job
	nextID int
	active *Job
}

func NewManager(runner Runner, lock Lock) *Manager {
	return &Manager{runner: runner, lock: lock, jobs: map[string]*Job{}}
}

func (m *Manager) Start(restoreType, target, startedBy string) (*Job, error) {
	if !m.lock.TryAcquire() {
		return nil, ErrAlreadyRunning
	}

	m.mu.Lock()
	m.nextID++
	job := &Job{
		ID:        fmt.Sprintf("restore-%d", m.nextID),
		Type:      restoreType,
		Target:    target,
		Status:    StatusRunning,
		StartedAt: time.Now(),
		StartedBy: startedBy,
	}
	m.jobs[job.ID] = job
	m.active = job
	m.mu.Unlock()

	go m.runRestore(job)

	return job, nil
}

func (m *Manager) runRestore(job *Job) {
	// Sem timeout: um restore de uma BD grande, tal como um backup full,
	// pode legitimamente demorar mais do que qualquer valor que
	// chutássemos aqui -- ver a mesma decisão em backupjob.Manager.run.
	err := m.runner.Restore(context.Background(), job.Type, job.Target)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
		job.EndedAt = time.Now()
		m.active = nil
		m.lock.Release()
		return
	}
	job.Status = StatusAwaitingConfirmation
	// Lock permanece preso propositadamente -- um backup ou outro
	// restore não deve poder correr enquanto este ainda está pausado à
	// espera de confirmação (o Postgres está em recovery, não é um
	// estado seguro para mais nenhuma operação pgBackRest).
}

func (m *Manager) Confirm(ctx context.Context, id, confirmedBy string) (*Job, error) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	if job.Status != StatusAwaitingConfirmation {
		m.mu.Unlock()
		return nil, ErrWrongState
	}
	m.mu.Unlock()

	err := m.runner.Confirm(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	job.EndedAt = time.Now()
	job.ConfirmedBy = confirmedBy
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
	} else {
		job.Status = StatusSucceeded
	}
	m.active = nil
	m.lock.Release()
	return job, err
}

func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}
