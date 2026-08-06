// Package audit regista, uma linha por ação, quem fez o quê contra a
// base de dados através desta API -- uma feature que consegue disparar
// backups e (numa fase futura) PITR precisa deixar rasto de quem pediu
// o quê e quando, para além do que já aparece em `docker logs`.
//
// Limitação conhecida (aceite para este MVP): o log fica só no
// filesystem do container, num caminho que não está montado como volume
// -- sobrevive a um restart do container (o filesystem da imagem em si
// não é recriado), mas não a uma recriação (docker compose up --force-recreate,
// rebuild da imagem). Levar isto para o volume de dados (ou para um
// sistema de log central, já que services/monitoring tem Loki) fica para
// quando o audit log precisar sobreviver a isso.
package audit

import (
	"encoding/json"
	"log/slog"
	"time"
)

type Entry struct {
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail,omitempty"`
	Result string    `json:"result"`
}

// Logger escreve entradas de audit como JSON estruturado no logger
// process-wide (stdout, capturado por `docker logs`, e dali coletável
// pelo Promtail já existente em services/monitoring) -- em vez de um
// segundo arquivo/handler, reaproveita a mesma infraestrutura de logging
// que o resto do processo já usa.
type Logger struct {
	logger *slog.Logger
}

func NewLogger(logger *slog.Logger) *Logger {
	return &Logger{logger: logger}
}

func (l *Logger) Log(actor, action, detail, result string) {
	entry := Entry{Time: time.Now(), Actor: actor, Action: action, Detail: detail, Result: result}
	b, _ := json.Marshal(entry)
	l.logger.Info("audit", "entry", string(b))
}
