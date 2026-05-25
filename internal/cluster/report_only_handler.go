/*-------------------------------------------------------------------------
 *
 * report_only_handler.go
 *	  report_only_handler.go implements the ReportOnlyHandler for
 *	  exec_mode=report_only. LLM actions go to report. User actions
 *	  execute (with confirmation for high risk). Test actions always
 *	  execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/report_only_handler.go
 *
 *-------------------------------------------------------------------------
 */
// report_only_handler.go implements the ReportOnlyHandler for exec_mode=report_only.
// LLM actions go to report. User actions execute (with confirmation for high risk).
// Test actions always execute.
package cluster

import (
	"context"
	"fmt"
)

// ReportOnlyHandler implements ActionHandler for ExecModeReportOnly.
// LLM-initiated actions are never executed — they go into the report.
// User-initiated actions may execute, with confirmation required for high/critical risk.
// Test-initiated actions always execute.
type ReportOnlyHandler struct {
	executor SQLExecutor
}

// NewReportOnlyHandler creates a ReportOnlyHandler with the given SQL executor.
func NewReportOnlyHandler(executor SQLExecutor) *ReportOnlyHandler {
	return &ReportOnlyHandler{executor: executor}
}

// Handle processes a SQL action according to report_only rules.
func (h *ReportOnlyHandler) Handle(ctx context.Context, initiator Initiator, action SQLAction) (*ActionResult, error) {
	switch initiator {
	case InitiatorLLM:
		return handleLLMAction(action), nil
	case InitiatorUser:
		return h.handleUserAction(ctx, action)
	case InitiatorTest:
		return h.handleTestAction(ctx, action)
	default:
		return nil, fmt.Errorf("unknown initiator %q", initiator)
	}
}

// handleLLMAction always returns report-only result — LLM never executes writes.
func handleLLMAction(action SQLAction) *ActionResult {
	return &ActionResult{
		Executed: false,
		Output:   fmt.Sprintf("added to report: %s", action.Description),
	}
}

// handleUserAction executes the action, but prompts for confirmation on high/critical risk.
func (h *ReportOnlyHandler) handleUserAction(ctx context.Context, action SQLAction) (*ActionResult, error) {
	if action.Risk.NeedsConfirmation() {
		return &ActionResult{
			Executed: false,
			Output:   confirmationMessage(action),
		}, nil
	}
	return h.executeAction(ctx, action)
}

// handleTestAction always executes — test framework needs to inject faults.
func (h *ReportOnlyHandler) handleTestAction(ctx context.Context, action SQLAction) (*ActionResult, error) {
	return h.executeAction(ctx, action)
}

// executeAction runs the SQL via the executor and returns the result.
func (h *ReportOnlyHandler) executeAction(ctx context.Context, action SQLAction) (*ActionResult, error) {
	output, err := h.executor.ExecSQL(ctx, action.SQL)
	if err != nil {
		return &ActionResult{
			Executed: false,
			Error:    fmt.Sprintf("execution failed: %v", err),
		}, nil
	}
	return &ActionResult{
		Executed: true,
		Output:   output,
	}, nil
}

// confirmationMessage builds a user-facing confirmation prompt for risky operations.
func confirmationMessage(action SQLAction) string {
	msg := fmt.Sprintf("⚠ %s risk operation requires confirmation:\n", action.Risk)
	msg += fmt.Sprintf("  SQL: %s\n", action.SQL)
	msg += fmt.Sprintf("  Description: %s\n", action.Description)
	if action.Rollback != "" {
		msg += fmt.Sprintf("  Rollback: %s\n", action.Rollback)
	}
	return msg
}
