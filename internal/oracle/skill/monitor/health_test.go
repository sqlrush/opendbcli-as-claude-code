/*-------------------------------------------------------------------------
 *
 * health_test.go
 *	  Test cases for health.go (monitor package):
 *	  TestHealthSkill_Metadata, TestHealthSkill_Execute_ReturnsReport,
 *	  TestHealthSkill_Execute_WithData.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/health_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestHealthSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewHealthSkill(drv)

	if s.Name() != "health" {
		t.Errorf("Name() = %q, want %q", s.Name(), "health")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestHealthSkill_Execute_ReturnsReport(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.ServerInfoVal = db.ServerInfo{
		InstanceName: "testdb",
		Version:      "19.3.0.0.0",
	}
	// Default mock returns empty QueryResult for all queries — checks should handle gracefully.
	s := NewHealthSkill(drv)
	result, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != skill.ResultText {
		t.Errorf("Type = %v, want ResultText", result.Type)
	}
	// Should contain the header.
	if !strings.Contains(result.Rendered, "testdb") {
		t.Error("rendered output should contain instance name")
	}
	// Should contain OK/WARN/FAIL summary.
	if !strings.Contains(result.Summary, "OK") {
		t.Error("summary should contain OK count")
	}
	// Should have 24 check items.
	hr, ok := result.Data.(*HealthReport)
	if !ok {
		t.Fatal("Data should be *HealthReport")
	}
	if len(hr.Checks) != 24 {
		t.Errorf("got %d checks, want 24", len(hr.Checks))
	}
}

func TestHealthSkill_Execute_WithData(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.ServerInfoVal = db.ServerInfo{
		InstanceName: "prod",
		Version:      "19.3.0.0.0",
	}
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		// Return some data for key checks.
		if strings.Contains(sql, "startup_time") {
			return &db.QueryResult{Columns: []string{"M"}, Rows: [][]any{{float64(1440)}}}, nil
		}
		if strings.Contains(sql, "database_role") {
			return &db.QueryResult{Columns: []string{"R", "S"}, Rows: [][]any{{"PRIMARY", "ARCHIVELOG"}}}, nil
		}
		if strings.Contains(sql, "v$session") && strings.Contains(sql, "COUNT") {
			return &db.QueryResult{Columns: []string{"C", "M"}, Rows: [][]any{{float64(50), float64(500)}}}, nil
		}
		return &db.QueryResult{}, nil
	}

	s := NewHealthSkill(drv)
	result, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Rendered, "UP") {
		t.Error("should show instance UP")
	}
	if !strings.Contains(result.Rendered, "PRIMARY") {
		t.Error("should show database role")
	}
}

func TestStatusLabel(t *testing.T) {
	tests := []struct {
		status   checkStatus
		contains string
	}{
		{statusOK, "OK"},
		{statusWarn, "WARN"},
		{statusFail, "FAIL"},
		{checkStatus(99), "????"},
	}

	for _, tt := range tests {
		got := tt.status.statusLabel()
		if !strings.Contains(got, tt.contains) {
			t.Errorf("statusLabel(%d) = %q, want containing %q", tt.status, got, tt.contains)
		}
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  float64
	}{
		{"float64", 42.5, 42.5},
		{"float32", float32(3.14), 3.140000104904175},
		{"int", 10, 10.0},
		{"int64", int64(99), 99.0},
		{"int32", int32(7), 7.0},
		{"string", "123.45", 123.45},
		{"nil", nil, 0},
		{"bool", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toFloat64(tt.input)
			if got != tt.want {
				t.Errorf("toFloat64(%v) = %f, want %f", tt.input, got, tt.want)
			}
		})
	}
}
