package main

import (
	"log/slog"
	"os"
	"strings"
)

// setupLogger monta o logger estruturado do processo a partir de env vars e
// o registra como slog.Default(), pra que qualquer pacote (ex:
// internal/loadbalancer) que só chame slog.Info/slog.Error direto já saia
// no mesmo formato, sem precisar receber o logger explicitamente.
//
// LOG_FORMAT=json (default) é o que interessa em produção: cada linha é um
// objeto, fácil de agregar/filtrar num coletor de logs. LOG_FORMAT=text usa
// um formato legível em texto, melhor pra rodar local no terminal.
//
// LOG_LEVEL controla o nível mínimo (debug, info, warn, error); default info.
func setupLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(os.Getenv("LOG_LEVEL"))}

	var handler slog.Handler
	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "text") {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func parseLevel(v string) slog.Level {
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
