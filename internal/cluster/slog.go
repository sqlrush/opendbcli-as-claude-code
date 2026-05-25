/*-------------------------------------------------------------------------
 *
 * slog.go
 *	  slog.go provides a structured JSON logger factory for cluster
 *	  components. Each component (drone, overlord, cerebrate) gets a
 *	  logger with a "component" default attribute. Output goes to an
 *	  io.Writer (typically DualWriter for both stdout and file).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/slog.go
 *
 *-------------------------------------------------------------------------
 */
// slog.go provides a structured JSON logger factory for cluster components.
// Each component (drone, overlord, cerebrate) gets a logger with a "component"
// default attribute. Output goes to an io.Writer (typically DualWriter for
// both stdout and file).
package cluster

import (
	"io"
	"log/slog"
	"os"
)

// NewLogger creates a structured JSON logger for the given component.
// The component name is embedded as a default attribute in every log line.
// output is the io.Writer that receives JSON log lines (e.g., DualWriter).
func NewLogger(component string, output io.Writer) *slog.Logger {
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	return slog.New(handler).With(slog.String("component", component))
}

// DefaultLogger returns a logger writing to stderr with the given component.
// Use this when no DualWriter is available (e.g., early startup, tests).
func DefaultLogger(component string) *slog.Logger {
	return NewLogger(component, os.Stderr)
}

// WithFields returns a new logger with the given additional attributes.
// The original logger is not modified (immutable pattern).
func WithFields(logger *slog.Logger, attrs ...slog.Attr) *slog.Logger {
	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	return logger.With(args...)
}
