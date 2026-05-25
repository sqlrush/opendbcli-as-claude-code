/*-------------------------------------------------------------------------
 *
 * exec_mode.go
 *	  exec_mode.go defines execution mode types and the ActionHandler
 *	  interface. Controls how LLM-generated SQL is handled: report-only,
 *	  confirm, or auto-execute. Key principle: restrictions apply to
 *	  LLM, not to users.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/exec_mode.go
 *
 *-------------------------------------------------------------------------
 */
// exec_mode.go defines execution mode types and the ActionHandler interface.
// Controls how LLM-generated SQL is handled: report-only, confirm, or auto-execute.
// Key principle: restrictions apply to LLM, not to users.
package cluster

import (
	"context"
	"fmt"
	"strings"
)

// ExecMode controls how LLM-generated SQL is handled.
type ExecMode string

const (
	ExecModeReportOnly  ExecMode = "report_only"  // V1: SQL goes to report only
	ExecModeConfirmExec ExecMode = "confirm_exec"  // V2 planned: user confirms each SQL
	ExecModeAutoExec    ExecMode = "auto_exec"     // V3 planned: auto-execute with safety
)

// ValidExecModes lists all recognized exec modes.
var ValidExecModes = []ExecMode{
	ExecModeReportOnly,
	ExecModeConfirmExec,
	ExecModeAutoExec,
}

// ParseExecMode converts a string to ExecMode, returning an error for unknown values.
func ParseExecMode(s string) (ExecMode, error) {
	mode := ExecMode(strings.TrimSpace(s))
	for _, v := range ValidExecModes {
		if mode == v {
			return mode, nil
		}
	}
	return "", fmt.Errorf("unknown exec_mode %q: valid values are report_only, confirm_exec, auto_exec", s)
}

// RiskLevel classifies how dangerous an operation is.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// NeedsConfirmation returns true if the risk level requires user confirmation.
func (r RiskLevel) NeedsConfirmation() bool {
	return r == RiskHigh || r == RiskCritical
}

// ActionCategory classifies the type of database operation.
type ActionCategory string

const (
	CategoryReadOnly    ActionCategory = "read_only"
	CategoryKillSession ActionCategory = "kill_session"
	CategoryDDL         ActionCategory = "ddl"
	CategoryDML         ActionCategory = "dml"
	CategoryParameter   ActionCategory = "parameter"
	CategoryFailover    ActionCategory = "failover"
)

// Initiator identifies who requested the action.
type Initiator string

const (
	InitiatorLLM  Initiator = "llm"  // LLM autonomous diagnosis
	InitiatorUser Initiator = "user" // User via / command
	InitiatorTest Initiator = "test" // Test framework (/inject)
)

// SQLAction represents a generated SQL command with metadata.
// Immutable after creation — callers should construct a new SQLAction rather than modifying fields.
type SQLAction struct {
	SQL            string         // complete executable SQL
	Description    string         // what this does
	Risk           RiskLevel      // low/medium/high/critical
	ExpectedEffect string         // what should happen after execution
	Rollback       string         // how to undo if things go wrong
	VerifySQL      string         // SQL to verify the fix worked
	Category       ActionCategory // classifies the operation type
}

// NewSQLAction creates a new SQLAction with the given fields.
func NewSQLAction(sql, description string, risk RiskLevel, category ActionCategory) SQLAction {
	return SQLAction{
		SQL:         sql,
		Description: description,
		Risk:        risk,
		Category:    category,
	}
}

// WithExpectedEffect returns a copy of SQLAction with the expected effect set.
func (a SQLAction) WithExpectedEffect(effect string) SQLAction {
	a.ExpectedEffect = effect
	return a
}

// WithRollback returns a copy of SQLAction with the rollback plan set.
func (a SQLAction) WithRollback(rollback string) SQLAction {
	a.Rollback = rollback
	return a
}

// WithVerifySQL returns a copy of SQLAction with the verify SQL set.
func (a SQLAction) WithVerifySQL(verifySQL string) SQLAction {
	a.VerifySQL = verifySQL
	return a
}

// ActionResult holds the outcome of handling an action.
// Immutable after creation.
type ActionResult struct {
	Executed bool   // whether the SQL was actually executed
	Output   string // execution output or "added to report"
	Error    string // error message if failed
}

// ActionHandler processes SQL actions according to exec_mode and initiator.
type ActionHandler interface {
	Handle(ctx context.Context, initiator Initiator, action SQLAction) (*ActionResult, error)
}

// SQLExecutor is the interface for actually executing SQL against a database.
// Implementations are provided by the database-specific packages.
type SQLExecutor interface {
	ExecSQL(ctx context.Context, sql string) (string, error)
}
