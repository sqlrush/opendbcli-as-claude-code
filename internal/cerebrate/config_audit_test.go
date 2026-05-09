/*-------------------------------------------------------------------------
 *
 * config_audit_test.go
 *	  Test cases for config_audit.go (cerebrate package):
 *	  TestConfigDriftDetection, TestConfigNoDrift,
 *	  TestConfigDriftReportMarkdown.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/config_audit_test.go
 *
 *-------------------------------------------------------------------------
 */
package cerebrate

import (
	"testing"
)

func TestConfigDriftDetection(t *testing.T) {
	fm := NewFleetManager("test-cerebrate")
	audit := NewConfigAuditManager(fm)

	// Manually set snapshots with drift
	audit.lastSnapshots = map[string]*ConfigSnapshot{
		"overlord-1": {
			OverlordID: "overlord-1",
			Region:     "us-east",
			Parameters: map[string]string{
				"shared_buffers": "4GB",
				"work_mem":       "64MB",
				"max_connections": "200",
			},
		},
		"overlord-2": {
			OverlordID: "overlord-2",
			Region:     "eu-west",
			Parameters: map[string]string{
				"shared_buffers": "8GB",  // different!
				"work_mem":       "64MB", // same
				"max_connections": "500", // different!
			},
		},
	}

	report := audit.DetectDrift()

	if report.RegionCount != 2 {
		t.Errorf("expected 2 regions, got %d", report.RegionCount)
	}

	if len(report.Drifts) != 2 {
		t.Fatalf("expected 2 drifts, got %d", len(report.Drifts))
	}

	// Both shared_buffers and max_connections should be detected as drifted
	driftNames := make(map[string]bool)
	for _, d := range report.Drifts {
		driftNames[d.ParamName] = true
	}
	if !driftNames["shared_buffers"] {
		t.Error("expected shared_buffers drift")
	}
	if !driftNames["max_connections"] {
		t.Error("expected max_connections drift")
	}

	// Both are critical params
	for _, d := range report.Drifts {
		if !d.IsCritical {
			t.Errorf("expected %s to be critical", d.ParamName)
		}
	}
}

func TestConfigNoDrift(t *testing.T) {
	fm := NewFleetManager("test-cerebrate")
	audit := NewConfigAuditManager(fm)

	// Same values everywhere
	audit.lastSnapshots = map[string]*ConfigSnapshot{
		"overlord-1": {
			Parameters: map[string]string{"shared_buffers": "4GB"},
		},
		"overlord-2": {
			Parameters: map[string]string{"shared_buffers": "4GB"},
		},
	}

	report := audit.DetectDrift()
	if len(report.Drifts) != 0 {
		t.Errorf("expected no drifts, got %d", len(report.Drifts))
	}
}

func TestConfigDriftReportMarkdown(t *testing.T) {
	fm := NewFleetManager("test-cerebrate")
	audit := NewConfigAuditManager(fm)

	audit.lastSnapshots = map[string]*ConfigSnapshot{
		"overlord-1": {
			Parameters: map[string]string{"shared_buffers": "4GB", "timezone": "UTC"},
		},
		"overlord-2": {
			Parameters: map[string]string{"shared_buffers": "8GB", "timezone": "US/Eastern"},
		},
	}

	report := audit.DetectDrift()
	md := audit.GenerateDriftReport(report)

	if len(md) == 0 {
		t.Error("expected non-empty report")
	}
	if report.TotalParams != 2 {
		t.Errorf("expected 2 total params, got %d", report.TotalParams)
	}
}

func TestConfigDriftHistory(t *testing.T) {
	fm := NewFleetManager("test-cerebrate")
	audit := NewConfigAuditManager(fm)

	if audit.LastDriftReport() != nil {
		t.Error("expected nil initial drift report")
	}

	audit.lastSnapshots = map[string]*ConfigSnapshot{
		"o1": {Parameters: map[string]string{"x": "1"}},
		"o2": {Parameters: map[string]string{"x": "2"}},
	}

	audit.DetectDrift()
	if audit.LastDriftReport() == nil {
		t.Error("expected non-nil drift report after DetectDrift")
	}
}

func TestHasDrift(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{"empty", map[string]string{}, false},
		{"single", map[string]string{"a": "1"}, false},
		{"same", map[string]string{"a": "1", "b": "1"}, false},
		{"different", map[string]string{"a": "1", "b": "2"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasDrift(tt.values); got != tt.want {
				t.Errorf("hasDrift() = %v, want %v", got, tt.want)
			}
		})
	}
}
