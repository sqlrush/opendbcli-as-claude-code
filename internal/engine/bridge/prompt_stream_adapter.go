/*-------------------------------------------------------------------------
 *
 * prompt_stream_adapter.go
 *	  v1.2.1 stream adapter for PromptToolAdapter. Wraps a legacy provider
 *	  stream and routes incoming text deltas through StreamingParser:
 *
 *	    - Format A (tool_call JSON): buffer chunks silently until JSON
 *	      complete, then emit a synthetic StreamToolCallDelta event per
 *	      parsed ToolCall, followed by StreamDone.
 *	    - Format B (markdown answer): pass chunks through as
 *	      StreamTextDelta in real time. Caller sees natural streaming.
 *
 *	  Only activated when the active PromptBuilder is PromptModeBuilder
 *	  (legacywrapper.ChatStream checks Mode() == "prompt"). Native FC
 *	  streams skip this adapter entirely — zero latency overhead.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/bridge/prompt_stream_adapter.go
 *
 *-------------------------------------------------------------------------
 */
package bridge

import (
	"github.com/sqlrush/opendb/internal/engine/provider"
)

// promptStreamAdapter wraps a legacyStreamWrapper and routes text deltas
// through StreamingParser. Implements provider.Stream.
type promptStreamAdapter struct {
	inner  *legacyStreamWrapper
	parser *provider.StreamingParser

	// pendingToolCalls is the queue of synthetic StreamToolCallDelta events
	// emitted after Format A parsing succeeds. We deliver one per Next()
	// call so the engine's stream consumer sees them as discrete events
	// (matching native FC behavior).
	pendingToolCalls []provider.ToolCall
	// pendingText is fallback content from Format A parse failure that
	// needs to be delivered before the Done event.
	pendingText string
	// finalized is true after we've emitted everything from Finish().
	finalized bool
}

// newPromptStreamAdapter constructs the adapter with a fresh StreamingParser.
// knownToolNames enables Levenshtein correction during JSON parse.
func newPromptStreamAdapter(inner *legacyStreamWrapper, knownToolNames []string) *promptStreamAdapter {
	return &promptStreamAdapter{
		inner:  inner,
		parser: provider.NewStreamingParser(knownToolNames, 64),
	}
}

// Next reads the next event from the underlying stream, routes it through
// the parser, and returns the appropriate event for the engine.
//
// Flow:
//
//	1. If pendingToolCalls is non-empty, dequeue one and emit it.
//	2. Else if finalized and no pending text, emit StreamDone.
//	3. Else read from inner stream.
//	4. Text delta → parser.Feed(); emit user-facing text or buffer silently.
//	5. Inner stream returns StreamDone → call parser.Finish() and emit
//	   any synthetic tool_call events (queued) then StreamDone.
func (a *promptStreamAdapter) Next() (provider.StreamEvent, error) {
	// Drain queued synthetic events first.
	if len(a.pendingToolCalls) > 0 {
		tc := a.pendingToolCalls[0]
		a.pendingToolCalls = a.pendingToolCalls[1:]
		return provider.StreamEvent{
			Type:     provider.StreamToolCallDelta,
			ToolCall: &tc,
		}, nil
	}
	if a.pendingText != "" {
		text := a.pendingText
		a.pendingText = ""
		return provider.StreamEvent{Type: provider.StreamTextDelta, Content: text}, nil
	}
	if a.finalized {
		return provider.StreamEvent{Type: provider.StreamDone, FinishReason: "stop"}, nil
	}

	for {
		ev, err := a.inner.Next()
		if err != nil {
			return ev, err
		}
		switch ev.Type {
		case provider.StreamTextDelta:
			textForUser, _ := a.parser.Feed(ev.Content)
			if textForUser != "" {
				return provider.StreamEvent{Type: provider.StreamTextDelta, Content: textForUser}, nil
			}
			// Format A buffering — silently consume, keep reading.
			continue

		case provider.StreamThinkingDelta:
			// Pass through unchanged (thinking content is independent of FC).
			return ev, nil

		case provider.StreamToolCallDelta:
			// Hybrid backend slipped a native tool_call through despite
			// prompt mode — just forward it.
			return ev, nil

		case provider.StreamDone:
			// Finalize parser, emit pending events.
			calls, text, _, _ := a.parser.Finish()
			a.finalized = true
			if len(calls) > 0 {
				a.pendingToolCalls = calls
				// Emit the first immediately.
				tc := a.pendingToolCalls[0]
				a.pendingToolCalls = a.pendingToolCalls[1:]
				return provider.StreamEvent{
					Type:     provider.StreamToolCallDelta,
					ToolCall: &tc,
				}, nil
			}
			if text != "" {
				return provider.StreamEvent{Type: provider.StreamTextDelta, Content: text}, nil
			}
			return ev, nil // pass through the Done event with its finish reason

		default:
			return ev, nil
		}
	}
}

// Close releases the underlying stream.
func (a *promptStreamAdapter) Close() error {
	return a.inner.Close()
}
