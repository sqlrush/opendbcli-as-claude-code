/*-------------------------------------------------------------------------
 *
 * failover_test.go
 *	  Test cases for failover.go (overlord package):
 *	  TestAnalyze_NilDBAccess_Oracle, TestAnalyze_NilDBAccess_MySQL,
 *	  TestAnalyze_NilDBAccess_PostgreSQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/failover_test.go
 *
 *-------------------------------------------------------------------------
 */
package overlord

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// mockDriver implements db.Driver for testing.
type mockDriver struct {
	queryFunc func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
}

func (d *mockDriver) Close() error                                           { return nil }
func (d *mockDriver) Exec(context.Context, string, ...any) (*db.ExecResult, error) { return nil, nil }
func (d *mockDriver) BeginTx(context.Context, *db.TxOptions) (db.Tx, error)      { return nil, nil }
func (d *mockDriver) Ping(context.Context) error                                  { return nil }
func (d *mockDriver) ServerInfo() db.ServerInfo                                    { return db.ServerInfo{} }

func (d *mockDriver) Query(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
	if d.queryFunc != nil {
		return d.queryFunc(ctx, sql, args...)
	}
	return &db.QueryResult{}, nil
}

// --- Tests for Analyze with nil dbAccess (template fallback) ---

func TestAnalyze_NilDBAccess_Oracle(t *testing.T) {
	analyzer := NewFailoverAnalyzer(nil)
	report, err := analyzer.Analyze(context.Background(), "primary-01", "standby-01", "oracle")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertReportBasics(t, report, "oracle", "primary-01", "standby-01")
	if report.SyncStatus != "unknown (no database access configured)" {
		t.Errorf("expected unknown sync status, got %q", report.SyncStatus)
	}
	if report.RiskLevel != "high" {
		t.Errorf("expected high risk (unknown status), got %q", report.RiskLevel)
	}
	if len(report.Steps) != 6 {
		t.Errorf("expected 6 oracle steps, got %d", len(report.Steps))
	}
}

func TestAnalyze_NilDBAccess_MySQL(t *testing.T) {
	analyzer := NewFailoverAnalyzer(nil)
	report, err := analyzer.Analyze(context.Background(), "primary-01", "standby-01", "mysql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertReportBasics(t, report, "mysql", "primary-01", "standby-01")
	if len(report.Steps) != 6 {
		t.Errorf("expected 6 mysql steps, got %d", len(report.Steps))
	}
}

func TestAnalyze_NilDBAccess_PostgreSQL(t *testing.T) {
	analyzer := NewFailoverAnalyzer(nil)
	report, err := analyzer.Analyze(context.Background(), "primary-01", "standby-01", "postgresql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertReportBasics(t, report, "postgresql", "primary-01", "standby-01")
	if len(report.Steps) != 6 {
		t.Errorf("expected 6 postgresql steps, got %d", len(report.Steps))
	}
}

func TestAnalyze_NilDBAccess_Generic(t *testing.T) {
	analyzer := NewFailoverAnalyzer(nil)
	report, err := analyzer.Analyze(context.Background(), "primary-01", "standby-01", "mongodb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertReportBasics(t, report, "mongodb", "primary-01", "standby-01")
	if len(report.Steps) != 5 {
		t.Errorf("expected 5 generic steps, got %d", len(report.Steps))
	}
}

func TestAnalyze_ValidationErrors(t *testing.T) {
	analyzer := NewFailoverAnalyzer(nil)

	_, err := analyzer.Analyze(context.Background(), "", "standby-01", "oracle")
	if err == nil {
		t.Error("expected error for empty primary")
	}

	_, err = analyzer.Analyze(context.Background(), "primary-01", "", "oracle")
	if err == nil {
		t.Error("expected error for empty standby")
	}

	_, err = analyzer.Analyze(context.Background(), "primary-01", "standby-01", "")
	if err == nil {
		t.Error("expected error for empty dbType")
	}
}

// --- Tests for Analyze with mock dbAccess ---

func TestAnalyze_WithMockDBAccess_Oracle(t *testing.T) {
	dam := newMockDBAccessManager(func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		if strings.Contains(sql, "V$DATAGUARD_STATS") {
			return &db.QueryResult{
				Columns: []string{"NAME", "VALUE", "DATUM_TIME"},
				Rows: [][]any{
					{"transport lag", "+00 00:00:02", "2026-04-11 10:00:00"},
					{"apply lag", "+00 00:00:05", "2026-04-11 10:00:00"},
				},
			}, nil
		}
		return &db.QueryResult{}, nil
	})

	analyzer := NewFailoverAnalyzer(dam)
	report, err := analyzer.Analyze(context.Background(), "primary-ora", "standby-ora", "oracle")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.SyncStatus == "unknown (no database access configured)" {
		t.Error("expected sync status from mock, got unknown")
	}
	if !strings.Contains(report.SyncStatus, "transport lag") {
		t.Errorf("expected sync status to contain dataguard info, got %q", report.SyncStatus)
	}
	if report.RiskLevel != "medium" {
		t.Errorf("expected medium risk for known sync status, got %q", report.RiskLevel)
	}
}

func TestAnalyze_WithMockDBAccess_MySQL(t *testing.T) {
	dam := newMockDBAccessManager(func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		if strings.Contains(sql, "REPLICA STATUS") {
			return &db.QueryResult{
				Columns: []string{"Replica_IO_Running", "Replica_SQL_Running", "Seconds_Behind_Source"},
				Rows:    [][]any{{"Yes", "Yes", 0}},
			}, nil
		}
		return &db.QueryResult{}, nil
	})

	analyzer := NewFailoverAnalyzer(dam)
	report, err := analyzer.Analyze(context.Background(), "primary-my", "standby-my", "mysql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(report.SyncStatus, "Yes") {
		t.Errorf("expected sync status to contain replica info, got %q", report.SyncStatus)
	}
}

func TestAnalyze_WithMockDBAccess_PostgreSQL(t *testing.T) {
	dam := newMockDBAccessManager(func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		if strings.Contains(sql, "pg_stat_replication") {
			return &db.QueryResult{
				Columns: []string{"client_addr", "state", "sent_lsn", "write_lsn", "flush_lsn", "replay_lsn", "sync_state"},
				Rows:    [][]any{{"10.0.0.2", "streaming", "0/5000000", "0/5000000", "0/5000000", "0/4FFFF00", "async"}},
			}, nil
		}
		return &db.QueryResult{}, nil
	})

	analyzer := NewFailoverAnalyzer(dam)
	report, err := analyzer.Analyze(context.Background(), "primary-pg", "standby-pg", "postgres")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(report.SyncStatus, "streaming") {
		t.Errorf("expected sync status to contain PG replication info, got %q", report.SyncStatus)
	}
}

// --- Tests for RenderFailoverMarkdown ---

func TestRenderFailoverMarkdown_ContainsAllSections(t *testing.T) {
	report := &FailoverReport{
		Timestamp:   time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC),
		PrimaryHost: "primary-01",
		StandbyHost: "standby-01",
		DBType:      "oracle",
		SyncStatus:  "transport lag: 0, apply lag: 2s",
		RiskLevel:   "medium",
		PreChecks: []string{
			"Confirm standby is mounted",
			"Confirm no active long-running transactions",
		},
		Steps: []FailoverStep{
			{
				Order:       1,
				Description: "Check sync status",
				SQL:         "SELECT * FROM V$DATAGUARD_STATS;",
				Risk:        "low",
				Expected:    "Lag near zero",
				VerifySQL:   "SELECT DATABASE_ROLE FROM V$DATABASE;",
				Notes:       "Read-only query",
			},
			{
				Order:       2,
				Description: "Promote standby",
				SQL:         "ALTER DATABASE COMMIT TO SWITCHOVER TO PRIMARY;",
				Risk:        "critical",
				Expected:    "Standby becomes primary",
				VerifySQL:   "SELECT OPEN_MODE FROM V$DATABASE;",
				Notes:       "",
			},
		},
		RollbackPlan:      "Reinstate original Data Guard configuration",
		EstimatedDowntime: "5-15 minutes",
	}

	md := RenderFailoverMarkdown(report)

	requiredSections := []string{
		"# Failover Report",
		"## Pre-Checks",
		"## Failover Steps",
		"## Rollback Plan",
		"## Estimated Downtime",
	}
	for _, section := range requiredSections {
		if !strings.Contains(md, section) {
			t.Errorf("rendered markdown missing section %q", section)
		}
	}

	// Verify report metadata.
	if !strings.Contains(md, "2026-04-11 10:00:00") {
		t.Error("missing timestamp in rendered output")
	}
	if !strings.Contains(md, "oracle") {
		t.Error("missing db type in rendered output")
	}
	if !strings.Contains(md, "primary-01") {
		t.Error("missing primary host in rendered output")
	}
	if !strings.Contains(md, "medium") {
		t.Error("missing risk level in rendered output")
	}
}

func TestRenderFailoverMarkdown_StepHasSQLAndVerify(t *testing.T) {
	report := &FailoverReport{
		Timestamp:   time.Now(),
		PrimaryHost: "p", StandbyHost: "s", DBType: "oracle",
		SyncStatus: "ok", RiskLevel: "low",
		PreChecks: []string{"check1"},
		Steps: []FailoverStep{
			{
				Order:       1,
				Description: "Test step",
				SQL:         "ALTER DATABASE OPEN;",
				Risk:        "critical",
				Expected:    "Database opens",
				VerifySQL:   "SELECT OPEN_MODE FROM V$DATABASE;",
				Notes:       "Important note",
			},
		},
		RollbackPlan:      "rollback",
		EstimatedDowntime: "5 min",
	}

	md := RenderFailoverMarkdown(report)

	// Each step must have SQL block, risk, expected, and verify SQL.
	if !strings.Contains(md, "ALTER DATABASE OPEN;") {
		t.Error("missing step SQL in rendered output")
	}
	if !strings.Contains(md, "SELECT OPEN_MODE FROM V$DATABASE;") {
		t.Error("missing verify SQL in rendered output")
	}
	if !strings.Contains(md, "**Risk:** critical") {
		t.Error("missing risk annotation in rendered output")
	}
	if !strings.Contains(md, "**Expected:** Database opens") {
		t.Error("missing expected outcome in rendered output")
	}
	if !strings.Contains(md, "**Notes:** Important note") {
		t.Error("missing notes in rendered output")
	}
	if !strings.Contains(md, "```sql") {
		t.Error("missing SQL code block markers")
	}
}

func TestRenderFailoverMarkdown_EachStepHasRiskAndVerification(t *testing.T) {
	analyzer := NewFailoverAnalyzer(nil)
	report, err := analyzer.Analyze(context.Background(), "p", "s", "oracle")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, step := range report.Steps {
		if step.SQL == "" {
			t.Errorf("step %d (%s) has empty SQL", step.Order, step.Description)
		}
		if step.Risk == "" {
			t.Errorf("step %d (%s) has empty risk", step.Order, step.Description)
		}
		if step.VerifySQL == "" {
			t.Errorf("step %d (%s) has empty verify SQL", step.Order, step.Description)
		}
		if step.Expected == "" {
			t.Errorf("step %d (%s) has empty expected outcome", step.Order, step.Description)
		}
	}
}

// --- Tests for FailoverSkill integration ---

func TestFailoverSkill_Execute_Integration(t *testing.T) {
	// We cannot use real gRPC workers in unit tests, so we test that the
	// skill falls through to the analyzer when GetWorkerStatus fails.
	wm := NewWorkerManager("overlord-test", "test-region")
	sk := NewFailoverSkill(wm, nil)

	params := skill.ParamsFromMap(map[string]any{
		"primary_worker": "primary-test",
		"standby_worker": "standby-test",
	})

	result, err := sk.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still generate a report (fallback to oracle when workers not found).
	if result.Type != skill.ResultText {
		t.Errorf("expected ResultText, got %v", result.Type)
	}
	if !strings.Contains(result.Rendered, "# Failover Report") {
		t.Error("expected rendered markdown with failover report header")
	}
	if !strings.Contains(result.Rendered, "## Failover Steps") {
		t.Error("expected rendered markdown with failover steps section")
	}
	if !strings.Contains(result.Summary, "failover report for") {
		t.Errorf("expected summary to contain 'failover report for', got %q", result.Summary)
	}
}

func TestFailoverSkill_Validate(t *testing.T) {
	wm := NewWorkerManager("overlord-test", "test-region")
	sk := NewFailoverSkill(wm, nil)

	// Missing primary.
	err := sk.Validate(skill.ParamsFromMap(map[string]any{"standby_worker": "s"}))
	if err == nil {
		t.Error("expected validation error for missing primary_worker")
	}

	// Missing standby.
	err = sk.Validate(skill.ParamsFromMap(map[string]any{"primary_worker": "p"}))
	if err == nil {
		t.Error("expected validation error for missing standby_worker")
	}

	// Valid.
	err = sk.Validate(skill.ParamsFromMap(map[string]any{
		"primary_worker": "p",
		"standby_worker": "s",
	}))
	if err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

// --- Test helpers ---

func assertReportBasics(t *testing.T, r *FailoverReport, dbType, primary, standby string) {
	t.Helper()
	if r.DBType != dbType {
		t.Errorf("expected dbType %q, got %q", dbType, r.DBType)
	}
	if r.PrimaryHost != primary {
		t.Errorf("expected primary %q, got %q", primary, r.PrimaryHost)
	}
	if r.StandbyHost != standby {
		t.Errorf("expected standby %q, got %q", standby, r.StandbyHost)
	}
	if r.RiskLevel == "" {
		t.Error("risk level should not be empty")
	}
	if len(r.PreChecks) == 0 {
		t.Error("pre-checks should not be empty")
	}
	if len(r.Steps) == 0 {
		t.Error("steps should not be empty")
	}
	if r.RollbackPlan == "" {
		t.Error("rollback plan should not be empty")
	}
	if r.EstimatedDowntime == "" {
		t.Error("estimated downtime should not be empty")
	}
}

// newMockDBAccessManager creates a DBAccessManager with a mock driver factory.
func newMockDBAccessManager(queryFunc func(context.Context, string, ...any) (*db.QueryResult, error)) *DBAccessManager {
	dam := NewDBAccessManager()
	dam.SetDriverFactory(func(cfg db.ConnectionConfig) (db.Driver, error) {
		return &mockDriver{queryFunc: queryFunc}, nil
	})
	// Register a dummy config for any worker.
	for _, wid := range []string{"primary-ora", "standby-ora", "primary-my", "standby-my", "primary-pg", "standby-pg"} {
		dam.RegisterWorkerDB(wid, db.ConnectionConfig{DBType: "mock", Host: "localhost"})
	}
	return dam
}
