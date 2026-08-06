package main

import (
	"sync"
	"testing"
	"time"
)

// TestDrainSetClear confirma que clear() esvazia o conjunto -- usado por
// restartGroup depois de terminateAllReplicas, para não acumular entradas
// de containers que já não existem.
func TestDrainSetClear(t *testing.T) {
	d := newDrainSet()
	d.add("a")
	d.add("b")
	if d.len() != 2 {
		t.Fatalf("len = %d, want 2 antes de clear", d.len())
	}

	d.clear()
	if d.len() != 0 {
		t.Fatalf("len = %d, want 0 depois de clear", d.len())
	}
	if d.contains("a") {
		t.Fatalf("contains(\"a\") = true depois de clear")
	}
}

// TestRestartMuSerializesAgainstReconcile testa a disciplina de lock em
// isolamento (sem Docker real): confirma que quem detém restartMu bloqueia
// de verdade uma segunda goroutine tentando adquiri-lo, e que ela só avança
// depois do primeiro largar o lock -- é essa serialização que impede
// restartGroup e reconcile() de correrem ao mesmo tempo sobre o mesmo
// conjunto de réplicas.
func TestRestartMuSerializesAgainstReconcile(t *testing.T) {
	var restartMu sync.Mutex
	started := make(chan struct{})
	acquired := make(chan struct{})

	restartMu.Lock()
	go func() {
		close(started)
		restartMu.Lock()
		close(acquired)
		restartMu.Unlock()
	}()

	<-started
	select {
	case <-acquired:
		t.Fatalf("segunda goroutine adquiriu restartMu antes da primeira largar")
	case <-time.After(50 * time.Millisecond):
		// esperado: ainda bloqueada
	}

	restartMu.Unlock()

	select {
	case <-acquired:
		// esperado: destravou depois do Unlock
	case <-time.After(time.Second):
		t.Fatalf("segunda goroutine nunca adquiriu restartMu depois do Unlock")
	}
}
