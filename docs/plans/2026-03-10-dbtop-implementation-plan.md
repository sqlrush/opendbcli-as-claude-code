# /dbtop Real-Time Monitoring Dashboard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement `/dbtop` command that displays a real-time Oracle monitoring dashboard with in-place refresh in the REPL content area.

**Architecture:** New `internal/monitor/dbtop/` package handles data collection (SQL queries + delta calculations) and rendering (box-drawing + colors + bar charts). The REPL gets a `runDbtop()` method that drives the refresh loop with ANSI cursor positioning. The existing dbtop skill triggers the REPL into refresh mode via `ResultRefresh`.

**Tech Stack:** Go, ANSI escape codes, lipgloss (colors), go-runewidth (CJK alignment), goroutines (parallel SQL queries)

**Design doc:** `docs/plans/2026-03-10-dbtop-design.md`

---

## Task 1: Data Types and Snapshot Structure

Define the core data types for dbtop: the snapshot (one frame of data), delta state, and health status.

**Files:**
- Create: `internal/monitor/dbtop/types.go`
- Test: `internal/monitor/dbtop/types_test.go`

**Step 1: Write the test**

```go
// internal/monitor/dbtop/types_test.go
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
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/monitor/dbtop/ -run TestHealthLevel -v`
Expected: FAIL (package does not exist)

**Step 3: Write the implementation**

```go
// internal/monitor/dbtop/types.go
package dbtop

import "time"

// HealthLevel represents the overall database health.
type HealthLevel uint8

const (
	Healthy  HealthLevel = iota
	Warning
	Critical
)

func (h HealthLevel) String() string {
	switch h {
	case Warning:
		return "WARNING"
	case Critical:
		return "CRITICAL"
	default:
		return "HEALTHY"
	}
}

// Snapshot holds all data for one frame of the dbtop dashboard.
type Snapshot struct {
	// DB Info (cached, queried once)
	Version      string
	InstanceName string
	DBRole       string
	Timestamp    time.Time

	// Memory
	SGAMB float64
	PGAMB float64

	// CPU/Wait ratios (delta-based, "--" on first frame)
	DBPercent  float64
	WTRPercent float64
	HasDelta   bool // false on the first frame

	// Session counts
	TotalSessions  int
	ActiveCount    int // AN: active non-idle
	ActiveCPU      int // ASC: active on CPU
	ActiveIO       int // ASI: active on I/O
	IdleCount      int // IDL: inactive or idle wait

	// Throughput (delta-based)
	TPS     float64
	QPS     float64
	RedoKBs float64

	// Top wait events (up to 5)
	Events []WaitEvent

	// Active sessions (sorted by ElapsedSec DESC)
	Sessions []SessionRow

	// Health
	Health  HealthLevel
	Alerts  []string // human-readable alert messages
}

// WaitEvent represents one row in the Top Wait Events panel.
type WaitEvent struct {
	Event      string
	Waits      int64   // delta waits (RT mode)
	TimeSec    float64 // delta time in seconds
	PCT        float64 // percentage of total DB time
	WaitClass  string
}

// SessionRow represents one row in the Active Sessions panel.
type SessionRow struct {
	SID        int
	Username   string
	SQLID      string
	Event      string
	ElapsedSec float64
	SQLText    string
	Program    string
	Status     string
}

// DeltaState stores previous-frame values for delta calculations.
type DeltaState struct {
	PrevCPUTime   int64
	PrevDBTime    int64
	PrevCommits   int64
	PrevRollbacks int64
	PrevExecutes  int64
	PrevRedoSize  int64
	PrevTimestamp time.Time
	PrevTPS       float64 // for TPS drop detection

	// Per-event previous values for RT mode.
	PrevEventWaits map[string]int64
	PrevEventTime  map[string]int64 // microseconds
	PrevTotalEventTime int64
	PrevCPUTimeEvent   int64

	Initialized bool
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/monitor/dbtop/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/dbtop/types.go internal/monitor/dbtop/types_test.go
git commit -m "feat(dbtop): add data types for snapshot, delta state, and health"
```

---

## Task 2: Delta Calculation Logic

Implement pure functions for computing delta-based metrics (TPS, QPS, REDO, db%, WTR%) from two consecutive raw samples.

**Files:**
- Create: `internal/monitor/dbtop/delta.go`
- Test: `internal/monitor/dbtop/delta_test.go`

**Step 1: Write the test**

```go
// internal/monitor/dbtop/delta_test.go
package dbtop

import (
	"testing"
	"time"
)

func TestComputeDeltas_FirstFrame(t *testing.T) {
	state := &DeltaState{}
	now := time.Now()
	raw := RawSample{
		CPUTime: 1000000, DBTime: 2000000,
		Commits: 100, Rollbacks: 5, Executes: 500, RedoSize: 1024000,
		Timestamp: now,
	}
	result := ComputeDeltas(state, raw)
	if result.HasDelta {
		t.Error("first frame should not have delta")
	}
	if !state.Initialized {
		t.Error("state should be initialized after first frame")
	}
}

func TestComputeDeltas_SecondFrame(t *testing.T) {
	now := time.Now()
	state := &DeltaState{
		PrevCPUTime: 1000000, PrevDBTime: 2000000,
		PrevCommits: 100, PrevRollbacks: 5,
		PrevExecutes: 500, PrevRedoSize: 1024000,
		PrevTimestamp: now,
		Initialized:  true,
	}
	raw := RawSample{
		CPUTime: 1500000, DBTime: 3000000,
		Commits: 200, Rollbacks: 10, Executes: 1500, RedoSize: 2048000,
		Timestamp: now.Add(1 * time.Second),
	}
	result := ComputeDeltas(state, raw)
	if !result.HasDelta {
		t.Error("second frame should have delta")
	}
	// TPS = (200-100 + 10-5) / 1.0 = 105
	if result.TPS < 104 || result.TPS > 106 {
		t.Errorf("TPS = %.1f, want ~105", result.TPS)
	}
	// QPS = (1500-500) / 1.0 = 1000
	if result.QPS < 999 || result.QPS > 1001 {
		t.Errorf("QPS = %.1f, want ~1000", result.QPS)
	}
	// REDO = (2048000-1024000) / 1024 / 1.0 = 1000 kB/s
	if result.RedoKBs < 999 || result.RedoKBs > 1001 {
		t.Errorf("RedoKBs = %.1f, want ~1000", result.RedoKBs)
	}
}

func TestComputeDeltas_DBPercent(t *testing.T) {
	now := time.Now()
	state := &DeltaState{
		PrevCPUTime: 0, PrevDBTime: 0,
		PrevTimestamp: now, Initialized: true,
	}
	// After 1 second: 500ms of CPU time (= 500000 microseconds)
	raw := RawSample{
		CPUTime:   500000,
		DBTime:    800000,
		Timestamp: now.Add(1 * time.Second),
	}
	result := ComputeDeltas(state, raw)
	// db% = cpu_delta / (time_delta * 1e6) * 100 = 500000 / 1000000 * 100 = 50%
	if result.DBPercent < 49 || result.DBPercent > 51 {
		t.Errorf("DBPercent = %.1f, want ~50", result.DBPercent)
	}
	// WTR% = (db - cpu) / db * 100 = (800000-500000)/800000*100 = 37.5%
	if result.WTRPercent < 37 || result.WTRPercent > 38 {
		t.Errorf("WTRPercent = %.1f, want ~37.5", result.WTRPercent)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/dbtop/ -run TestComputeDeltas -v`
Expected: FAIL (undefined: RawSample, ComputeDeltas)

**Step 3: Write the implementation**

```go
// internal/monitor/dbtop/delta.go
package dbtop

import "time"

// RawSample holds raw cumulative values from a single Oracle query.
type RawSample struct {
	CPUTime   int64     // v$sys_time_model DB CPU (microseconds)
	DBTime    int64     // v$sys_time_model DB time (microseconds)
	Commits   int64     // v$sysstat user commits
	Rollbacks int64     // v$sysstat user rollbacks
	Executes  int64     // v$sysstat execute count
	RedoSize  int64     // v$sysstat redo size (bytes)
	Timestamp time.Time // SYSTIMESTAMP
}

// DeltaResult holds computed per-second rates.
type DeltaResult struct {
	HasDelta   bool
	TPS        float64
	QPS        float64
	RedoKBs    float64
	DBPercent  float64
	WTRPercent float64
}

// ComputeDeltas calculates delta-based metrics from a raw sample.
// On the first call (state.Initialized == false), it stores the baseline and returns HasDelta=false.
// On subsequent calls, it computes per-second rates.
func ComputeDeltas(state *DeltaState, raw RawSample) DeltaResult {
	if !state.Initialized {
		state.PrevCPUTime = raw.CPUTime
		state.PrevDBTime = raw.DBTime
		state.PrevCommits = raw.Commits
		state.PrevRollbacks = raw.Rollbacks
		state.PrevExecutes = raw.Executes
		state.PrevRedoSize = raw.RedoSize
		state.PrevTimestamp = raw.Timestamp
		state.Initialized = true
		return DeltaResult{}
	}

	elapsed := raw.Timestamp.Sub(state.PrevTimestamp).Seconds()
	if elapsed <= 0 {
		return DeltaResult{HasDelta: true}
	}

	commitsDelta := float64(raw.Commits - state.PrevCommits)
	rollbacksDelta := float64(raw.Rollbacks - state.PrevRollbacks)
	executesDelta := float64(raw.Executes - state.PrevExecutes)
	redoDelta := float64(raw.RedoSize - state.PrevRedoSize)
	cpuDelta := float64(raw.CPUTime - state.PrevCPUTime)
	dbDelta := float64(raw.DBTime - state.PrevDBTime)

	result := DeltaResult{
		HasDelta: true,
		TPS:      (commitsDelta + rollbacksDelta) / elapsed,
		QPS:      executesDelta / elapsed,
		RedoKBs:  redoDelta / 1024.0 / elapsed,
	}

	// db% = CPU time used / wall clock time * 100
	// CPU time is in microseconds, elapsed is in seconds.
	wallMicro := elapsed * 1e6
	if wallMicro > 0 {
		result.DBPercent = cpuDelta / wallMicro * 100
	}

	// WTR% = (DB time - CPU time) / DB time * 100
	if dbDelta > 0 {
		result.WTRPercent = (dbDelta - cpuDelta) / dbDelta * 100
	}

	// Update state for next frame.
	state.PrevCPUTime = raw.CPUTime
	state.PrevDBTime = raw.DBTime
	state.PrevCommits = raw.Commits
	state.PrevRollbacks = raw.Rollbacks
	state.PrevExecutes = raw.Executes
	state.PrevRedoSize = raw.RedoSize
	state.PrevTPS = result.TPS
	state.PrevTimestamp = raw.Timestamp

	return result
}
```

**Step 4: Run test**

Run: `go test ./internal/monitor/dbtop/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/dbtop/delta.go internal/monitor/dbtop/delta_test.go
git commit -m "feat(dbtop): implement delta calculation for TPS/QPS/REDO/db%/WTR%"
```

---

## Task 3: Health Evaluation

Implement the health evaluation logic that determines HEALTHY/WARNING/CRITICAL from snapshot data.

**Files:**
- Create: `internal/monitor/dbtop/health.go`
- Test: `internal/monitor/dbtop/health_test.go`

**Step 1: Write the test**

```go
// internal/monitor/dbtop/health_test.go
package dbtop

import "testing"

func TestEvaluateHealth_Healthy(t *testing.T) {
	s := &Snapshot{
		ActiveCount: 10, DBPercent: 20, WTRPercent: 15,
		HasDelta: true,
		Events:   []WaitEvent{{PCT: 20}},
		Sessions: []SessionRow{{ElapsedSec: 100}},
	}
	EvaluateHealth(s, 0)
	if s.Health != Healthy {
		t.Errorf("Health = %v, want Healthy", s.Health)
	}
	if len(s.Alerts) != 0 {
		t.Errorf("Alerts = %v, want empty", s.Alerts)
	}
}

func TestEvaluateHealth_Warning_AN(t *testing.T) {
	s := &Snapshot{ActiveCount: 50, HasDelta: true}
	EvaluateHealth(s, 0)
	if s.Health != Warning {
		t.Errorf("Health = %v, want Warning (AN=50)", s.Health)
	}
}

func TestEvaluateHealth_Critical_AN(t *testing.T) {
	s := &Snapshot{ActiveCount: 100, HasDelta: true}
	EvaluateHealth(s, 0)
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (AN=100)", s.Health)
	}
}

func TestEvaluateHealth_Warning_DBPercent(t *testing.T) {
	s := &Snapshot{DBPercent: 60, HasDelta: true}
	EvaluateHealth(s, 0)
	if s.Health != Warning {
		t.Errorf("Health = %v, want Warning (db%%=60)", s.Health)
	}
}

func TestEvaluateHealth_Critical_WTR(t *testing.T) {
	s := &Snapshot{WTRPercent: 70, HasDelta: true}
	EvaluateHealth(s, 0)
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (WTR%%=70)", s.Health)
	}
}

func TestEvaluateHealth_Warning_EventPCT(t *testing.T) {
	s := &Snapshot{
		HasDelta: true,
		Events:   []WaitEvent{{Event: "log file sync", PCT: 40}},
	}
	EvaluateHealth(s, 0)
	if s.Health != Warning {
		t.Errorf("Health = %v, want Warning (event PCT=40)", s.Health)
	}
}

func TestEvaluateHealth_Critical_SessionET(t *testing.T) {
	s := &Snapshot{
		HasDelta: true,
		Sessions: []SessionRow{{ElapsedSec: 700, SID: 142}},
	}
	EvaluateHealth(s, 0)
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (E/T=700)", s.Health)
	}
}

func TestEvaluateHealth_Warning_TPSDrop(t *testing.T) {
	s := &Snapshot{TPS: 40, HasDelta: true}
	EvaluateHealth(s, 100) // prevTPS=100, current=40, drop=60%
	if s.Health != Warning {
		t.Errorf("Health = %v, want Warning (TPS drop 60%%)", s.Health)
	}
}

func TestEvaluateHealth_NoDelta_AlwaysHealthy(t *testing.T) {
	s := &Snapshot{ActiveCount: 200, HasDelta: false}
	EvaluateHealth(s, 0)
	// Delta metrics not evaluated on first frame, but AN is not delta-based
	// AN should still trigger
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (AN=200 even without delta)", s.Health)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/dbtop/ -run TestEvaluateHealth -v`
Expected: FAIL (undefined: EvaluateHealth)

**Step 3: Write the implementation**

```go
// internal/monitor/dbtop/health.go
package dbtop

import "fmt"

// Health thresholds.
const (
	anWarn     = 30
	anCrit     = 80
	dbPctWarn  = 50.0
	dbPctCrit  = 80.0
	wtrWarn    = 30.0
	wtrCrit    = 60.0
	evtPctWarn = 30.0
	evtPctCrit = 50.0
	etWarn     = 300.0 // seconds
	etCrit     = 600.0
	tpsDropWarn = 50.0 // percent
	tpsDropCrit = 80.0
)

// EvaluateHealth evaluates the snapshot and sets Health and Alerts.
// prevTPS is the TPS from the previous frame (for drop detection).
func EvaluateHealth(s *Snapshot, prevTPS float64) {
	s.Health = Healthy
	s.Alerts = nil

	promote := func(level HealthLevel, msg string) {
		if level > s.Health {
			s.Health = level
		}
		s.Alerts = append(s.Alerts, msg)
	}

	// AN (active sessions) — always evaluated.
	switch {
	case s.ActiveCount > anCrit:
		promote(Critical, fmt.Sprintf("AN=%d (>%d)", s.ActiveCount, anCrit))
	case s.ActiveCount > anWarn:
		promote(Warning, fmt.Sprintf("AN=%d (>%d)", s.ActiveCount, anWarn))
	}

	// Delta-based metrics.
	if s.HasDelta {
		switch {
		case s.DBPercent > dbPctCrit:
			promote(Critical, fmt.Sprintf("db%%=%.1f (>%.0f)", s.DBPercent, dbPctCrit))
		case s.DBPercent > dbPctWarn:
			promote(Warning, fmt.Sprintf("db%%=%.1f (>%.0f)", s.DBPercent, dbPctWarn))
		}

		switch {
		case s.WTRPercent > wtrCrit:
			promote(Critical, fmt.Sprintf("WTR%%=%.1f (>%.0f)", s.WTRPercent, wtrCrit))
		case s.WTRPercent > wtrWarn:
			promote(Warning, fmt.Sprintf("WTR%%=%.1f (>%.0f)", s.WTRPercent, wtrWarn))
		}

		// TPS drop detection.
		if prevTPS > 0 && s.TPS < prevTPS {
			dropPct := (prevTPS - s.TPS) / prevTPS * 100
			switch {
			case dropPct > tpsDropCrit:
				promote(Critical, fmt.Sprintf("TPS drop %.0f%%", dropPct))
			case dropPct > tpsDropWarn:
				promote(Warning, fmt.Sprintf("TPS drop %.0f%%", dropPct))
			}
		}
	}

	// Event PCT — check each event.
	for _, e := range s.Events {
		switch {
		case e.PCT > evtPctCrit:
			promote(Critical, fmt.Sprintf("%s PCT=%.1f%%", e.Event, e.PCT))
		case e.PCT > evtPctWarn:
			promote(Warning, fmt.Sprintf("%s PCT=%.1f%%", e.Event, e.PCT))
		}
	}

	// Session E/T — check each session.
	for _, sess := range s.Sessions {
		switch {
		case sess.ElapsedSec > etCrit:
			promote(Critical, fmt.Sprintf("SID %d E/T=%.0fs", sess.SID, sess.ElapsedSec))
		case sess.ElapsedSec > etWarn:
			promote(Warning, fmt.Sprintf("SID %d E/T=%.0fs", sess.SID, sess.ElapsedSec))
		}
	}
}
```

**Step 4: Run test**

Run: `go test ./internal/monitor/dbtop/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/dbtop/health.go internal/monitor/dbtop/health_test.go
git commit -m "feat(dbtop): implement health evaluation with thresholds"
```

---

## Task 4: Data Collector

Implement the SQL queries and parallel data collection. The collector uses `db.Driver` to query Oracle and returns a `Snapshot`.

**Files:**
- Create: `internal/monitor/dbtop/collector.go`
- Test: `internal/monitor/dbtop/collector_test.go`

**Step 1: Write the test**

```go
// internal/monitor/dbtop/collector_test.go
package dbtop

import (
	"context"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
)

func setupMockDriver() *mock.Driver {
	drv := mock.NewMockDriver()
	drv.ServerInfoVal = db.ServerInfo{
		Version:      "19.3.0",
		InstanceName: "ORCL",
	}
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		switch {
		case containsStr(sql, "v$database"):
			return &db.QueryResult{
				Columns: []string{"DATABASE_ROLE"},
				Rows:    [][]any{{"PRIMARY"}},
			}, nil
		case containsStr(sql, "v$sga"):
			return &db.QueryResult{
				Columns: []string{"SGA_MB"},
				Rows:    [][]any{{float64(4096)}},
			}, nil
		case containsStr(sql, "v$pgastat"):
			return &db.QueryResult{
				Columns: []string{"PGA_MB"},
				Rows:    [][]any{{float64(512)}},
			}, nil
		case containsStr(sql, "v$sys_time_model"):
			return &db.QueryResult{
				Columns: []string{"CPU_TIME", "DB_TIME"},
				Rows:    [][]any{{int64(1000000), int64(2000000)}},
			}, nil
		case containsStr(sql, "v$sysstat"):
			return &db.QueryResult{
				Columns: []string{"name", "value"},
				Rows: [][]any{
					{"user commits", int64(100)},
					{"user rollbacks", int64(5)},
					{"execute count", int64(500)},
					{"redo size", int64(1024000)},
				},
			}, nil
		case containsStr(sql, "v$session") && containsStr(sql, "COUNT"):
			return &db.QueryResult{
				Columns: []string{"sn", "an", "asc", "asi", "idl"},
				Rows:    [][]any{{int64(142), int64(23), int64(8), int64(3), int64(111)}},
			}, nil
		case containsStr(sql, "v$system_event"):
			return &db.QueryResult{
				Columns: []string{"CPU_TIME", "EVENT", "TOTAL_WAITS", "TIME_WAITED_MICRO", "WAIT_CLASS"},
				Rows: [][]any{
					{int64(1000000), "db file sequential read", int64(5000), int64(12500000), "User I/O"},
					{int64(1000000), "log file sync", int64(2000), int64(4800000), "Commit"},
				},
			}, nil
		case containsStr(sql, "v$session") && containsStr(sql, "ACTIVE"):
			return &db.QueryResult{
				Columns: []string{"SID", "USERNAME", "SQL_ID", "EVENT", "ELAPSED_SEC", "SQL_TEXT", "PROGRAM", "STATUS"},
				Rows: [][]any{
					{int64(142), "APP_USER", "abc123", "db file sequential read", float64(3.2), "SELECT * FROM orders", "sqlplus", "ACTIVE"},
				},
			}, nil
		default:
			return &db.QueryResult{}, nil
		}
	}
	return drv
}

func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

// stringContains is a simple contains check (avoiding strings import in test helper).
func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestCollector_Collect_FirstFrame(t *testing.T) {
	drv := setupMockDriver()
	c := NewCollector(drv)
	snap := c.Collect(context.Background())

	if snap.InstanceName != "ORCL" {
		t.Errorf("InstanceName = %q, want ORCL", snap.InstanceName)
	}
	if snap.Version != "19.3.0" {
		t.Errorf("Version = %q, want 19.3.0", snap.Version)
	}
	if snap.DBRole != "PRIMARY" {
		t.Errorf("DBRole = %q, want PRIMARY", snap.DBRole)
	}
	if snap.SGAMB != 4096 {
		t.Errorf("SGAMB = %.0f, want 4096", snap.SGAMB)
	}
	if snap.PGAMB != 512 {
		t.Errorf("PGAMB = %.0f, want 512", snap.PGAMB)
	}
	if snap.TotalSessions != 142 {
		t.Errorf("TotalSessions = %d, want 142", snap.TotalSessions)
	}
	if snap.ActiveCount != 23 {
		t.Errorf("ActiveCount = %d, want 23", snap.ActiveCount)
	}
	if snap.HasDelta {
		t.Error("first frame should not have delta")
	}
	if len(snap.Events) == 0 {
		t.Error("Events should not be empty")
	}
	if len(snap.Sessions) == 0 {
		t.Error("Sessions should not be empty")
	}
}

func TestCollector_Collect_SecondFrame_HasDelta(t *testing.T) {
	drv := setupMockDriver()
	c := NewCollector(drv)

	// First frame.
	c.Collect(context.Background())
	// Simulate time passing.
	c.state.PrevTimestamp = time.Now().Add(-1 * time.Second)

	// Second frame.
	snap := c.Collect(context.Background())
	// Delta should exist (values unchanged, so rates are 0, but HasDelta is true).
	if !snap.HasDelta {
		t.Error("second frame should have delta")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/dbtop/ -run TestCollector -v`
Expected: FAIL (undefined: NewCollector)

**Step 3: Write the implementation**

```go
// internal/monitor/dbtop/collector.go
package dbtop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// SQL queries for data collection.
const (
	dbRoleSQL = `SELECT database_role FROM v$database`

	sgaSQL = `SELECT ROUND(SUM(value)/1024/1024, 0) FROM v$sga`

	pgaSQL = `SELECT ROUND(value/1024/1024, 0) FROM v$pgastat WHERE name = 'total PGA allocated'`

	timeModelSQL = `SELECT
  (SELECT value FROM v$sys_time_model WHERE stat_name = 'DB CPU') AS cpu_time,
  (SELECT value FROM v$sys_time_model WHERE stat_name = 'DB time') AS db_time`

	sysstatSQL2 = `SELECT name, value FROM v$sysstat
WHERE name IN ('user commits', 'user rollbacks', 'execute count', 'redo size')`

	sessionCountSQL = `SELECT
  COUNT(*) AS sn,
  COUNT(CASE WHEN s.STATUS='ACTIVE' AND s.WAIT_CLASS != 'Idle' THEN 1 END) AS an,
  COUNT(CASE WHEN s.STATUS='ACTIVE' AND s.WAIT_CLASS NOT IN ('Idle','User I/O','System I/O') THEN 1 END) AS asc_cnt,
  COUNT(CASE WHEN s.STATUS='ACTIVE' AND s.WAIT_CLASS IN ('User I/O','System I/O') THEN 1 END) AS asi,
  COUNT(CASE WHEN s.STATUS='INACTIVE' OR s.WAIT_CLASS='Idle' THEN 1 END) AS idl
FROM v$session s WHERE s.TYPE = 'USER'`

	eventSQL = `SELECT
  (SELECT value FROM v$sys_time_model WHERE stat_name = 'DB CPU') AS cpu_time,
  e.EVENT, e.TOTAL_WAITS, e.TIME_WAITED_MICRO, e.WAIT_CLASS
FROM v$system_event e
WHERE e.WAIT_CLASS != 'Idle' AND e.TOTAL_WAITS > 0
ORDER BY e.TIME_WAITED_MICRO DESC`

	activeSessionSQL = `SELECT
  s.SID, s.USERNAME, s.SQL_ID,
  NVL(s.EVENT, 'ON CPU'),
  CASE WHEN s.STATUS='ACTIVE' AND s.SQL_EXEC_START IS NOT NULL
       THEN ROUND((SYSDATE - s.SQL_EXEC_START) * 86400, 1) ELSE 0 END AS elapsed_sec,
  (SELECT SUBSTR(sql_text, 1, 80) FROM v$sqlarea WHERE sql_id = s.SQL_ID AND ROWNUM = 1),
  s.PROGRAM, s.STATUS
FROM v$session s
WHERE s.TYPE = 'USER' AND s.STATUS = 'ACTIVE' AND s.WAIT_CLASS != 'Idle'
ORDER BY elapsed_sec DESC`
)

// Collector gathers dbtop data from Oracle using a db.Driver.
type Collector struct {
	driver db.Driver
	state  DeltaState

	// Cached values (queried once).
	cachedRole    string
	cachedVersion string
	cachedInstance string
}

// NewCollector creates a Collector.
func NewCollector(driver db.Driver) *Collector {
	info := driver.ServerInfo()
	return &Collector{
		driver:         driver,
		cachedVersion:  info.Version,
		cachedInstance: info.InstanceName,
	}
}

// Collect queries Oracle and returns a Snapshot.
// Queries run in parallel via goroutines for minimal latency.
func (c *Collector) Collect(ctx context.Context) Snapshot {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		snap Snapshot
		raw  RawSample
	)

	snap.Version = c.cachedVersion
	snap.InstanceName = c.cachedInstance
	snap.Timestamp = time.Now()

	// Group 1: DB role + SGA + PGA + time model + sysstat
	wg.Add(1)
	go func() {
		defer wg.Done()
		role := c.queryDBRole(ctx)
		sgaMB := c.querySGA(ctx)
		pgaMB := c.queryPGA(ctx)
		cpuTime, dbTime := c.queryTimeModel(ctx)
		commits, rollbacks, executes, redoSize := c.querySysstat(ctx)
		mu.Lock()
		snap.DBRole = role
		snap.SGAMB = sgaMB
		snap.PGAMB = pgaMB
		raw.CPUTime = cpuTime
		raw.DBTime = dbTime
		raw.Commits = commits
		raw.Rollbacks = rollbacks
		raw.Executes = executes
		raw.RedoSize = redoSize
		raw.Timestamp = time.Now()
		mu.Unlock()
	}()

	// Group 2: Session counts
	wg.Add(1)
	go func() {
		defer wg.Done()
		sn, an, asc, asi, idl := c.querySessionCounts(ctx)
		mu.Lock()
		snap.TotalSessions = sn
		snap.ActiveCount = an
		snap.ActiveCPU = asc
		snap.ActiveIO = asi
		snap.IdleCount = idl
		mu.Unlock()
	}()

	// Group 3: Wait events
	wg.Add(1)
	go func() {
		defer wg.Done()
		events := c.queryEvents(ctx)
		mu.Lock()
		snap.Events = events
		mu.Unlock()
	}()

	// Group 4: Active sessions
	wg.Add(1)
	go func() {
		defer wg.Done()
		sessions := c.queryActiveSessions(ctx)
		mu.Lock()
		snap.Sessions = sessions
		mu.Unlock()
	}()

	wg.Wait()

	// Compute deltas.
	prevTPS := c.state.PrevTPS
	delta := ComputeDeltas(&c.state, raw)
	snap.HasDelta = delta.HasDelta
	snap.TPS = delta.TPS
	snap.QPS = delta.QPS
	snap.RedoKBs = delta.RedoKBs
	snap.DBPercent = delta.DBPercent
	snap.WTRPercent = delta.WTRPercent

	// Evaluate health.
	EvaluateHealth(&snap, prevTPS)

	return snap
}

func (c *Collector) queryDBRole(ctx context.Context) string {
	if c.cachedRole != "" {
		return c.cachedRole
	}
	result, err := c.driver.Query(ctx, dbRoleSQL)
	if err != nil || len(result.Rows) == 0 {
		return "UNKNOWN"
	}
	role := fmt.Sprintf("%v", result.Rows[0][0])
	c.cachedRole = role
	return role
}

func (c *Collector) querySGA(ctx context.Context) float64 {
	result, err := c.driver.Query(ctx, sgaSQL)
	if err != nil || len(result.Rows) == 0 {
		return 0
	}
	return toFloat(result.Rows[0][0])
}

func (c *Collector) queryPGA(ctx context.Context) float64 {
	result, err := c.driver.Query(ctx, pgaSQL)
	if err != nil || len(result.Rows) == 0 {
		return 0
	}
	return toFloat(result.Rows[0][0])
}

func (c *Collector) queryTimeModel(ctx context.Context) (int64, int64) {
	result, err := c.driver.Query(ctx, timeModelSQL)
	if err != nil || len(result.Rows) == 0 || len(result.Rows[0]) < 2 {
		return 0, 0
	}
	return toInt(result.Rows[0][0]), toInt(result.Rows[0][1])
}

func (c *Collector) querySysstat(ctx context.Context) (int64, int64, int64, int64) {
	result, err := c.driver.Query(ctx, sysstatSQL2)
	if err != nil {
		return 0, 0, 0, 0
	}
	stats := make(map[string]int64)
	for _, row := range result.Rows {
		if len(row) >= 2 {
			name := fmt.Sprintf("%v", row[0])
			stats[name] = toInt(row[1])
		}
	}
	return stats["user commits"], stats["user rollbacks"], stats["execute count"], stats["redo size"]
}

func (c *Collector) querySessionCounts(ctx context.Context) (int, int, int, int, int) {
	result, err := c.driver.Query(ctx, sessionCountSQL)
	if err != nil || len(result.Rows) == 0 || len(result.Rows[0]) < 5 {
		return 0, 0, 0, 0, 0
	}
	r := result.Rows[0]
	return int(toInt(r[0])), int(toInt(r[1])), int(toInt(r[2])), int(toInt(r[3])), int(toInt(r[4]))
}

func (c *Collector) queryEvents(ctx context.Context) []WaitEvent {
	result, err := c.driver.Query(ctx, eventSQL)
	if err != nil || len(result.Rows) == 0 {
		return nil
	}

	// First row's CPU_TIME is the DB CPU value.
	var cpuTime int64
	if len(result.Rows[0]) >= 1 {
		cpuTime = toInt(result.Rows[0][0])
	}

	// Sum all event times for PCT denominator.
	var totalEventTime int64
	for _, row := range result.Rows {
		if len(row) >= 4 {
			totalEventTime += toInt(row[3])
		}
	}
	denominator := float64(totalEventTime + cpuTime)

	// Build events (top 5).
	var events []WaitEvent
	// Add DB CPU as first entry.
	if denominator > 0 {
		events = append(events, WaitEvent{
			Event:     "DB CPU",
			TimeSec:   float64(cpuTime) / 1e6,
			PCT:       float64(cpuTime) / denominator * 100,
			WaitClass: "CPU",
		})
	}

	for _, row := range result.Rows {
		if len(row) < 5 || len(events) >= 6 { // DB CPU + 5 events
			break
		}
		timeMicro := toInt(row[3])
		pct := 0.0
		if denominator > 0 {
			pct = float64(timeMicro) / denominator * 100
		}
		events = append(events, WaitEvent{
			Event:     fmt.Sprintf("%v", row[1]),
			Waits:     toInt(row[2]),
			TimeSec:   float64(timeMicro) / 1e6,
			PCT:       pct,
			WaitClass: fmt.Sprintf("%v", row[4]),
		})
	}

	return events
}

func (c *Collector) queryActiveSessions(ctx context.Context) []SessionRow {
	result, err := c.driver.Query(ctx, activeSessionSQL)
	if err != nil {
		return nil
	}
	var sessions []SessionRow
	for _, row := range result.Rows {
		if len(row) < 8 || len(sessions) >= 10 {
			break
		}
		sessions = append(sessions, SessionRow{
			SID:        int(toInt(row[0])),
			Username:   fmt.Sprintf("%v", row[1]),
			SQLID:      fmt.Sprintf("%v", row[2]),
			Event:      fmt.Sprintf("%v", row[3]),
			ElapsedSec: toFloat(row[4]),
			SQLText:    fmt.Sprintf("%v", row[5]),
			Program:    fmt.Sprintf("%v", row[6]),
			Status:     fmt.Sprintf("%v", row[7]),
		})
	}
	return sessions
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case int32:
		return int64(n)
	default:
		return 0
	}
}

// containsString is a helper for SQL routing in tests — not exported.
func init() {
	_ = strings.Contains // ensure strings import
}
```

Note: The test helper `containsStr` / `stringContains` should use `strings.Contains` instead. Fix in test:
```go
import "strings"
// Replace containsStr/stringContains with strings.Contains
```

**Step 4: Run test**

Run: `go test ./internal/monitor/dbtop/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/dbtop/collector.go internal/monitor/dbtop/collector_test.go
git commit -m "feat(dbtop): implement parallel data collector with SQL queries"
```

---

## Task 5: Dashboard Renderer

Render a `Snapshot` into 28 fixed lines of ANSI-colored text with box-drawing characters and bar charts.

**Files:**
- Create: `internal/monitor/dbtop/renderer.go`
- Test: `internal/monitor/dbtop/renderer_test.go`

**Step 1: Write the test**

```go
// internal/monitor/dbtop/renderer_test.go
package dbtop

import (
	"strings"
	"testing"
	"time"
)

func TestRender_FixedHeight(t *testing.T) {
	snap := Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Date(2026, 3, 10, 15, 32, 1, 0, time.Local),
		SGAMB: 4096, PGAMB: 512,
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

	lines := Render(snap, 80, 1)
	if len(lines) != 28 {
		t.Errorf("Render returned %d lines, want 28", len(lines))
	}
}

func TestRender_ContainsBoxChars(t *testing.T) {
	snap := Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Now(), SGAMB: 4096, PGAMB: 512,
		Health: Healthy,
	}
	lines := Render(snap, 80, 1)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "╭") {
		t.Error("output should contain top-left corner ╭")
	}
	if !strings.Contains(joined, "╰") {
		t.Error("output should contain bottom-left corner ╰")
	}
	if !strings.Contains(joined, "dbtop") {
		t.Error("output should contain 'dbtop' title")
	}
}

func TestRender_HealthIndicator(t *testing.T) {
	snap := Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Now(), Health: Critical,
		Alerts: []string{"AN=100"},
	}
	lines := Render(snap, 80, 1)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "CRITICAL") {
		t.Error("CRITICAL health not shown in output")
	}
}

func TestRender_NoDelta_ShowsDash(t *testing.T) {
	snap := Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Now(), HasDelta: false, Health: Healthy,
	}
	lines := Render(snap, 80, 1)
	joined := strings.Join(lines, "\n")

	// db% and WTR% should show "--" when no delta
	if !strings.Contains(joined, "--") {
		t.Error("no-delta frame should show '--' for db%/WTR%")
	}
}

func TestRender_BarChart(t *testing.T) {
	snap := Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Now(), HasDelta: true, Health: Healthy,
		Events: []WaitEvent{
			{Event: "test event", PCT: 50.0, TimeSec: 10.0, Waits: 100},
		},
	}
	lines := Render(snap, 100, 1)
	joined := strings.Join(lines, "\n")

	// Should contain bar chart characters
	if !strings.Contains(joined, "▇") {
		t.Error("output should contain bar chart filled char ▇")
	}
}

func TestRender_StatusBar_Critical(t *testing.T) {
	snap := Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Now(), Health: Critical,
		Alerts: []string{"AN=100"},
	}
	lines := Render(snap, 80, 1)
	lastLine := lines[len(lines)-1]

	if !strings.Contains(lastLine, "/health") {
		t.Error("CRITICAL status bar should mention /health")
	}
}

func TestRender_SessionTruncation(t *testing.T) {
	snap := Snapshot{
		Version: "19.3.0", InstanceName: "ORCL", DBRole: "PRIMARY",
		Timestamp: time.Now(), Health: Healthy,
		ActiveCount: 15,
	}
	// 15 active but only 0 in Sessions slice = all truncated.
	lines := Render(snap, 80, 1)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "15") {
		t.Error("should show remaining session count")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/monitor/dbtop/ -run TestRender -v`
Expected: FAIL (undefined: Render)

**Step 3: Write the implementation**

Create `internal/monitor/dbtop/renderer.go`. This is the largest file — it renders each panel into fixed-height line blocks.

Key function signature:
```go
// Render produces exactly 28 lines of the dbtop dashboard.
// cols is the terminal width, intervalSec is the refresh interval for the status bar.
func Render(snap Snapshot, cols int, intervalSec int) []string
```

The renderer consists of:
- `renderHeaderBox(snap, cols)` — returns 5 lines (top border, blank, data1, data2, bottom border)
- `renderEventsBox(snap, cols)` — returns 8 lines (top border, blank+header, 5 event rows, bottom border)
- `renderSessionsBox(snap, cols)` — returns 14 lines (top border, blank+header, 10 session rows, truncation hint, bottom border)
- `renderStatusBar(snap, cols, intervalSec)` — returns 1 line
- `barChart(pct float64, width int)` — returns string like "▇▇▇▇░░░░"
- `formatSize(mb float64)` — "4,096M" or "512M"
- `formatRate(v float64)` — "1,250" or "2.0k"

Each panel pads/truncates lines to exactly `cols` characters wide. Empty event/session slots are filled with blank bordered lines.

The implementation should use `lipgloss` styles for color (import from `internal/ui` patterns) and `runewidth` for CJK-safe padding.

**Full implementation is ~300 lines. Key logic patterns:**

```go
func Render(snap Snapshot, cols int, intervalSec int) []string {
    var lines []string
    lines = append(lines, renderHeaderBox(snap, cols)...)
    lines = append(lines, renderEventsBox(snap, cols)...)
    lines = append(lines, renderSessionsBox(snap, cols)...)
    lines = append(lines, renderStatusBar(snap, cols, intervalSec))
    // Pad to exactly 28 lines.
    for len(lines) < 28 {
        lines = append(lines, "")
    }
    return lines[:28]
}

func barChart(pct float64, width int) string {
    filled := int(pct / 100.0 * float64(width))
    if filled > width { filled = width }
    if filled < 0 { filled = 0 }
    empty := width - filled
    return strings.Repeat("▇", filled) + strings.Repeat("░", empty)
}
```

**Step 4: Run test**

Run: `go test ./internal/monitor/dbtop/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/monitor/dbtop/renderer.go internal/monitor/dbtop/renderer_test.go
git commit -m "feat(dbtop): implement dashboard renderer with box drawing and bar charts"
```

---

## Task 6: REPL Refresh Loop

Add `runDbtop()` to the REPL that drives the in-place refresh loop: first frame via `writeOutputLine`, subsequent frames via ANSI cursor positioning.

**Files:**
- Create: `internal/ui/dbtop.go`
- Modify: `internal/ui/repl.go` (REPL struct, renderResult)

**Step 1: Create `internal/ui/dbtop.go`**

```go
// internal/ui/dbtop.go
package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/monitor/dbtop"
)

// runDbtop enters the real-time monitoring mode.
// It writes the first frame using writeOutputLine (handles scrolling),
// then overwrites in-place using ANSI positioning until the user presses q/ESC/Ctrl+C.
func (r *REPL) runDbtop(driver db.Driver, intervalSec int) {
	if intervalSec < 1 {
		intervalSec = 1
	}

	collector := dbtop.NewCollector(driver)
	interval := time.Duration(intervalSec) * time.Second

	// First frame: render and write via writeOutputLine to handle scroll.
	snap := collector.Collect(context.Background())
	lines := dbtop.Render(snap, r.cols, intervalSec)

	// Record starting row BEFORE writing.
	var startRow int
	if r.scrollMode {
		// In scroll mode, lines will scroll up from maxContentRow.
		// After writing N lines, startRow = maxContentRow - N + 1.
		// But writeOutputLine always writes at maxContentRow with scroll.
		// So after writing 28 lines, the first line is at maxContentRow - 27.
		maxRow := r.maxContentRow()
		for _, line := range lines {
			r.writeOutputLine(line)
		}
		startRow = maxRow - len(lines) + 1
		if startRow < 1 {
			startRow = 1
		}
	} else {
		startRow = r.contentRow
		for _, line := range lines {
			r.writeOutputLine(line)
		}
	}

	// Hide cursor during refresh.
	fmt.Fprint(r.writer, "\033[?25l")

	// Refresh loop.
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	buf := make([]byte, 16)
	done := make(chan struct{})

	// Keyboard listener goroutine.
	go func() {
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				close(done)
				return
			}
			for i := 0; i < n; i++ {
				switch buf[i] {
				case 'q', 'Q', 3: // q, Q, Ctrl+C
					close(done)
					return
				case 27: // ESC
					// Check if bare ESC (not CSI sequence).
					if i+1 < n && buf[i+1] == '[' {
						i += 2 // skip CSI sequence
						continue
					}
					close(done)
					return
				}
			}
		}
	}()

	for {
		select {
		case <-done:
			goto exit
		case <-ticker.C:
			snap = collector.Collect(context.Background())
			lines = dbtop.Render(snap, r.cols, intervalSec)
			// Overwrite in-place.
			for i, line := range lines {
				row := startRow + i
				fmt.Fprintf(r.writer, "\033[%d;1H\033[2K%s", row, line)
			}
		}
	}

exit:
	// Show cursor.
	fmt.Fprint(r.writer, "\033[?25h")
	// Position cursor after dbtop output.
	endRow := startRow + 28
	fmt.Fprintf(r.writer, "\033[%d;1H", endRow)

	// Update contentRow if not in scroll mode.
	if !r.scrollMode {
		r.contentRow = endRow
	}
}
```

**Step 2: Modify `internal/ui/repl.go` — renderResult for ResultRefresh**

Replace the existing `case skill.ResultRefresh:` block in `renderResult()` (~line 818):

```go
case skill.ResultRefresh:
    // dbtop returns the driver and interval in Data.
    if cfg, ok := result.Data.(*dbtopConfig); ok {
        r.runDbtop(cfg.driver, cfg.intervalSec)
    } else {
        // Fallback for other ResultRefresh types.
        text, _ := result.Data.(string)
        r.writeOutputLine("")
        for _, line := range strings.Split(text, "\n") {
            r.writeOutputLine(line)
        }
    }
```

Add the config type (can be in `repl.go` or `dbtop.go`):
```go
// dbtopConfig is passed from the dbtop skill to the REPL via ResultRefresh.
type dbtopConfig struct {
    driver      db.Driver
    intervalSec int
}
```

**Step 3: Modify `internal/skill/builtin/monitor/dbtop.go` — return driver + interval**

Rewrite `Execute()` to parse the interval argument and return the driver:

```go
func (s *DBTopSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
    intervalSec := 1
    if args := params.StringOr("args", ""); args != "" {
        if n, err := strconv.Atoi(strings.TrimSpace(args)); err == nil && n > 0 {
            intervalSec = n
        }
    }

    return &skill.Result{
        Type: skill.ResultRefresh,
        Data: &DbtopRefreshConfig{
            Driver:      s.driver,
            IntervalSec: intervalSec,
        },
    }, nil
}

// DbtopRefreshConfig carries the driver and settings to the REPL.
type DbtopRefreshConfig struct {
    Driver      db.Driver
    IntervalSec int
}
```

Note: The REPL's renderResult needs to import the monitor package or use an interface. To avoid circular imports, define the config type in a shared location or use type assertion. The simplest approach: check `result.Metadata["command"] == "dbtop"` and use the Data field.

**Step 4: Handle keyboard in REPL main loop**

The `runDbtop` method takes over `os.Stdin.Read`, which conflicts with the REPL's main event loop. The key insight: `runDbtop` blocks until exit, just like `browseTable`. The REPL's main loop is paused during dbtop because `handleEnter` → `renderResult` → `runDbtop` is synchronous.

**Step 5: Run tests**

Run: `go test ./internal/ui/ -v && go test ./internal/monitor/dbtop/ -v && go test ./internal/skill/builtin/monitor/ -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/ui/dbtop.go internal/ui/repl.go internal/skill/builtin/monitor/dbtop.go
git commit -m "feat(dbtop): implement REPL refresh loop with in-place ANSI rendering"
```

---

## Task 7: Update Existing dbtop Tests

Update the existing tests in `internal/skill/builtin/monitor/dbtop_test.go` to match the new Execute behavior (returns driver+interval config instead of a snapshot string).

**Files:**
- Modify: `internal/skill/builtin/monitor/dbtop_test.go`

**Step 1: Rewrite tests**

The new Execute returns a `DbtopRefreshConfig` in `result.Data` instead of a string. Update all tests:

```go
func TestDBTopSkill_Execute_ReturnsConfig(t *testing.T) {
    drv := mock.NewMockDriver()
    s := NewDBTopSkill(drv)

    result, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if result.Type != skill.ResultRefresh {
        t.Errorf("Type = %v, want ResultRefresh", result.Type)
    }

    cfg, ok := result.Data.(*DbtopRefreshConfig)
    if !ok {
        t.Fatalf("Data type = %T, want *DbtopRefreshConfig", result.Data)
    }
    if cfg.Driver != drv {
        t.Error("Driver should be the mock driver")
    }
    if cfg.IntervalSec != 1 {
        t.Errorf("IntervalSec = %d, want 1 (default)", cfg.IntervalSec)
    }
}

func TestDBTopSkill_Execute_CustomInterval(t *testing.T) {
    drv := mock.NewMockDriver()
    s := NewDBTopSkill(drv)

    params := skill.ParamsFromMap(map[string]any{"args": "3"})
    result, err := s.Execute(context.Background(), params)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    cfg, ok := result.Data.(*DbtopRefreshConfig)
    if !ok {
        t.Fatalf("Data type = %T, want *DbtopRefreshConfig", result.Data)
    }
    if cfg.IntervalSec != 3 {
        t.Errorf("IntervalSec = %d, want 3", cfg.IntervalSec)
    }
}

func TestDBTopSkill_Execute_InvalidInterval(t *testing.T) {
    drv := mock.NewMockDriver()
    s := NewDBTopSkill(drv)

    params := skill.ParamsFromMap(map[string]any{"args": "abc"})
    result, err := s.Execute(context.Background(), params)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    cfg := result.Data.(*DbtopRefreshConfig)
    if cfg.IntervalSec != 1 {
        t.Errorf("IntervalSec = %d, want 1 (fallback)", cfg.IntervalSec)
    }
}
```

**Step 2: Run test**

Run: `go test ./internal/skill/builtin/monitor/ -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/skill/builtin/monitor/dbtop_test.go
git commit -m "test(dbtop): update skill tests for new config-based Execute"
```

---

## Task 8: Integration Build & Verify

Ensure the full build passes and all tests are green.

**Step 1: Run full test suite**

Run: `go test ./... 2>&1 | tail -30`
Expected: All PASS

**Step 2: Build the binary**

Run: `go build -o /dev/null ./cmd/opendb/`
Expected: Success (exit 0)

**Step 3: Cross-compile for Linux deployment**

Run: `GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/opendb/`
Expected: Success (exit 0)

**Step 4: Run go vet**

Run: `go vet ./...`
Expected: No issues

**Step 5: Commit any fixes**

If any build/vet issues found, fix and commit:
```bash
git commit -m "fix(dbtop): resolve build issues"
```

---

## Summary

| Task | What | Files | Est. Lines |
|------|------|-------|------------|
| 1 | Data types | `types.go`, `types_test.go` | ~120 |
| 2 | Delta calc | `delta.go`, `delta_test.go` | ~150 |
| 3 | Health eval | `health.go`, `health_test.go` | ~120 |
| 4 | Collector | `collector.go`, `collector_test.go` | ~300 |
| 5 | Renderer | `renderer.go`, `renderer_test.go` | ~400 |
| 6 | REPL loop | `ui/dbtop.go`, modify `repl.go`, modify `dbtop.go` | ~200 |
| 7 | Update tests | modify `dbtop_test.go` | ~80 |
| 8 | Integration | build + vet | — |

All new code goes in `internal/monitor/dbtop/` (new package) and `internal/ui/dbtop.go` (new file).
Modifications to existing files are minimal: `repl.go` renderResult (~10 lines), `dbtop.go` Execute (~15 lines).
