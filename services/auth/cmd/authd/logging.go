package main

import (
	"log/slog"
	"os"
	"strings"
)

// setupLogger monta o logger estruturado do processo a partir de env vars e
// o regista como slog.Default() -- mesmo padrão usado no
// services/autoscaler/cmd/group, pra manter os logs de todos os serviços
// do base-stack consistentes (mesmo formato JSON, mesma convenção de
// LOG_LEVEL).
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
