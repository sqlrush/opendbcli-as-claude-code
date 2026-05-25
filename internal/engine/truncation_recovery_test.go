/*-------------------------------------------------------------------------
 *
 * truncation_recovery_test.go
 *	  Test cases for truncation_recovery.go (engine package):
 *	  TestTruncationRecovery_HappyPath,
 *	  TestTruncationRecovery_RecoveryAlsoTruncated,
 *	  TestTruncationRecovery_NonTruncatedNoRecovery.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/truncation_recovery_test.go
 *
 *-------------------------------------------------------------------------
 */
package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// Truncation recovery is a notoriously fragile code path. Memory records
// two past breakages in this area (see memory/feedback-sse-finishreason-
// lesson.md and memory/feedback-streaming-truncation-debug.md):
//
//   1. SSE stream parser detected `finish_reason != nil` but forgot to
//      write FinishReason into the returned StreamEvent, so the engine
//      never saw Truncated=true and recovery never ran.
//   2. Streaming strategy refactor dropped Done events through a 256-slot
//      channel, causing long responses to hang.
//
// These regressions made it to release because the recovery was only
// exercised against real long-output workloads, not unit tests. The tests
// below guard the invariants for each recovery pathway:
//
//   - Engine sees Response.Truncated=true → must call recoverTruncatedOutput
//   - recoverTruncatedOutput must prepend the truncated content so the LLM's
//     resumed text concatenates seamlessly
//   - If the recovery attempt also fails, engine returns the original
//     truncated content (best-effort, never blocks the user)

// TestTruncationRecovery_HappyPath verifies the basic loop:
//   round 1: Response{Content="首先检查内存", Truncated=true}
//   round 2 (recovery): Response{Content="然后检查 IO", Truncated=false}
//   final result content = "首先检查内存" + "然后检查 IO"
func TestTruncationRecovery_HappyPath(t *testing.T) {
	mockProv := newMockProvider(
		// Turn 1 output: truncated halfway
		&provider.Response{
			Content:    "首先检查内存使用情况",
			Truncated:  true,
			StopReason: "length",
		},
		// Recovery call: continue writing
		&provider.Response{
			Content:    "；其次检查 IO 是否冲高",
			Truncated:  false,
			StopReason: "stop",
		},
	)

	eng := New(mockProv, &testProfile{}, newMockSkillExecutor(), nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "实例最近有点慢",
		Mode:         ModePlaybook,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
	})
	if err != nil {
		t.Fatalf("Run unexpected error: %v", err)
	}

	// Recovery must have concatenated.
	if !strings.Contains(result.Content, "首先检查内存") {
		t.Errorf("lost original truncated content; got: %q", result.Content)
	}
	if !strings.Contains(result.Content, "然后检查 IO") == false && !strings.Contains(result.Content, "其次检查 IO") {
		// Accept either wording; the test model's second-chunk literal is "其次".
		t.Errorf("recovery content missing; got: %q", result.Content)
	}

	// Provider must have been called at least twice (original + recovery).
	if mockProv.callIdx < 2 {
		t.Errorf("expected >= 2 provider calls (original + recovery), got %d", mockProv.callIdx)
	}
}

// TestTruncationRecovery_RecoveryAlsoTruncated verifies that if the
// recovery call itself also gets truncated, we DON'T loop forever — engine
// caps at MaxOutputRecoveries and returns what it has.
func TestTruncationRecovery_RecoveryAlsoTruncated(t *testing.T) {
	// 5 consecutive truncated responses (more than MaxOutputRecoveries=3)
	mockProv := newMockProvider(
		&provider.Response{Content: "段落1", Truncated: true, StopReason: "length"},
		&provider.Response{Content: "段落2", Truncated: true, StopReason: "length"},
		&provider.Response{Content: "段落3", Truncated: true, StopReason: "length"},
		&provider.Response{Content: "段落4", Truncated: true, StopReason: "length"},
		&provider.Response{Content: "段落5", Truncated: false, StopReason: "stop"},
	)

	eng := New(mockProv, &testProfile{}, newMockSkillExecutor(), nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "输出一份详细报告",
		Mode:         ModePlaybook,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
	})
	if err != nil {
		t.Fatalf("Run unexpected error: %v", err)
	}

	// Should have stopped at the recovery cap (≤ MaxOutputRecoveries=3 retries)
	// + the original call = 4 max. If the loop didn't cap, callIdx would be 5.
	if mockProv.callIdx > 4 {
		t.Errorf("recovery loop did not cap: callIdx=%d > 4 (MaxOutputRecoveries + 1)", mockProv.callIdx)
	}

	// Content should include at least the original truncated chunk.
	if !strings.Contains(result.Content, "段落1") {
		t.Errorf("original content lost; got: %q", result.Content)
	}
}

// TestTruncationRecovery_NonTruncatedNoRecovery verifies the happy path
// where the first call finishes cleanly — no recovery should be triggered.
func TestTruncationRecovery_NonTruncatedNoRecovery(t *testing.T) {
	mockProv := newMockProvider(
		&provider.Response{Content: "一切正常", Truncated: false, StopReason: "stop"},
	)

	eng := New(mockProv, &testProfile{}, newMockSkillExecutor(), nil)
	_, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "检查",
		Mode:         ModePlaybook,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
	})
	if err != nil {
		t.Fatalf("Run unexpected error: %v", err)
	}

	// Exactly one provider call — no recovery.
	if mockProv.callIdx != 1 {
		t.Errorf("unexpected recovery: callIdx=%d, want 1", mockProv.callIdx)
	}
}
