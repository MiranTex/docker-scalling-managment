//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// onResize chama f sempre que a janela do terminal muda de tamanho.
func onResize(f func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				f()
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
