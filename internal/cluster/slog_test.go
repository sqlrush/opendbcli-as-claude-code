/*-------------------------------------------------------------------------
 *
 * slog_test.go
 *	  Test cases for slog.go (cluster package):
 *	  TestNewLogger_JSONFormat, TestNewLogger_ComponentField,
 *	  TestNewLogger_AllLevels.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/slog_test.go
 *
 *-------------------------------------------------------------------------
 */
package cluster

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestNewLogger_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("drone", &buf)
	logger.Info("test message", slog.String("key", "value"))

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	if record["msg"] != "test message" {
		t.Errorf("msg = %v, want %q", record["msg"], "test message")
	}
	if record["component"] != "drone" {
		t.Errorf("component = %v, want %q", record["component"], "drone")
	}
	if record["key"] != "value" {
		t.Errorf("key = %v, want %q", record["key"], "value")
	}
	if _, ok := record["time"]; !ok {
		t.Error("missing time field in JSON output")
	}
	if _, ok := record["level"]; !ok {
		t.Error("missing level field in JSON output")
	}
}

func TestNewLogger_ComponentField(t *testing.T) {
	components := []string{"drone", "overlord", "cerebrate", "grpc"}
	for _, comp := range components {
		var buf bytes.Buffer
		logger := NewLogger(comp, &buf)
		logger.Info("hello")

		var record map[string]any
		if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
			t.Fatalf("component=%s: invalid JSON: %v", comp, err)
		}
		if record["component"] != comp {
			t.Errorf("component=%s: got %v", comp, record["component"])
		}
	}
}

func TestNewLogger_AllLevels(t *testing.T) {
	tests := []struct {
		name  string
		logFn func(*slog.Logger, string, ...any)
		level string
	}{
		{"debug", (*slog.Logger).Debug, "DEBUG"},
		{"info", (*slog.Logger).Info, "INFO"},
		{"warn", (*slog.Logger).Warn, "WARN"},
		{"error", (*slog.Logger).Error, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := NewLogger("test", &buf)
			tt.logFn(logger, "msg-"+tt.name)

			var record map[string]any
			if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
				t.Fatalf("invalid JSON: %v\nraw: %s", err, buf.String())
			}
			if record["level"] != tt.level {
				t.Errorf("level = %v, want %q", record["level"], tt.level)
			}
		})
	}
}

func TestNewLogger_ConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("concurrent", &buf)

	const goroutines = 10
	const messagesPerGoroutine = 50
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				logger.Info("concurrent msg", slog.Int("goroutine", id), slog.Int("seq", j))
			}
		}(i)
	}
	wg.Wait()

	// Each log line is a valid JSON object.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	total := goroutines * messagesPerGoroutine
	if len(lines) != total {
		t.Errorf("expected %d lines, got %d", total, len(lines))
	}
	for i, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Errorf("line %d: invalid JSON: %v\nraw: %s", i, err, line)
		}
	}
}

func TestWithFields_CreatesNewLogger(t *testing.T) {
	var buf bytes.Buffer
	base := NewLogger("drone", &buf)

	extended := WithFields(base, slog.String("worker_id", "w-001"), slog.String("region", "east"))
	extended.Info("with fields")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if record["component"] != "drone" {
		t.Errorf("component = %v", record["component"])
	}
	if record["worker_id"] != "w-001" {
		t.Errorf("worker_id = %v", record["worker_id"])
	}
	if record["region"] != "east" {
		t.Errorf("region = %v", record["region"])
	}
}

func TestWithFields_DoesNotMutateOriginal(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	base := NewLogger("test", &buf1)

	// Create extended logger with separate buffer to verify independence.
	_ = WithFields(base, slog.String("extra", "yes"))

	base.Info("original")
	output := buf1.String()
	if strings.Contains(output, "extra") {
		t.Error("WithFields mutated original logger")
	}

	// Extended logger should have the extra field.
	extended := WithFields(NewLogger("test", &buf2), slog.String("extra", "yes"))
	extended.Info("extended")
	if !strings.Contains(buf2.String(), "extra") {
		t.Error("extended logger missing extra field")
	}
}

func TestDefaultLogger_WritesToStderr(t *testing.T) {
	// Just verify it returns a non-nil logger without panicking.
	logger := DefaultLogger("cerebrate")
	if logger == nil {
		t.Fatal("DefaultLogger returned nil")
	}
}

func TestNewLogger_ChineseMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("drone", &buf)
	logger.Warn("心跳超时", slog.String("worker", "w-001"))

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if record["msg"] != "心跳超时" {
		t.Errorf("msg = %v, want %q", record["msg"], "心跳超时")
	}
}
