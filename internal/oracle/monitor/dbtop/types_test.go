/*-------------------------------------------------------------------------
 *
 * types_test.go
 *	  Test cases for types.go (dbtop package): TestHealthLevel_String,
 *	  TestSnapshot_Zero.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/monitor/dbtop/types_test.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import "testing"

func TestHealthLevel_String(t *testing.T) {
	tests := []struct {
		level HealthLevel
		want  string
	}{
		{Healthy, "HEALTHY"},
		{Warning, "WARNING"},
		{Critical, "CRITICAL"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("HealthLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestSnapshot_Zero(t *testing.T) {
	var s Snapshot
	if s.ActiveCount != 0 {
		t.Error("zero Snapshot should have 0 ActiveCount")
	}
	if s.Health != Healthy {
		t.Error("zero Snapshot should be Healthy")
	}
}
