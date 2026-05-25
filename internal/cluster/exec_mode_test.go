/*-------------------------------------------------------------------------
 *
 * exec_mode_test.go
 *	  Test cases for exec_mode.go (cluster package):
 *	  TestReportOnlyHandler_LLMInitiator_NeverExecutes,
 *	  TestReportOnlyHandler_LLMInitiator_ReadOnly_StillNotExecuted,
 *	  TestReportOnlyHandler_UserInitiator_LowRisk_Executes.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/exec_mode_test.go
 *
 *-------------------------------------------------------------------------
 */
package cluster

import (
	"context"
	"fmt"
	"testing"
)

// mockExecutor is a test double for SQLExecutor.
type mockExecutor struct {
	output string
	err    error
	called bool
	lastSQL string
}

func (m *mockExecutor) ExecSQL(_ context.Context, sql string) (string, error) {
	m.called = true
	m.lastSQL = sql
	return m.output, m.err
}

func TestReportOnlyHandler_LLMInitiator_NeverExecutes(t *testing.T) {
	exec := &mockExecutor{output: "should not see this"}
	handler := NewReportOnlyHandler(exec)

	action := NewSQLAction(
		"ALTER SYSTEM SET db_cache_size=2G",
		"increase buffer cache",
		RiskHigh,
		CategoryParameter,
	)

	result, err := handler.Handle(context.Background(), InitiatorLLM, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Executed {
		t.Error("LLM action should not be executed in report_only mode")
	}
	if result.Output != "added to report: increase buffer cache" {
		t.Errorf("unexpected output: %s", result.Output)
	}
	if exec.called {
		t.Error("executor should not have been called for LLM initiator")
	}
}

func TestReportOnlyHandler_LLMInitiator_ReadOnly_StillNotExecuted(t *testing.T) {
	exec := &mockExecutor{output: "rows returned"}
	handler := NewReportOnlyHandler(exec)

	// Even read_only category with low risk — LLM write actions go to report.
	action := NewSQLAction(
		"SELECT count(*) FROM v$session",
		"count active sessions",
		RiskLow,
		CategoryReadOnly,
	)

	result, err := handler.Handle(context.Background(), InitiatorLLM, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Executed {
		t.Error("LLM action should not be executed even for read_only category")
	}
	if exec.called {
		t.Error("executor should not have been called for LLM initiator")
	}
}

func TestReportOnlyHandler_UserInitiator_LowRisk_Executes(t *testing.T) {
	exec := &mockExecutor{output: "1 row updated"}
	handler := NewReportOnlyHandler(exec)

	action := NewSQLAction(
		"EXEC DBMS_STATS.GATHER_TABLE_STATS('HR','EMPLOYEES')",
		"gather table statistics",
		RiskLow,
		CategoryDML,
	)

	result, err := handler.Handle(context.Background(), InitiatorUser, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Executed {
		t.Error("user low-risk action should be executed")
	}
	if result.Output != "1 row updated" {
		t.Errorf("unexpected output: %s", result.Output)
	}
	if !exec.called {
		t.Error("executor should have been called")
	}
	if exec.lastSQL != "EXEC DBMS_STATS.GATHER_TABLE_STATS('HR','EMPLOYEES')" {
		t.Errorf("wrong SQL passed to executor: %s", exec.lastSQL)
	}
}

func TestReportOnlyHandler_UserInitiator_MediumRisk_Executes(t *testing.T) {
	exec := &mockExecutor{output: "session killed"}
	handler := NewReportOnlyHandler(exec)

	action := NewSQLAction(
		"ALTER SYSTEM KILL SESSION '123,456'",
		"kill blocking session",
		RiskMedium,
		CategoryKillSession,
	)

	result, err := handler.Handle(context.Background(), InitiatorUser, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Executed {
		t.Error("user medium-risk action should be executed without confirmation")
	}
}

func TestReportOnlyHandler_UserInitiator_HighRisk_NeedsConfirmation(t *testing.T) {
	exec := &mockExecutor{output: "should not execute"}
	handler := NewReportOnlyHandler(exec)

	action := NewSQLAction(
		"DROP INDEX hr.emp_idx",
		"drop unused index",
		RiskHigh,
		CategoryDDL,
	).WithRollback("CREATE INDEX hr.emp_idx ON hr.employees(emp_id)")

	result, err := handler.Handle(context.Background(), InitiatorUser, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Executed {
		t.Error("user high-risk action should require confirmation, not execute directly")
	}
	if result.Output == "" {
		t.Error("should return a confirmation message")
	}
	if exec.called {
		t.Error("executor should not have been called for high-risk user action")
	}
}

func TestReportOnlyHandler_UserInitiator_CriticalRisk_NeedsConfirmation(t *testing.T) {
	exec := &mockExecutor{output: "should not execute"}
	handler := NewReportOnlyHandler(exec)

	action := NewSQLAction(
		"ALTER DATABASE SWITCHOVER TO standby_db",
		"switchover to standby",
		RiskCritical,
		CategoryFailover,
	)

	result, err := handler.Handle(context.Background(), InitiatorUser, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Executed {
		t.Error("user critical-risk action should require confirmation")
	}
	if exec.called {
		t.Error("executor should not have been called for critical-risk action")
	}
}

func TestReportOnlyHandler_TestInitiator_AlwaysExecutes(t *testing.T) {
	tests := []struct {
		name string
		risk RiskLevel
	}{
		{"low risk", RiskLow},
		{"medium risk", RiskMedium},
		{"high risk", RiskHigh},
		{"critical risk", RiskCritical},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &mockExecutor{output: "injected fault"}
			handler := NewReportOnlyHandler(exec)

			action := NewSQLAction(
				"INSERT INTO test_fault_table VALUES(1)",
				"inject test fault",
				tt.risk,
				CategoryDML,
			)

			result, err := handler.Handle(context.Background(), InitiatorTest, action)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.Executed {
				t.Errorf("test initiator should always execute, even at %s risk", tt.risk)
			}
			if !exec.called {
				t.Error("executor should have been called for test initiator")
			}
		})
	}
}

func TestReportOnlyHandler_ExecutionError(t *testing.T) {
	exec := &mockExecutor{err: fmt.Errorf("ORA-01017: invalid username/password")}
	handler := NewReportOnlyHandler(exec)

	action := NewSQLAction(
		"SELECT 1 FROM dual",
		"test connectivity",
		RiskLow,
		CategoryReadOnly,
	)

	result, err := handler.Handle(context.Background(), InitiatorUser, action)
	if err != nil {
		t.Fatalf("handler should not return error for execution failure: %v", err)
	}
	if result.Executed {
		t.Error("failed execution should set Executed=false")
	}
	if result.Error == "" {
		t.Error("should contain error message")
	}
}

func TestReportOnlyHandler_UnknownInitiator(t *testing.T) {
	exec := &mockExecutor{}
	handler := NewReportOnlyHandler(exec)

	action := NewSQLAction("SELECT 1", "test", RiskLow, CategoryReadOnly)

	_, err := handler.Handle(context.Background(), Initiator("unknown"), action)
	if err == nil {
		t.Error("unknown initiator should return error")
	}
}

func TestSQLAction_ImmutableBuilders(t *testing.T) {
	original := NewSQLAction(
		"SELECT 1",
		"test query",
		RiskLow,
		CategoryReadOnly,
	)

	withEffect := original.WithExpectedEffect("returns 1")
	withRollback := original.WithRollback("no rollback needed")
	withVerify := original.WithVerifySQL("SELECT 1 FROM dual")

	// Original should not be mutated.
	if original.ExpectedEffect != "" {
		t.Error("original ExpectedEffect should be empty")
	}
	if original.Rollback != "" {
		t.Error("original Rollback should be empty")
	}
	if original.VerifySQL != "" {
		t.Error("original VerifySQL should be empty")
	}

	// Copies should have the new values.
	if withEffect.ExpectedEffect != "returns 1" {
		t.Errorf("withEffect.ExpectedEffect = %q, want %q", withEffect.ExpectedEffect, "returns 1")
	}
	if withRollback.Rollback != "no rollback needed" {
		t.Errorf("withRollback.Rollback = %q, want %q", withRollback.Rollback, "no rollback needed")
	}
	if withVerify.VerifySQL != "SELECT 1 FROM dual" {
		t.Errorf("withVerify.VerifySQL = %q, want %q", withVerify.VerifySQL, "SELECT 1 FROM dual")
	}

	// Copies should preserve original fields.
	if withEffect.SQL != "SELECT 1" {
		t.Error("withEffect should preserve SQL field")
	}
	if withEffect.Risk != RiskLow {
		t.Error("withEffect should preserve Risk field")
	}
}

func TestSQLAction_ChainedBuilders(t *testing.T) {
	action := NewSQLAction(
		"ALTER SYSTEM SET memory_target=4G",
		"increase memory",
		RiskHigh,
		CategoryParameter,
	).WithExpectedEffect("memory increased to 4G").
		WithRollback("ALTER SYSTEM SET memory_target=2G").
		WithVerifySQL("SELECT value FROM v$parameter WHERE name='memory_target'")

	if action.SQL != "ALTER SYSTEM SET memory_target=4G" {
		t.Error("SQL should be preserved through chain")
	}
	if action.ExpectedEffect != "memory increased to 4G" {
		t.Error("ExpectedEffect should be set")
	}
	if action.Rollback != "ALTER SYSTEM SET memory_target=2G" {
		t.Error("Rollback should be set")
	}
	if action.VerifySQL != "SELECT value FROM v$parameter WHERE name='memory_target'" {
		t.Error("VerifySQL should be set")
	}
}

func TestRiskLevel_NeedsConfirmation(t *testing.T) {
	tests := []struct {
		risk RiskLevel
		want bool
	}{
		{RiskLow, false},
		{RiskMedium, false},
		{RiskHigh, true},
		{RiskCritical, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.risk), func(t *testing.T) {
			if got := tt.risk.NeedsConfirmation(); got != tt.want {
				t.Errorf("RiskLevel(%q).NeedsConfirmation() = %v, want %v", tt.risk, got, tt.want)
			}
		})
	}
}

func TestActionCategory_Constants(t *testing.T) {
	// Verify all categories are distinct and non-empty.
	categories := []ActionCategory{
		CategoryReadOnly,
		CategoryKillSession,
		CategoryDDL,
		CategoryDML,
		CategoryParameter,
		CategoryFailover,
	}

	seen := make(map[ActionCategory]bool)
	for _, cat := range categories {
		if cat == "" {
			t.Errorf("category should not be empty")
		}
		if seen[cat] {
			t.Errorf("duplicate category: %s", cat)
		}
		seen[cat] = true
	}
}

func TestParseExecMode(t *testing.T) {
	tests := []struct {
		input   string
		want    ExecMode
		wantErr bool
	}{
		{"report_only", ExecModeReportOnly, false},
		{"confirm_exec", ExecModeConfirmExec, false},
		{"auto_exec", ExecModeAutoExec, false},
		{"  report_only  ", ExecModeReportOnly, false}, // trimmed
		{"invalid", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseExecMode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseExecMode(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseExecMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInitiator_Constants(t *testing.T) {
	// Verify all initiators are distinct and non-empty.
	initiators := []Initiator{InitiatorLLM, InitiatorUser, InitiatorTest}
	seen := make(map[Initiator]bool)
	for _, init := range initiators {
		if init == "" {
			t.Error("initiator should not be empty")
		}
		if seen[init] {
			t.Errorf("duplicate initiator: %s", init)
		}
		seen[init] = true
	}
}
