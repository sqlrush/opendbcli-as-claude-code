/*-------------------------------------------------------------------------
 *
 * logger_test.go
 *	  Test cases for logger.go (logger package):
 *	  TestInit_CreatesLogFile, TestInit_CreatesNestedDirectories,
 *	  TestLogOutput_ContainsExpectedFields.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/logger/logger_test.go
 *
 *-------------------------------------------------------------------------
 */
package logger

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_CreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	err := Init(logDir, slog.LevelDebug)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { Close() })

	logPath := filepath.Join(logDir, "opendb.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("log file not created at %q", logPath)
	}
}

func TestInit_CreatesNestedDirectories(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "deep", "nested", "logs")

	err := Init(logDir, slog.LevelInfo)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { Close() })

	logPath := filepath.Join(logDir, "opendb.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("log file not created at %q", logPath)
	}
}

func TestLogOutput_ContainsExpectedFields(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	err := Init(logDir, slog.LevelDebug)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { Close() })

	Info("test info message", "key1", "value1")
	Warn("test warn message", "key2", 42)
	Error("test error message", "err", "something broke")
	Debug("test debug message", "detail", true)

	// Close to flush
	if err := Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	logPath := filepath.Join(logDir, "opendb.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}

	content := string(data)

	checks := []struct {
		label string
		want  string
	}{
		{"info message", "test info message"},
		{"warn message", "test warn message"},
		{"error message", "test error message"},
		{"debug message", "test debug message"},
		{"info level", `"level":"INFO"`},
		{"warn level", `"level":"WARN"`},
		{"error level", `"level":"ERROR"`},
		{"debug level", `"level":"DEBUG"`},
		{"key1 value", `"key1":"value1"`},
		{"key2 value", `"key2":42`},
		{"err value", `"err":"something broke"`},
		{"detail value", `"detail":true`},
	}

	for _, c := range checks {
		if !strings.Contains(content, c.want) {
			t.Errorf("log output missing %s (%q); got:\n%s", c.label, c.want, content)
		}
	}
}

func TestLogOutput_RespectsLevel(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")

	err := Init(logDir, slog.LevelWarn)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { Close() })

	Info("should not appear")
	Debug("should not appear either")
	Warn("should appear")
	Error("should also appear")

	if err := Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	logPath := filepath.Join(logDir, "opendb.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}

	content := string(data)

	if strings.Contains(content, "should not appear") {
		t.Errorf("log output should not contain INFO/DEBUG messages at WARN level; got:\n%s", content)
	}
	if !strings.Contains(content, "should appear") {
		t.Errorf("log output missing WARN message; got:\n%s", content)
	}
	if !strings.Contains(content, "should also appear") {
		t.Errorf("log output missing ERROR message; got:\n%s", content)
	}
}

func TestClose_SafeWithoutInit(t *testing.T) {
	// Ensure Close is safe to call even if Init was never called
	err := Close()
	if err != nil {
		t.Errorf("Close without Init should not error, got: %v", err)
	}
}

func TestReinit_ClosesOldFile(t *testing.T) {
	dir := t.TempDir()
	logDir1 := filepath.Join(dir, "logs1")
	logDir2 := filepath.Join(dir, "logs2")

	err := Init(logDir1, slog.LevelInfo)
	if err != nil {
		t.Fatalf("first Init failed: %v", err)
	}

	Info("message in first log")

	err = Init(logDir2, slog.LevelInfo)
	if err != nil {
		t.Fatalf("second Init failed: %v", err)
	}
	t.Cleanup(func() { Close() })

	Info("message in second log")

	if err := Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	data2, err := os.ReadFile(filepath.Join(logDir2, "opendb.log"))
	if err != nil {
		t.Fatalf("reading second log file: %v", err)
	}
	if !strings.Contains(string(data2), "message in second log") {
		t.Error("second log file should contain the second message")
	}
}
