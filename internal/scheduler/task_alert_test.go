/*-------------------------------------------------------------------------
 *
 * task_alert_test.go
 *	  Test cases for task_alert.go (scheduler package):
 *	  TestClassifyORAError, TestAlertDedup, TestFormatAlertRecords.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/task_alert_test.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"testing"
	"time"
)

func TestClassifyORAError(t *testing.T) {
	tests := []struct {
		message  string
		wantCode string
		wantSev  string
	}{
		{"ORA-00060: deadlock detected", "ORA-00060", "WARN"},
		{"ORA-04031: unable to allocate 4096 bytes", "ORA-04031", "CRITICAL"},
		{"ORA-00600: internal error code", "ORA-00600", "CRITICAL"},
		{"ORA-01652: unable to extend temp segment", "ORA-01652", "CRITICAL"},
		{"ORA-12345: unknown error", "ORA-12345", "INFO"},
		{"No error here", "", ""},
	}

	for _, tt := range tests {
		name := tt.message
		if len(name) > 20 {
			name = name[:20]
		}
		t.Run(name, func(t *testing.T) {
			records := ClassifyORAError(tt.message)
			if tt.wantCode == "" {
				if len(records) != 0 {
					t.Errorf("expected no records, got %d", len(records))
				}
				return
			}
			if len(records) == 0 {
				t.Fatalf("expected records for %q, got none", tt.message)
			}
			found := false
			for _, r := range records {
				if r.ORACode == tt.wantCode && r.Severity == tt.wantSev {
					found = true
				}
			}
			if !found {
				t.Errorf("expected code=%s sev=%s, got %+v", tt.wantCode, tt.wantSev, records)
			}
		})
	}
}

func TestAlertDedup(t *testing.T) {
	dedup := NewAlertDedup(5 * time.Minute)
	now := time.Now()

	// First alert should pass.
	if !dedup.ShouldAlert("ORA-00060", now) {
		t.Error("first alert should pass")
	}

	// Same code within window should be suppressed.
	if dedup.ShouldAlert("ORA-00060", now.Add(2*time.Minute)) {
		t.Error("second alert within window should be suppressed")
	}

	// Different code should pass.
	if !dedup.ShouldAlert("ORA-04031", now.Add(2*time.Minute)) {
		t.Error("different code should pass")
	}

	// Same code after window should pass.
	if !dedup.ShouldAlert("ORA-00060", now.Add(6*time.Minute)) {
		t.Error("alert after window should pass")
	}
}

func TestFormatAlertRecords(t *testing.T) {
	records := []AlertRecord{
		{ORACode: "ORA-00060", Severity: "WARN", Message: "deadlock detected"},
	}
	s := FormatAlertRecords(records)
	if s == "" {
		t.Error("expected non-empty output")
	}

	// No records.
	s = FormatAlertRecords(nil)
	if s != "" {
		t.Errorf("expected empty for no records, got %q", s)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Error("short string should not be truncated")
	}
	long := "this is a very long string that exceeds the limit"
	result := truncate(long, 20)
	if len(result) > 20 {
		t.Errorf("expected max 20 chars, got %d", len(result))
	}
}
