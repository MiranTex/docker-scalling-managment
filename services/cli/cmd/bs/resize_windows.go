//go:build windows

package main

// No Windows não há SIGWINCH: o tamanho fica o do arranque.
func onResize(func()) (stop func()) { return func() {} }
