/*-------------------------------------------------------------------------
 *
 * audit.go
 *	  AuditLogger writes append-only audit log entries for database
 *	  write operations. Each entry is HMAC-SHA256 chain-hashed to detect
 *	  tampering.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/audit.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	auditLogFile = "audit.log"
	auditKeyFile = "audit.key"
	auditKeyLen  = 32
	hashPrefix   = " | hash:"
)

// AuditLogger writes append-only audit log entries for database write operations.
// Each entry is HMAC-SHA256 chain-hashed to detect tampering.
type AuditLogger struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	hmacKey  []byte
	prevHash string
}

// NewAuditLogger creates an audit logger at the given baseDir.
// Loads or auto-generates the HMAC key, and initializes prevHash from the last log entry.
func NewAuditLogger(baseDir string) (*AuditLogger, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	key, err := loadOrGenerateKey(filepath.Join(baseDir, auditKeyFile))
	if err != nil {
		return nil, fmt.Errorf("load audit key: %w", err)
	}
	return newAuditLogger(baseDir, key)
}

// NewAuditLoggerWithKey creates an audit logger using the provided HMAC key (for testing).
func NewAuditLoggerWithKey(baseDir string, key []byte) (*AuditLogger, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	return newAuditLogger(baseDir, key)
}

// newAuditLogger is the shared constructor that opens the log file and reads prevHash.
func newAuditLogger(baseDir string, key []byte) (*AuditLogger, error) {
	logPath := filepath.Join(baseDir, auditLogFile)
	prevHash, err := readLastHash(logPath)
	if err != nil {
		return nil, fmt.Errorf("read last hash: %w", err)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	return &AuditLogger{
		path:     logPath,
		file:     f,
		hmacKey:  key,
		prevHash: prevHash,
	}, nil
}

// Log writes a single audit entry with HMAC chain hash. Thread-safe.
func (a *AuditLogger) Log(role, target, operation, reason, result string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entryText := fmt.Sprintf("%s | %s | %s | %s | reason: %s | result: %s",
		time.Now().Format(time.RFC3339),
		role,
		target,
		operation,
		reason,
		result,
	)
	h := computeHMAC(a.hmacKey, a.prevHash, entryText)
	line := entryText + hashPrefix + h + "\n"

	if _, err := a.file.WriteString(line); err != nil {
		return err
	}
	a.prevHash = h
	return nil
}

// Close closes the underlying file.
func (a *AuditLogger) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		return a.file.Close()
	}
	return nil
}

// computeHMAC returns the hex-encoded HMAC-SHA256 of prevHash + entryText.
func computeHMAC(key []byte, prevHash, entryText string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(prevHash + entryText))
	return hex.EncodeToString(mac.Sum(nil))
}

// loadOrGenerateKey reads the HMAC key from keyPath, or generates a new one if missing.
func loadOrGenerateKey(keyPath string) ([]byte, error) {
	data, err := os.ReadFile(keyPath)
	if err == nil {
		return decodeHexKey(strings.TrimSpace(string(data)))
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	return generateKey(keyPath)
}

// generateKey creates a new random HMAC key and writes it with 0600 permissions.
func generateKey(keyPath string) ([]byte, error) {
	key := make([]byte, auditKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate random key: %w", err)
	}
	encoded := hex.EncodeToString(key)
	if err := os.WriteFile(keyPath, []byte(encoded+"\n"), 0600); err != nil {
		return nil, fmt.Errorf("write key file: %w", err)
	}
	return key, nil
}

// decodeHexKey decodes a hex-encoded key string.
func decodeHexKey(s string) ([]byte, error) {
	key, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode hex key: %w", err)
	}
	return key, nil
}

// readLastHash reads the last line of the audit log and extracts the hash suffix.
// Returns "" if the file is empty or does not exist (genesis state).
func readLastHash(logPath string) (string, error) {
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open log for reading: %w", err)
	}
	defer f.Close()
	return extractLastHash(f)
}

// extractLastHash scans the file for the last non-empty line and extracts its hash.
func extractLastHash(f *os.File) (string, error) {
	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lastLine = line
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan audit log: %w", err)
	}
	if lastLine == "" {
		return "", nil
	}
	return extractHash(lastLine), nil
}

// extractHash returns the hash from a line formatted as "... | hash:<hex>".
// Returns "" if no hash suffix is found (legacy lines).
func extractHash(line string) string {
	idx := strings.LastIndex(line, hashPrefix)
	if idx < 0 {
		return ""
	}
	return line[idx+len(hashPrefix):]
}
