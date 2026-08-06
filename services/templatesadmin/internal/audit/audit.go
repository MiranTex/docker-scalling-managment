// Package audit regista, uma linha por ação, quem fez o quê contra os
// modelos de serviço geridos por esta API -- mesmo padrão de
// services/secretsadmin/internal/audit e services/database/dbadmin.
//
// Limitação conhecida (aceite, mesmo motivo do secretsadmin): o log fica
// só no filesystem do container (stdout, capturado por `docker logs`),
// não num volume -- sobrevive a um restart do container, não a uma
// recriação.
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
