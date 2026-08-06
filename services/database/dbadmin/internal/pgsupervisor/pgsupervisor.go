// Package pgsupervisor arranca e controla o processo do Postgres como
// FILHO do dbadmin, em vez de deixar o Postgres ser o PID 1 do
// container (como a imagem oficial faz sozinha).
//
// Isto existe só por causa do PITR: restaurar um backup exige o Postgres
// completamente parado (pgBackRest reescreve o PGDATA por baixo dele) e
// depois arrancado de novo -- se o Postgres fosse o PID 1, pará-lo
// matava o container inteiro (Docker interpreta a saída do PID 1 como
// "container terminou"), levando o próprio dbadmin com ele a meio de uma
// operação que ainda não tinha terminado. Com o Postgres como filho, o
// dbadmin (agora o PID 1) sobrevive ao ciclo parar/restaurar/arrancar
// inteiro, e continua a servir a API (e a aceitar o pedido de
// confirmação do restore) o tempo todo.
package pgsupervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Supervisor struct {
	command string
	args    []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{} // fechado quando o processo ATUAL termina -- fechar (não enviar) permite vários leitores (Stop e Monitor) sem se roubarem o valor um ao outro
	exitErr error
	stopped bool // true se a última saída foi pedida por nós (Stop), não um crash
}

func New(command string, args []string) *Supervisor {
	return &Supervisor{command: command, args: args}
}

// Start arranca o Postgres em background (não bloqueia) -- stdout/stderr
// vão para os mesmos descritores deste processo, exatamente como
// acontecia quando o Postgres era o PID 1 (é assim que `docker logs`
// continua a mostrar tudo junto).
func (s *Supervisor) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd := exec.Command(s.command, s.args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Grupo de processos próprio: o Postgres cria vários processos
	// backend, mas só precisamos de sinalizar o postmaster (o processo
	// principal, cmd.Process) -- é ele quem propaga o shutdown aos
	// filhos, exatamente como fazia sendo PID 1.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("pgsupervisor: arrancando %s: %w", s.command, err)
	}

	s.cmd = cmd
	s.stopped = false
	s.exitErr = nil
	done := make(chan struct{})
	s.done = done
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.exitErr = err
		s.mu.Unlock()
		close(done)
	}()

	return nil
}

// Stop pede ao Postgres para desligar com "fast shutdown" (SIGINT --
// mesmo sinal que `pg_ctl stop -m fast` usa: desliga já, sem esperar
// clientes desligarem sozinhos, mas ainda faz um checkpoint limpo,
// diferente de SIGQUIT/immediate que não garante isso) e espera até ctx
// expirar. É o que precisamos antes de um restore -- não podemos
// arriscar `pgbackrest restore` a mexer no PGDATA com o Postgres ainda
// vivo por cima dele.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	done := s.done
	if cmd == nil || cmd.Process == nil || done == nil {
		s.mu.Unlock()
		return nil // já não há processo nenhum a parar
	}
	s.stopped = true
	s.mu.Unlock()

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		return fmt.Errorf("pgsupervisor: enviando SIGINT: %w", err)
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("pgsupervisor: Postgres não parou a tempo: %w", ctx.Err())
	}
}

// Signal encaminha um sinal do SO para o Postgres em execução -- usado
// pelo dbadmin no seu próprio handler de SIGTERM/SIGINT (o Docker envia
// o sinal só ao PID 1, que agora é o dbadmin, não o Postgres).
func (s *Supervisor) Signal(sig os.Signal) error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(sig)
}

// Monitor bloqueia (chama-se numa goroutine própria) e invoca
// onUnexpectedExit sempre que o Postgres terminar por conta própria
// (crash), nunca quando a saída foi pedida por Stop() -- é assim que o
// dbadmin sabe distinguir "o Postgres morreu, o container inteiro deve
// morrer com ele" (comportamento que já existia quando o Postgres era o
// PID 1) de "estamos a meio de um restore controlado, continua tudo
// normal". Devolve quando ctx é cancelado.
func (s *Supervisor) Monitor(ctx context.Context, onUnexpectedExit func(error)) {
	for {
		s.mu.Lock()
		done := s.done
		s.mu.Unlock()
		if done == nil {
			if !sleepOrDone(ctx) {
				return
			}
			continue
		}

		select {
		case <-done:
			s.mu.Lock()
			stopped := s.stopped
			err := s.exitErr
			s.mu.Unlock()
			if !stopped {
				onUnexpectedExit(err)
				return
			}
			// Paragem controlada (ex: a meio de um restore) -- espera a
			// PRÓXIMA chamada a Start() substituir este canal antes de
			// voltar a vigiar, para não ficar preso a ler um canal já
			// fechado num ciclo apertado.
			if !s.waitForRestart(ctx, done) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Supervisor) waitForRestart(ctx context.Context, old <-chan struct{}) bool {
	for {
		s.mu.Lock()
		cur := s.done
		s.mu.Unlock()
		if cur != nil && cur != old {
			return true
		}
		if !sleepOrDone(ctx) {
			return false
		}
	}
}

// sleepOrDone espera um curto intervalo de polling, devolvendo false se
// ctx for cancelado entretanto (sinal para quem chamou parar de vigiar).
func sleepOrDone(ctx context.Context) bool {
	select {
	case <-time.After(100 * time.Millisecond):
		return true
	case <-ctx.Done():
		return false
	}
}
