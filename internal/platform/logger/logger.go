// Package logger builds the process-wide structured logger.
package logger

import (
	"log/slog"
	"os"
)

// New returns a JSON logger in production (machine-parseable) and a
// human-readable text logger otherwise.
func New(env string) *slog.Logger {
	level := slog.LevelInfo
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
