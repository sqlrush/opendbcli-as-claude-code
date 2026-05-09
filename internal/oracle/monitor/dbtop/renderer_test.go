/*-------------------------------------------------------------------------
 *
 * renderer_test.go
 *	  Test cases for renderer.go (dbtop package):
 *	  TestRender_FixedHeight, TestRender_ContainsBoxChars,
 *	  TestRender_HealthIndicator.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/monitor/dbtop/renderer_test.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import (
	"strings"
	"testing"
	"time"
)

func testSnapshot() Snapshot {
	return Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Date(2026, 3, 10, 15, 32, 1, 0, time.Local),
		SGAMaxMB: 4096, PGAUsedMB: 512,
		HasDelta: true, DBPercent: 12.3, WTRPercent: 5.1,
		TotalSessions: 142, ActiveCount: 23, ActiveCPU: 8, ActiveIO: 3, IdleCount: 111,
		TPS: 1250, QPS: 8430, RedoKBs: 2048,
		Events: []WaitEvent{
			{Event: "db file sequential read", Waits: 3210, TimeSec: 12.5, PCT: 34.2},
			{Event: "log file sync", Waits: 1050, TimeSec: 4.8, PCT: 13.1},
		},
		Sessions: []SessionRow{
			{SID: 142, Username: "APP_USER", SQLID: "abc123def", Event: "db file seq read", ElapsedSec: 3.2, SQLText: "SELECT * FROM orders"},
		},
		Health: Healthy,
	}
}

func TestRender_FixedHeight(t *testing.T) {
	lines := Render(testSnapshot(), 80, 1)
	if len(lines) != 28 {
		t.Errorf("Render returned %d lines, want 28", len(lines))
	}
}

func TestRender_ContainsBoxChars(t *testing.T) {
	lines := Render(testSnapshot(), 80, 1)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "╭") {
		t.Error("should contain ╭")
	}
	if !strings.Contains(joined, "╰") {
		t.Error("should contain ╰")
	}
	if !strings.Contains(joined, "dbtop") {
		t.Error("should contain 'dbtop'")
	}
}

func TestRender_HealthIndicator(t *testing.T) {
	snap := testSnapshot()
	snap.Health = Critical
	snap.Alerts = []string{"AN=100"}
	lines := Render(snap, 80, 1)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "CRITICAL") {
		t.Error("CRITICAL not shown")
	}
}

func TestRender_NoDelta_ShowsDash(t *testing.T) {
	snap := testSnapshot()
	snap.HasDelta = false
	lines := Render(snap, 80, 1)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "--") {
		t.Error("should show '--' when no delta")
	}
}

func TestRender_BarChart(t *testing.T) {
	lines := Render(testSnapshot(), 100, 1)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "▇") {
		t.Error("should contain bar chart char ▇")
	}
}

func TestRender_StatusBar_Normal(t *testing.T) {
	lines := Render(testSnapshot(), 80, 1)
	last := lines[len(lines)-1]
	if !strings.Contains(last, "q") {
		t.Error("status bar should contain 'q'")
	}
}

func TestRender_StatusBar_Critical(t *testing.T) {
	snap := testSnapshot()
	snap.Health = Critical
	lines := Render(snap, 80, 1)
	last := lines[len(lines)-1]
	if !strings.Contains(last, "/health") {
		t.Error("CRITICAL status bar should mention /health")
	}
}

func TestRender_SessionTruncation(t *testing.T) {
	snap := testSnapshot()
	snap.ActiveCount = 25
	// Only 1 session in Sessions slice, but 25 active
	lines := Render(snap, 80, 1)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "还有") {
		t.Error("should show truncation hint")
	}
}

func TestRender_EmptySnapshot(t *testing.T) {
	snap := Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Now(), Health: Healthy,
	}
	lines := Render(snap, 80, 1)
	if len(lines) != 28 {
		t.Errorf("empty snapshot: got %d lines, want 28", len(lines))
	}
}

func TestBarChart(t *testing.T) {
	tests := []struct {
		pct   float64
		width int
		wantF int // filled chars
	}{
		{0, 8, 0},
		{50, 8, 4},
		{100, 8, 8},
		{25, 8, 2},
	}
	for _, tt := range tests {
		got := barChart(tt.pct, tt.width)
		filled := strings.Count(got, "▇")
		if filled != tt.wantF {
			t.Errorf("barChart(%.0f, %d) filled=%d want=%d got=%q", tt.pct, tt.width, filled, tt.wantF, got)
		}
		totalChars := strings.Count(got, "▇") + strings.Count(got, "░")
		if totalChars != tt.width {
			t.Errorf("barChart(%.0f, %d) total chars=%d want=%d", tt.pct, tt.width, totalChars, tt.width)
		}
	}
}
