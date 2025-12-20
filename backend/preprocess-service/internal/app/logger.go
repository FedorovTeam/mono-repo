package app

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger создаёт текстовый slog-логгер с уровнем из конфига.
func NewLogger(level string) *slog.Logger {
	slogLevel := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel})
	return slog.New(handler)
}
