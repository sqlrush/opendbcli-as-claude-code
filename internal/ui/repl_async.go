/*-------------------------------------------------------------------------
 *
 * repl_async.go
 *	  Async REPL render loop — receives streaming chunks from the
 *	  engine via a buffered diagCh channel and incrementally repaints
 *	  without blocking the user's input. Critical: 256-byte buffer is
 *	  the well-known truncation root-cause; do not lower.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_async.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/dispatch"
	"github.com/sqlrush/opendb/internal/odberr"
	"github.com/sqlrush/opendb/internal/skill"
)

// ── Async Diag ────────────────────────────────────────────────

// isNaturalLanguageWithLLM returns true if input is natural language and LLM is configured.
// This routes free-form questions to the diagnose skill for AI-powered analysis.
func (r *REPL) isNaturalLanguageWithLLM(input string) bool {
	if r.diagSkill == nil || !r.diagSkill.HasLLM() {
		return false
	}
	return dispatch.ClassifyInput(input) == dispatch.InputNaturalLanguage
}

// isDiagWithLLM returns true if input is a /llm command that would use LLM.
func (r *REPL) isDiagWithLLM(input string) bool {
	if r.diagSkill == nil || !r.diagSkill.HasLLM() {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(input))
	return strings.HasPrefix(lower, "/llm")
}

// resolveDiagSkill returns the DiagnoseSkill for the currently active database.
// It looks up the "llm" skill from the registry (which filters by activeDB),
// then falls back to r.diagSkill if the registry lookup fails.
func (r *REPL) resolveDiagSkill() diagAsyncSource {
	if s, ok := r.registry.Get("llm"); ok {
		if ds, ok := s.(diagAsyncSource); ok {
			return ds
		}
	}
	return r.diagSkill
}

// startDiagAsync launches /diag in a background goroutine with progress streaming.
func (r *REPL) startDiagAsync(input string) {
	r.diagRunning = true
	ch := make(chan DiagProgressEvent, 256)
	r.diagCh = ch

	// Resolve the DiagnoseSkill for the currently active database.
	// r.diagSkill may hold the wrong product's instance (last registered),
	// but the registry returns the correct one based on activeDB.
	activeDiag := r.resolveDiagSkill()

	// Wire progress callback → channel.
	activeDiag.SetOnProgress(func(phase, message string, elapsed time.Duration, result *skill.Result, err error) {
		var p DiagPhase
		switch phase {
		case "start":
			p = DiagPhaseStart
		case "rule":
			p = DiagPhaseRule
		case "ai_start":
			p = DiagPhaseAIStart
		case "ai_round":
			p = DiagPhaseAIRound
		case "ai_streaming":
			p = DiagPhaseAIStream
		case "done":
			p = DiagPhaseDone
		case "error":
			p = DiagPhaseError
		default:
			return
		}
		ev := DiagProgressEvent{Phase: p, Message: message, Elapsed: elapsed, Result: result, Err: err}
		ch <- ev
	})

	ctx, cancel := context.WithCancel(context.Background())
	r.diagCancel = cancel

	odberr.SafeGo(odberr.ErrDiagToolCall, func() {
		defer cancel()
		result, err := r.dispatcher.Dispatch(ctx, input)
		activeDiag.ClearOnProgress()

		// Done/Error MUST be delivered — use blocking send.
		// Streaming events may fill the 256-buffer channel; select{default:}
		// would drop Done → diagRunning stays true forever → all commands blocked.
		// Blocking send waits until REPL consumes enough events, then delivers.
		if err != nil {
			errMsg := err.Error()
			if ctx.Err() == context.Canceled {
				errMsg = "诊断已取消 (Ctrl+C)"
			}
			ch <- DiagProgressEvent{Phase: DiagPhaseError, Err: fmt.Errorf("%s", errMsg)}
		} else {
			ch <- DiagProgressEvent{Phase: DiagPhaseDone, Result: result}
		}
	})

	// DiagPhaseStart will write the initial blank line — don't add one here.
	r.drawInputArea()
}

// processCmdQueue executes the next queued command.
// Processes one command at a time: if it goes async, remaining queue stays
// and will be processed when that command completes.
func (r *REPL) processCmdQueue() {
	if len(r.cmdQueue) == 0 {
		return
	}

	// Pop one command.
	cmd := r.cmdQueue[0]
	r.cmdQueue = r.cmdQueue[1:]

	r.writeOutputLine("")
	r.writeOutputLine(dimStyle.Render("  ▸ 执行排队命令"))

	// Bare picker commands need interactive selection — route through handleEnter
	// so the picker opens now that async is done.
	if isBarePickerCommand(cmd) {
		r.handleEnter(cmd)
		if r.diagRunning || r.skillRunning {
			return // async started from picker selection, queue resumes on completion
		}
		r.processCmdQueue()
		return
	}

	// Route through the normal handleEnter logic (which will go async or sync).
	// But since diagRunning/skillRunning is already false (we're called from their
	// completion handlers), the command won't re-queue — it will execute.
	if r.isDiagWithLLM(cmd) {
		r.writeOutputLine(r.buildPrompt() + cmd)
		r.startDiagAsync(cmd)
		return
	}
	// Natural language → route to /llm async if LLM is configured.
	// Bug fix: previously natural language queued commands fell through to
	// startSkillAsync (2-min hardcoded timeout), causing /sqltune-like long
	// operations to fail with misleading "请检查数据库连接状态". Mirror the
	// non-queued path from repl_input.go.
	if r.isNaturalLanguageWithLLM(cmd) {
		r.writeOutputLine(r.buildPrompt() + cmd)
		r.startDiagAsync("/llm " + cmd)
		return
	}
	if !isSyncCommand(cmd) {
		r.writeOutputLine(r.buildPrompt() + cmd)
		r.startSkillAsync(cmd)
		return
	}
	// Sync command — execute immediately, then continue queue.
	r.handleEnterSync(cmd)
	r.writeOutputLine("")
	r.drawInputArea()
	r.processCmdQueue()
}

// handleEnterSync is the synchronous dispatch path (no async diag detection).
func (r *REPL) handleEnterSync(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}

	prompt := r.buildPrompt()
	r.writeOutputLine(prompt + input)

	// Check for \G vertical display modifier.
	vertical := false
	dispatchInput := input
	if strings.HasSuffix(strings.TrimSpace(input), `\G`) {
		vertical = true
		dispatchInput = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(input), `\G`))
	}

	ctx := context.Background()
	result, err := r.dispatcher.Dispatch(ctx, dispatchInput)
	if err != nil {
		r.writeOutputLine("")
		r.writeOutputLine(errorStyle.Render("  ✗ " + err.Error()))
		return
	}
	if result != nil {
		r.renderResult(result, input, vertical)
		r.updateConnInfoFromResult(result, input)
	}
	r.writeOutputLine("")
}

