/*-------------------------------------------------------------------------
 *
 * audit_test.go
 *	  Test cases for audit.go (security package): TestAuditLog_Write,
 *	  TestAuditLog_MultipleEntries.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/security/audit_test.go
 *
 *-------------------------------------------------------------------------
 */
package security

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAuditLog_Write(t *testing.T) {
	var buf bytes.Buffer
	logger := NewAuditLoggerWithWriter(&buf)

	entry := AuditEntry{
		Timestamp: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
		User:      "dba_admin",
		Instance:  "prod-oracle-01",
		Command:   "SELECT * FROM v$session",
		Level:     LevelReadOnly,
		Result:    "success",
	}

	err := logger.Log(entry)
	if err != nil {
		t.Fatalf("unexpected error writing audit log: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("audit log output should not be empty")
	}

	// Verify it ends with a newline (JSON lines format).
	if !strings.HasSuffix(output, "\n") {
		t.Error("audit log entry should end with a newline")
	}

	// Parse the JSON back and verify fields.
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); err != nil {
		t.Fatalf("failed to parse audit log JSON: %v", err)
	}

	if parsed["user"] != "dba_admin" {
		t.Errorf("expected user 'dba_admin', got %v", parsed["user"])
	}
	if parsed["instance"] != "prod-oracle-01" {
		t.Errorf("expected instance 'prod-oracle-01', got %v", parsed["instance"])
	}
	if parsed["command"] != "SELECT * FROM v$session" {
		t.Errorf("expected command 'SELECT * FROM v$session', got %v", parsed["command"])
	}
	if parsed["level"] != "readonly" {
		t.Errorf("expected level 'readonly', got %v", parsed["level"])
	}
	if parsed["result"] != "success" {
		t.Errorf("expected result 'success', got %v", parsed["result"])
	}
}

func TestAuditLog_MultipleEntries(t *testing.T) {
	var buf bytes.Buffer
	logger := NewAuditLoggerWithWriter(&buf)

	entries := []AuditEntry{
		{
			Timestamp: time.Now(),
			User:      "user1",
			Instance:  "dev-01",
			Command:   "SELECT 1",
			Level:     LevelReadOnly,
			Result:    "success",
		},
		{
			Timestamp: time.Now(),
			User:      "user2",
			Instance:  "dev-01",
			Command:   "DROP TABLE test",
			Level:     LevelDangerous,
			Result:    "denied",
		},
	}

	for _, entry := range entries {
		if err := logger.Log(entry); err != nil {
			t.Fatalf("unexpected error writing audit log: %v", err)
		}
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}

	// Verify each line is valid JSON.
	for i, line := range lines {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}
