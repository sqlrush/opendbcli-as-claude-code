/*-------------------------------------------------------------------------
 *
 * collector_test.go
 *	  Test cases for collector.go (trace package):
 *	  TestCaptureOpts_Validation, TestFormatTopFuncsTable,
 *	  TestFormatTopFuncsTable_Empty.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/collector_test.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"strings"
	"testing"
	"time"
)

func TestCaptureOpts_Validation(t *testing.T) {
	c := &Collector{}
	tests := []struct {
		name    string
		opts    CaptureOpts
		wantErr bool
	}{
		{"valid", CaptureOpts{PID: 1, Duration: 3 * time.Second, TopN: 20, OutDir: "/tmp", Freq: 99}, false},
		{"no pid", CaptureOpts{PID: 0, Duration: 3 * time.Second, TopN: 20, OutDir: "/tmp"}, true},
		{"too long", CaptureOpts{PID: 1, Duration: 30 * time.Second, TopN: 20, OutDir: "/tmp"}, true},
		{"no outdir", CaptureOpts{PID: 1, Duration: 3 * time.Second, TopN: 20, OutDir: ""}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.validate(tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFormatTopFuncsTable(t *testing.T) {
	funcs := []HotFunc{
		{Name: "ha_innodb::write_row", Samples: 100, Percentage: 62.5},
		{Name: "lock_wait", Samples: 60, Percentage: 37.5},
	}
	table := FormatTopFuncsTable(funcs)
	if table == "" {
		t.Error("expected non-empty table")
	}
	if !strings.Contains(table, "ha_innodb::write_row") {
		t.Error("table should contain function name")
	}
	if !strings.Contains(table, "62.5") {
		t.Error("table should contain percentage")
	}
}

func TestFormatTopFuncsTable_Empty(t *testing.T) {
	table := FormatTopFuncsTable(nil)
	if !strings.Contains(table, "no hot functions") {
		t.Error("expected 'no hot functions' message for empty input")
	}
}
