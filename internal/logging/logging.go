// Package logging builds the application's structured logger.
package logging

import (
	"log/slog"
	"os"
)

// New returns a slog.Logger writing to stderr in the given format ("text" or
// "json") at the given level ("debug", "info", "warn", "error").
func New(level, format string, attrs ...any) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(h).With(attrs...)
}
