/*-------------------------------------------------------------------------
 *
 * logger.go
 *	  logger.go implements dual-write logging (stdout + file) with log
 *	  rotation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/logger.go
 *
 *-------------------------------------------------------------------------
 */
// logger.go implements dual-write logging (stdout + file) with log rotation.
package drone

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
)

// DualWriter writes to both stdout and a log file simultaneously.
type DualWriter struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	maxSize int64 // max file size before rotation (default 100MB)
}

// NewDualWriter creates a dual writer that writes to stdout + file.
func NewDualWriter(baseDir, role string) (*DualWriter, error) {
	logDir := filepath.Join(baseDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	path := filepath.Join(logDir, fmt.Sprintf("agent-%s.log", role))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return &DualWriter{file: f, path: path, maxSize: 100 * 1024 * 1024}, nil
}

// Write implements io.Writer — writes to both stdout and file.
func (dw *DualWriter) Write(p []byte) (n int, err error) {
	dw.mu.Lock()
	defer dw.mu.Unlock()

	// Write to stdout.
	os.Stdout.Write(p)

	// Check rotation.
	if dw.file != nil {
		info, _ := dw.file.Stat()
		if info != nil && info.Size() > dw.maxSize {
			dw.rotate()
		}
		n, err = dw.file.Write(p)
	}
	return
}

// rotate renames current log file and creates a new one.
func (dw *DualWriter) rotate() {
	if dw.file != nil {
		dw.file.Close()
	}

	rotated := fmt.Sprintf("%s.%s", dw.path, time.Now().Format("20060102_150405"))
	os.Rename(dw.path, rotated)

	f, err := os.OpenFile(dw.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		dw.file = f
	}

	// Keep only last 5 rotated files.
	go cleanOldLogs(filepath.Dir(dw.path), 5)
}

// Close closes the log file.
func (dw *DualWriter) Close() error {
	dw.mu.Lock()
	defer dw.mu.Unlock()
	if dw.file != nil {
		return dw.file.Close()
	}
	return nil
}

// SetupDualLogging redirects os.Stdout and os.Stderr to dual writer.
func SetupDualLogging(baseDir, role string) (io.Closer, error) {
	dw, err := NewDualWriter(baseDir, role)
	if err != nil {
		return nil, err
	}
	// Redirect fmt.Printf etc. by replacing os.Stdout/Stderr not possible in Go,
	// but we can use log package. For now, agent code uses fmt.Printf which goes to stdout.
	// The DualWriter is available for explicit use.
	cluster.DefaultLogger("logger").Info("Log file ready", slog.String("path", dw.path), slog.String("rotation", "100MB"))
	return dw, nil
}

func cleanOldLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var logFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) != ".log" {
			logFiles = append(logFiles, filepath.Join(dir, e.Name()))
		}
	}

	if len(logFiles) > keep {
		for _, f := range logFiles[:len(logFiles)-keep] {
			os.Remove(f)
		}
	}
}
