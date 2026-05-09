/*-------------------------------------------------------------------------
 *
 * collector_test.go
 *	  Test cases for collector.go (dbtop package):
 *	  TestCollector_Collect_FirstFrame,
 *	  TestCollector_Collect_SecondFrame_HasDelta,
 *	  TestCollector_Collect_QueryError.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/monitor/dbtop/collector_test.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
)

// buildMockQueryFunc returns a QueryFunc that dispatches based on SQL content.
func buildMockQueryFunc() func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
	return func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		switch {
		case strings.Contains(sql, "database_role"):
			return &db.QueryResult{
				Columns: []string{"DATABASE_ROLE"},
				Rows:    [][]any{{"PRIMARY"}},
			}, nil

		case strings.Contains(sql, "v$sga"):
			return &db.QueryResult{
				Columns: []string{"SGA_MB"},
				Rows:    [][]any{{float64(4096)}},
			}, nil

		case strings.Contains(sql, "v$pgastat"):
			return &db.QueryResult{
				Columns: []string{"PGA_MB"},
				Rows:    [][]any{{float64(512)}},
			}, nil

		case strings.Contains(sql, "v$sysstat"):
			return &db.QueryResult{
				Columns: []string{"NAME", "VALUE"},
				Rows: [][]any{
					{"user commits", int64(1000)},
					{"user rollbacks", int64(50)},
					{"execute count", int64(5000)},
					{"redo size", int64(10240000)},
				},
			}, nil

		case strings.Contains(sql, "COUNT") && strings.Contains(sql, "v$session"):
			return &db.QueryResult{
				Columns: []string{"TOTAL", "AN", "ASC", "ASI", "IDL"},
				Rows:    [][]any{{int64(120), int64(15), int64(5), int64(3), int64(97)}},
			}, nil

		// Must check v$system_event before v$sys_time_model because
		// eventSQL contains both strings in its subquery.
		case strings.Contains(sql, "v$system_event"):
			// 4 events so each PCT < 30% (evtPctWarn threshold), keeping Health=Healthy.
			// Total = 1e6*4 = 4e6; each = 25%.
			return &db.QueryResult{
				Columns: []string{"EVENT", "TOTAL_WAITS", "TIME_WAITED_MICRO", "WAIT_CLASS"},
				Rows: [][]any{
					{"DB CPU", int64(0), int64(1000000), "CPU"},
					{"db file sequential read", int64(50000), int64(1000000), "User I/O"},
					{"log file sync", int64(20000), int64(1000000), "Commit"},
					{"db file parallel read", int64(10000), int64(1000000), "User I/O"},
				},
			}, nil

		case strings.Contains(sql, "v$sys_time_model"):
			return &db.QueryResult{
				Columns: []string{"STAT_NAME", "VALUE"},
				Rows: [][]any{
					{"DB CPU", int64(5000000)},
					{"DB time", int64(8000000)},
				},
			}, nil

		case strings.Contains(sql, "v$session") && strings.Contains(sql, "ACTIVE"):
			return &db.QueryResult{
				Columns: []string{"SID", "USERNAME", "SQL_ID", "EVENT", "WAIT_CLASS", "ELAPSED_SEC", "SQL_TEXT", "PROGRAM", "STATUS"},
				Rows: [][]any{
					{int64(142), "APP_USER", "abc123def", "db file sequential read", "User I/O", float64(45.2), "SELECT * FROM orders", "sqlplus.exe", "ACTIVE"},
					{int64(305), "BATCH_USER", "xyz789ghi", "CPU", "CPU", float64(12.8), "UPDATE inventory SET qty=1", "java.exe", "ACTIVE"},
				},
			}, nil

		default:
			return &db.QueryResult{}, nil
		}
	}
}

func TestCollector_Collect_FirstFrame(t *testing.T) {
	md := mock.NewMockDriver()
	md.ServerInfoVal = db.ServerInfo{
		Version:      "19.3.0.0.0",
		InstanceName: "ORCL",
		Hostname:     "dbhost01",
	}
	md.QueryFunc = buildMockQueryFunc()

	c := NewCollector(md)
	snap := c.Collect(context.Background())

	// DB info
	if snap.Version != "19.3.0.0.0" {
		t.Errorf("Version = %q, want %q", snap.Version, "19.3.0.0.0")
	}
	if snap.InstanceName != "ORCL" {
		t.Errorf("InstanceName = %q, want %q", snap.InstanceName, "ORCL")
	}
	if snap.DBRole != "PRIMARY" {
		t.Errorf("DBRole = %q, want %q", snap.DBRole, "PRIMARY")
	}

	// Memory
	if snap.SGAMaxMB != 4096 {
		t.Errorf("SGAMB = %.0f, want 4096", snap.SGAMaxMB)
	}
	if snap.PGAUsedMB != 512 {
		t.Errorf("PGAMB = %.0f, want 512", snap.PGAUsedMB)
	}

	// Session counts
	if snap.TotalSessions != 120 {
		t.Errorf("TotalSessions = %d, want 120", snap.TotalSessions)
	}
	if snap.ActiveCount != 15 {
		t.Errorf("ActiveCount = %d, want 15", snap.ActiveCount)
	}
	if snap.ActiveCPU != 5 {
		t.Errorf("ActiveCPU = %d, want 5", snap.ActiveCPU)
	}
	if snap.ActiveIO != 3 {
		t.Errorf("ActiveIO = %d, want 3", snap.ActiveIO)
	}
	if snap.IdleCount != 97 {
		t.Errorf("IdleCount = %d, want 97", snap.IdleCount)
	}

	// Wait events
	if len(snap.Events) != 4 {
		t.Errorf("Events count = %d, want 4 (DB CPU + 3 events)", len(snap.Events))
	}

	// Active sessions
	if len(snap.Sessions) != 2 {
		t.Errorf("Sessions count = %d, want 2", len(snap.Sessions))
	}
	if len(snap.Sessions) > 0 {
		if snap.Sessions[0].SID != 142 {
			t.Errorf("Sessions[0].SID = %d, want 142", snap.Sessions[0].SID)
		}
		if snap.Sessions[0].Username != "APP_USER" {
			t.Errorf("Sessions[0].Username = %q, want %q", snap.Sessions[0].Username, "APP_USER")
		}
	}

	// First frame: no delta
	if snap.HasDelta {
		t.Error("first frame should have HasDelta=false")
	}

	// Timestamp should be set
	if snap.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}

	// Health should be evaluated
	if snap.Health != Healthy {
		t.Errorf("Health = %v, want Healthy", snap.Health)
	}
}

func TestCollector_Collect_SecondFrame_HasDelta(t *testing.T) {
	md := mock.NewMockDriver()
	md.ServerInfoVal = db.ServerInfo{
		Version:      "19.3.0.0.0",
		InstanceName: "ORCL",
	}
	md.QueryFunc = buildMockQueryFunc()

	c := NewCollector(md)

	// First frame — baseline
	_ = c.Collect(context.Background())

	// Second frame — should have delta
	snap := c.Collect(context.Background())

	if !snap.HasDelta {
		t.Error("second frame should have HasDelta=true")
	}

	// Verify DB info is still populated
	if snap.Version != "19.3.0.0.0" {
		t.Errorf("Version = %q, want %q", snap.Version, "19.3.0.0.0")
	}
	if snap.DBRole != "PRIMARY" {
		t.Errorf("DBRole = %q, want %q", snap.DBRole, "PRIMARY")
	}
}

func TestCollector_Collect_QueryError(t *testing.T) {
	md := mock.NewMockDriver()
	md.ServerInfoVal = db.ServerInfo{
		Version:      "19.3.0.0.0",
		InstanceName: "ORCL",
	}
	// All queries fail
	md.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return nil, errors.New("ORA-01219: database not open")
	}

	c := NewCollector(md)
	snap := c.Collect(context.Background())

	// Should still return a snapshot with cached info
	if snap.Version != "19.3.0.0.0" {
		t.Errorf("Version = %q, want cached %q", snap.Version, "19.3.0.0.0")
	}
	if snap.InstanceName != "ORCL" {
		t.Errorf("InstanceName = %q, want cached %q", snap.InstanceName, "ORCL")
	}

	// Numeric fields should be zero-valued (graceful degradation)
	if snap.SGAMaxMB != 0 {
		t.Errorf("SGAMB = %.0f, want 0 on error", snap.SGAMaxMB)
	}
	if snap.PGAUsedMB != 0 {
		t.Errorf("PGAMB = %.0f, want 0 on error", snap.PGAUsedMB)
	}
	if snap.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0 on error", snap.TotalSessions)
	}
	if len(snap.Events) != 0 {
		t.Errorf("Events = %d, want 0 on error", len(snap.Events))
	}
	if len(snap.Sessions) != 0 {
		t.Errorf("Sessions = %d, want 0 on error", len(snap.Sessions))
	}
	if snap.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero even on error")
	}
}

func TestCollector_SetMode(t *testing.T) {
	md := mock.NewMockDriver()
	md.ServerInfoVal = db.ServerInfo{Version: "19.3.0", InstanceName: "ORCL"}
	c := NewCollector(md)

	if c.Mode() != ModeNormal {
		t.Errorf("initial mode = %d, want ModeNormal", c.Mode())
	}
	c.SetMode(ModeBurst)
	if c.Mode() != ModeBurst {
		t.Errorf("after SetMode(ModeBurst), mode = %d, want ModeBurst", c.Mode())
	}
	c.SetMode(ModeNormal)
	if c.Mode() != ModeNormal {
		t.Errorf("after SetMode(ModeNormal), mode = %d, want ModeNormal", c.Mode())
	}
}

// buildBurstMockQueryFunc returns a QueryFunc that includes burst-mode columns.
func buildBurstMockQueryFunc() func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
	base := buildMockQueryFunc()
	return func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
		// Intercept burst session SQL (contains BLOCKING_SESSION)
		if strings.Contains(sql, "BLOCKING_SESSION") {
			return &db.QueryResult{
				Columns: []string{
					"SID", "USERNAME", "SQL_ID", "EVENT", "WAIT_CLASS",
					"ELAPSED_SEC", "SQL_TEXT", "PROGRAM", "STATUS",
					"BLOCKING_SID", "PLAN_HASH_VALUE", "MACHINE", "WAIT_TIME_SEC", "SQL_CHILD_NUMBER",
				},
				Rows: [][]any{
					{
						int64(142), "APP_USER", "abc123def", "db file sequential read", "User I/O",
						float64(45.2), "SELECT * FROM orders WHERE id = :1", "sqlplus.exe", "ACTIVE",
						int64(305), int64(3847291056), "app-server-01", float64(2.3), int64(0),
					},
				},
			}, nil
		}
		return base(ctx, sql, args...)
	}
}

func TestCollector_Collect_BurstMode(t *testing.T) {
	md := mock.NewMockDriver()
	md.ServerInfoVal = db.ServerInfo{Version: "19.3.0", InstanceName: "ORCL"}
	md.QueryFunc = buildBurstMockQueryFunc()

	c := NewCollector(md)
	c.SetMode(ModeBurst)

	snap := c.Collect(context.Background())

	if len(snap.Sessions) == 0 {
		t.Fatal("expected sessions in burst mode, got 0")
	}

	s := snap.Sessions[0]
	if s.Burst == nil {
		t.Fatal("Burst detail should be populated in burst mode")
	}
	if s.Burst.BlockingSID != 305 {
		t.Errorf("BlockingSID = %d, want 305", s.Burst.BlockingSID)
	}
	if s.Burst.PlanHashValue != 3847291056 {
		t.Errorf("PlanHashValue = %d, want 3847291056", s.Burst.PlanHashValue)
	}
	if s.Burst.Machine != "app-server-01" {
		t.Errorf("Machine = %q, want %q", s.Burst.Machine, "app-server-01")
	}
	if s.Burst.WaitTimeSec != 2.3 {
		t.Errorf("WaitTimeSec = %f, want 2.3", s.Burst.WaitTimeSec)
	}
}

func TestCollector_Collect_NormalMode_NoBurstDetail(t *testing.T) {
	md := mock.NewMockDriver()
	md.ServerInfoVal = db.ServerInfo{Version: "19.3.0", InstanceName: "ORCL"}
	md.QueryFunc = buildMockQueryFunc()

	c := NewCollector(md)
	// Ensure normal mode (default)
	snap := c.Collect(context.Background())

	for _, s := range snap.Sessions {
		if s.Burst != nil {
			t.Errorf("session SID=%d should not have BurstDetail in normal mode", s.SID)
		}
	}
}
