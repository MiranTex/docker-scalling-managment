// Package pgbackrest chama a CLI pgbackrest/os scripts já existentes em
// services/database/scripts -- este processo corre DENTRO do mesmo
// container que o Postgres e o pgBackRest (ver entrypoint-wrapper.sh),
// então "integrar" é só invocar os mesmos binários que já estão em
// /usr/local/bin, em vez de reimplementar o que eles já fazem (inclusive
// a queda de privilégio para o user "postgres" via gosu -- ver
// run-backup.sh).
package pgbackrest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Backup é um item de "pgbackrest info --output=json" reduzido aos
// campos que a UI precisa mostrar -- o formato completo do pgBackRest
// tem muito mais detalhe (checksums, tablespaces, ...) que não interessa
// aqui.
type Backup struct {
	Label     string `json:"label"`
	Type      string `json:"type"`
	Timestamp struct {
		Start int64 `json:"start"`
		Stop  int64 `json:"stop"`
	} `json:"timestamp"`
	Info struct {
		Size int64 `json:"size"`
	} `json:"info"`
	Error bool `json:"error"`
}

// StanzaInfo é a stanza inteira devolvida por "pgbackrest info" -- uma
// stanza por cluster Postgres (este serviço só gere uma, a configurada
// em PGBACKREST_STANZA).
type StanzaInfo struct {
	Name   string `json:"name"`
	Status struct {
		Message string `json:"message"`
	} `json:"status"`
	Backups []Backup `json:"backup"`
}

// Client invoca pgbackrest/scripts para a stanza configurada.
type Client struct {
	Stanza string
}

// command monta uma invocação de pgbackrest, baixando privilégio via
// gosu quando este processo corre como root -- pgBackRest recusa-se a
// rodar como root (proteção da própria ferramenta, ver
// scripts/ensure-stanza.sh, que faz exatamente o mesmo). dbadmin é
// lançado em background pelo entrypoint-wrapper.sh enquanto este ainda é
// root, então cai neste caso sempre que corre dentro do container real.
func command(ctx context.Context, args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		return exec.CommandContext(ctx, "gosu", append([]string{"postgres", "pgbackrest"}, args...)...)
	}
	return exec.CommandContext(ctx, "pgbackrest", args...)
}

// Info devolve o estado da stanza (histórico de backups incluído) via
// "pgbackrest info --output=json". Se a stanza ainda não tiver nenhum
// backup, o pgBackRest ainda devolve um objeto válido (Backups vazio) --
// não é considerado erro.
func (c Client) Info(ctx context.Context) (StanzaInfo, error) {
	cmd := command(ctx, "--stanza="+c.Stanza, "--output=json", "info")
	out, err := cmd.Output()
	if err != nil {
		return StanzaInfo{}, fmt.Errorf("pgbackrest info: %w", exitErr(err))
	}

	var stanzas []StanzaInfo
	if err := json.Unmarshal(out, &stanzas); err != nil {
		return StanzaInfo{}, fmt.Errorf("pgbackrest info: decodificando saída: %w", err)
	}
	for _, s := range stanzas {
		if s.Name == c.Stanza {
			return s, nil
		}
	}
	return StanzaInfo{Name: c.Stanza}, nil
}

// ValidType confere se backupType é um dos três tipos que run-backup.sh
// aceita, antes de gastar um exec.Command com um argumento inválido.
func ValidType(backupType string) bool {
	switch backupType {
	case "full", "diff", "incr":
		return true
	default:
		return false
	}
}

// ValidRestoreType confere se restoreType é um dos tipos de alvo que o
// pgBackRest aceita em `restore --type=`. "name" é um ponto de restore
// nomeado (criado via pg_create_restore_point, não usado hoje neste
// projeto, mas é uma opção válida do pgBackRest mesmo assim).
func ValidRestoreType(restoreType string) bool {
	switch restoreType {
	case "time", "xid", "lsn", "name":
		return true
	default:
		return false
	}
}

// Restore corre "pgbackrest --delta restore", equivalente ao que
// scripts/restore-pitr.sh já faz manualmente -- a diferença é que aqui
// não há prompt interativo ("escreve 'sim'"): a confirmação já aconteceu
// do lado da UI (modal com confirmação escrita, ver plano) antes deste
// pedido HTTP sequer existir. --target-action=pause é sempre explícito
// (nunca "promote" direto) -- é a mesma proteção que o Postgres já dá
// por default: aplica o WAL até ao alvo e para, para dar chance de
// inspecionar antes de confirmar (ver Confirm, chamado só depois disso).
//
// PRÉ-REQUISITO: o Postgres já tem de estar parado antes de chamar isto
// -- ver pgsupervisor.Supervisor.Stop, chamado pelo orquestrador em
// internal/httpapi antes de invocar Restore.
func (c Client) Restore(ctx context.Context, restoreType, target string) error {
	args := []string{"--stanza=" + c.Stanza, "--delta", "restore"}
	if restoreType != "" {
		if !ValidRestoreType(restoreType) {
			return fmt.Errorf("tipo de restore inválido: %q", restoreType)
		}
		args = append(args, "--type="+restoreType, "--target="+target, "--target-action=pause")
	}

	cmd := command(ctx, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pgbackrest restore: %w: %s", exitErr(err), string(out))
	}
	return nil
}

// RunBackup invoca run-backup.sh (já existente, ver
// services/database/scripts/run-backup.sh) -- é uma operação longa
// (minutos, dependendo do tamanho da BD), por isso o chamador deve
// correr isto num goroutine e não bloquear o pedido HTTP nele (ver
// internal/backupjob).
func RunBackup(ctx context.Context, backupType string) error {
	if !ValidType(backupType) {
		return fmt.Errorf("tipo de backup inválido: %q", backupType)
	}
	cmd := exec.CommandContext(ctx, "run-backup.sh", backupType)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run-backup.sh %s: %w: %s", backupType, exitErr(err), string(out))
	}
	return nil
}

func exitErr(err error) error {
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("%w (stderr: %s)", err, string(ee.Stderr))
	}
	return err
}

// LastBackupAt é um pequeno helper para o endpoint de status: devolve o
// instante de fim do backup mais recente (qualquer tipo), ou zero se não
// houver nenhum ainda.
func LastBackupAt(info StanzaInfo) time.Time {
	var latest time.Time
	for _, b := range info.Backups {
		t := time.Unix(b.Timestamp.Stop, 0)
		if t.After(latest) {
			latest = t
		}
	}
	return latest
}
