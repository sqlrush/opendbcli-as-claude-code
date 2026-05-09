/*-------------------------------------------------------------------------
 *
 * audit.go
 *	  AuditEntry represents a single audit log record.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/security/audit.go
 *
 *-------------------------------------------------------------------------
 */
package security

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// AuditEntry represents a single audit log record.
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	User      string    `json:"user"`
	Instance  string    `json:"instance"`
	Command   string    `json:"command"`
	Level     Level     `json:"level"`
	Result    string    `json:"result"`
}

// MarshalJSON provides custom JSON serialization for AuditEntry.
func (e AuditEntry) MarshalJSON() ([]byte, error) {
	type Alias AuditEntry
	return json.Marshal(&struct {
		Alias
		Level string `json:"level"`
	}{
		Alias: Alias(e),
		Level: e.Level.String(),
	})
}

// AuditLogger provides append-only logging of audit entries.
type AuditLogger struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewAuditLogger creates a new AuditLogger that writes to the specified file path.
// The file is opened in append-only mode and created if it does not exist.
func NewAuditLogger(filePath string) (*AuditLogger, error) {
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log file %q: %w", filePath, err)
	}
	return &AuditLogger{writer: f}, nil
}

// NewAuditLoggerWithWriter creates a new AuditLogger that writes to the given writer.
// Useful for testing with in-memory buffers.
func NewAuditLoggerWithWriter(w io.Writer) *AuditLogger {
	return &AuditLogger{writer: w}
}

// Log writes an audit entry as a JSON line to the audit log.
// Thread-safe: multiple goroutines can call Log concurrently.
func (a *AuditLogger) Log(entry AuditEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal audit entry: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	line := append(data, '\n')
	if _, writeErr := a.writer.Write(line); writeErr != nil {
		return fmt.Errorf("failed to write audit entry: %w", writeErr)
	}

	return nil
}
