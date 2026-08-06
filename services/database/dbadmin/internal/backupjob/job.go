// Package backupjob rastreia backups disparados via API de forma
// assíncrona -- um backup "full" pode demorar minutos, então o endpoint
// POST /v1/backups não pode bloquear a resposta HTTP até ele terminar; em
// vez disso devolve um ID de job que o chamador consulta via
// GET /v1/backups/jobs/{id}.
package backupjob

import (
	"context"
	"errors"
	"sync"
	"time"

	"dbadmin/internal/pgbackrest"
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

type Job struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Status    Status    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedBy string    `json:"started_by"`
}

// ErrAlreadyRunning é devolvido por Start quando já existe um backup em
// andamento -- pgBackRest não suporta dois backups concorrentes contra a
// mesma stanza (o segundo falharia sozinho de forma menos clara), então
// serializamos aqui em vez de deixar o erro emergir só na CLI.
var ErrAlreadyRunning = errors.New("backupjob: já existe um backup em andamento")

// Manager mantém o histórico de jobs em memória. É deliberadamente
// simples (sem persistência) para este MVP -- um restart do container
// perde o histórico de jobs (não o histórico de backups em si, que
// continua íntegro em "pgbackrest info"), ver README para a limitação.
type Manager struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	running bool
	nextID  int
}

func NewManager() *Manager {
	return &Manager{jobs: map[string]*Job{}}
}

// TryAcquire/Release formam o mesmo Lock que internal/restorejob usa --
// um restore também mexe na stanza pgBackRest desta BD (e para/arranca o
// próprio Postgres), então backup e restore têm de ser mutuamente
// exclusivos, não só backup-com-backup. Ver httpapi.NewHandler, que
// passa este *Manager como restorejob.Lock.
func (m *Manager) TryAcquire() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return false
	}
	m.running = true
	return true
}

func (m *Manager) Release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = false
}

// Start dispara um backup do tipo backupType em background, atribuído a
// startedBy (subject do token que fez o pedido, para o audit trail).
// Devolve o Job recém-criado (ainda "running") ou ErrAlreadyRunning se um
// outro já estiver em curso (backup OU restore, ver TryAcquire).
func (m *Manager) Start(backupType, startedBy string) (*Job, error) {
	if !pgbackrest.ValidType(backupType) {
		return nil, errors.New("backupjob: tipo de backup inválido: " + backupType)
	}
	if !m.TryAcquire() {
		return nil, ErrAlreadyRunning
	}

	m.mu.Lock()
	m.nextID++
	job := &Job{
		ID:        idFromCounter(m.nextID),
		Type:      backupType,
		Status:    StatusRunning,
		StartedAt: time.Now(),
		StartedBy: startedBy,
	}
	m.jobs[job.ID] = job
	m.mu.Unlock()

	go m.run(job, backupType)

	return job, nil
}

func (m *Manager) run(job *Job, backupType string) {
	// Sem timeout aqui de propósito: um backup full de uma BD grande pode
	// legitimamente demorar mais do que qualquer timeout "seguro" que
	// pudéssemos escolher às cegas -- se isto vier a ser um problema real,
	// o limite deve ser configurável, não um valor chutado agora.
	err := pgbackrest.RunBackup(context.Background(), backupType)

	m.mu.Lock()
	job.EndedAt = time.Now()
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
	} else {
		job.Status = StatusSucceeded
	}
	m.mu.Unlock()
	m.Release()
}

func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

// List devolve todos os jobs conhecidos, mais recente primeiro.
func (m *Manager) List() []*Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	for i := 0; i < len(jobs); i++ {
		for j := i + 1; j < len(jobs); j++ {
			if jobs[j].StartedAt.After(jobs[i].StartedAt) {
				jobs[i], jobs[j] = jobs[j], jobs[i]
			}
		}
	}
	return jobs
}

func idFromCounter(n int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "job-0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{digits[n%36]}, b...)
		n /= 36
	}
	return "job-" + string(b)
}
