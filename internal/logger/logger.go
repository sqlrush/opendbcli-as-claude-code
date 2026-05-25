/*-------------------------------------------------------------------------
 *
 * logger.go
 *	  Init sets up file-based structured logging at the given directory
 *	  and level. The log file is created at logDir/opendb.log. Parent
 *	  directories are created if they do not exist.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/logger/logger.go
 *
 *-------------------------------------------------------------------------
 */
package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

var (
	instance *slog.Logger
	mu       sync.RWMutex
	logFile  *os.File
)

// Init sets up file-based structured logging at the given directory and level.
// The log file is created at logDir/opendb.log. Parent directories are created
// if they do not exist.
func Init(logDir string, level slog.Level) error {
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(logDir, 0750); err != nil {
		return fmt.Errorf("creating log directory %q: %w", logDir, err)
	}

	logPath := filepath.Join(logDir, "opendb.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return fmt.Errorf("opening log file %q: %w", logPath, err)
	}

	// Close previous log file if one was open
	if logFile != nil {
		logFile.Close()
	}
	logFile = f

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: level,
	})
	instance = slog.New(handler)

	return nil
}

// Close closes the underlying log file. Safe to call even if Init was never called.
func Close() error {
	mu.Lock()
	defer mu.Unlock()

	if logFile != nil {
		err := logFile.Close()
		logFile = nil
		instance = nil
		return err
	}
	return nil
}

// logger returns the current logger instance, falling back to the default
// slog logger if Init has not been called.
func logger() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()

	if instance != nil {
		return instance
	}
	return slog.Default()
}

// Info logs a message at INFO level with optional structured key-value pairs.
func Info(msg string, args ...any) {
	logger().Info(msg, args...)
}

// Warn logs a message at WARN level with optional structured key-value pairs.
func Warn(msg string, args ...any) {
	logger().Warn(msg, args...)
}

// Error logs a message at ERROR level with optional structured key-value pairs.
func Error(msg string, args ...any) {
	logger().Error(msg, args...)
}

// Debug logs a message at DEBUG level with optional structured key-value pairs.
func Debug(msg string, args ...any) {
	logger().Debug(msg, args...)
}
