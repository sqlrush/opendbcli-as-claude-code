/*-------------------------------------------------------------------------
 *
 * llm_hook.go
 *	  llm_hook.go provides Engine integration hooks for LLM recording
 *	  and replay. These hooks intercept LLM calls in the Engine V2
 *	  pipeline for: - Record mode: capture real LLM interactions to
 *	  files - Replay mode: feed recorded responses instead of calling
 *	  real LLM - Budget mode: check LLM budget before allowing a call
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/llm_hook.go
 *
 *-------------------------------------------------------------------------
 */
// llm_hook.go provides Engine integration hooks for LLM recording and replay.
// These hooks intercept LLM calls in the Engine V2 pipeline for:
// - Record mode: capture real LLM interactions to files
// - Replay mode: feed recorded responses instead of calling real LLM
// - Budget mode: check LLM budget before allowing a call
package drone

import (
	"fmt"
	"time"
)

// LLMHookMode defines the LLM interception mode.
type LLMHookMode string

const (
	LLMHookNone   LLMHookMode = ""        // normal operation
	LLMHookRecord LLMHookMode = "record"  // record real calls
	LLMHookReplay LLMHookMode = "replay"  // replay recorded calls
)

// LLMHooks bundles all LLM interception hooks for Engine integration.
type LLMHooks struct {
	Mode     LLMHookMode
	Recorder *LLMRecorder
	Replayer *LLMReplayer
	Budget   *LLMBudget
}

// NewLLMHooks creates hooks based on the specified mode.
func NewLLMHooks(mode LLMHookMode, recordDir, replayFile string) (*LLMHooks, error) {
	hooks := &LLMHooks{
		Mode:   mode,
		Budget: NewLLMBudget(),
	}

	switch mode {
	case LLMHookRecord:
		recorder, err := NewLLMRecorder(recordDir)
		if err != nil {
			return nil, fmt.Errorf("create recorder: %w", err)
		}
		hooks.Recorder = recorder

	case LLMHookReplay:
		replayer, err := NewLLMReplayer(replayFile)
		if err != nil {
			return nil, fmt.Errorf("create replayer: %w", err)
		}
		hooks.Replayer = replayer
	}

	return hooks, nil
}

// PreCall is called before an LLM API call.
// Returns (proceed, replayResponse, error).
// - proceed=true: go ahead with real LLM call
// - proceed=false + replayResponse: use this response instead of calling LLM
func (h *LLMHooks) PreCall(metric string) (proceed bool, replay *LLMRecord, err error) {
	// Budget check.
	if h.Budget != nil {
		allowed, reason := h.Budget.CanCallLLM(metric)
		if !allowed {
			return false, nil, fmt.Errorf("LLM budget: %s", reason)
		}
	}

	// Replay mode: return recorded response.
	if h.Mode == LLMHookReplay && h.Replayer != nil {
		rec, err := h.Replayer.Next()
		if err != nil {
			return false, nil, fmt.Errorf("replay: %w", err)
		}
		return false, &rec, nil
	}

	return true, nil, nil
}

// PostCall is called after an LLM API call completes.
// Records the interaction if in record mode, updates budget.
func (h *LLMHooks) PostCall(metric string, req LLMReq, resp LLMResp, latency time.Duration, success bool) {
	// Budget tracking.
	if h.Budget != nil {
		h.Budget.RecordCall(metric)
		if success {
			h.Budget.RecordSuccess(metric)
		} else {
			h.Budget.RecordFailure(metric)
		}
	}

	// Record mode: save to file.
	if h.Mode == LLMHookRecord && h.Recorder != nil {
		h.Recorder.Record(LLMRecord{
			Timestamp: time.Now(),
			Request:   req,
			Response:  resp,
			LatencyMs: latency.Milliseconds(),
		})
	}
}

// Close cleans up resources.
func (h *LLMHooks) Close() {
	if h.Recorder != nil {
		h.Recorder.Close()
	}
}

// Stats returns budget statistics.
func (h *LLMHooks) Stats() *BudgetStats {
	if h.Budget != nil {
		stats := h.Budget.Stats()
		return &stats
	}
	return nil
}
