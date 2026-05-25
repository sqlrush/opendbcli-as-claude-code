/*-------------------------------------------------------------------------
 *
 * streaming_parser.go
 *	  v1.2.1 Streaming parser for PromptToolAdapter. Detects whether the
 *	  LLM is about to emit Format A (tool call JSON) or Format B (final
 *	  markdown answer) from the first chunk(s) and routes downstream
 *	  events accordingly:
 *
 *	    Format A (JSON):  buffer all chunks until brace-balanced, parse
 *	                      into ToolCall list, deliver via OnToolCalls
 *	    Format B (Text):  pass chunks through to OnText as they arrive
 *	                      (real-time streaming preserved)
 *
 *	  Detection happens on the first non-whitespace content. Heuristic:
 *	    - Starts with ` ``` ` (fence open)        → Format A
 *	    - Starts with `{`                          → Format A
 *	    - First word looks like a tool name (in
 *	      known-tools list)                        → Format A
 *	    - Otherwise                                → Format B
 *
 *	  Once detected, mode is sticky for the rest of the stream — we don't
 *	  switch mid-stream because that thrashes the UI.
 *
 *	  Note: this parser is invoked at the StreamingAdapter layer (wraps the
 *	  Provider.ChatStream call). LegacyProviderWrapper.ChatStream wires it
 *	  in when the underlying builder is PromptModeBuilder.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/streaming_parser.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"strings"
	"sync"
)

// StreamMode identifies which format the LLM is producing this turn.
type StreamMode uint8

const (
	// StreamModeUnknown is the initial state before any content arrives.
	StreamModeUnknown StreamMode = iota
	// StreamModeFormatA means the LLM is emitting a tool_call JSON block;
	// chunks are buffered until the JSON is complete.
	StreamModeFormatA
	// StreamModeFormatB means the LLM is emitting a plain markdown answer;
	// chunks pass through to OnText as they arrive.
	StreamModeFormatB
)

// String returns a human-readable mode name for logs.
func (m StreamMode) String() string {
	switch m {
	case StreamModeFormatA:
		return "format_a_tool_call"
	case StreamModeFormatB:
		return "format_b_answer"
	default:
		return "unknown"
	}
}

// StreamingParser decides between Format A buffering and Format B
// passthrough based on the first chunk(s). Safe for single-goroutine
// streaming use (one parser per Provider.ChatStream call).
type StreamingParser struct {
	mu sync.Mutex

	parser *JSONToolCallParser // for final JSON parse when mode=A

	// detectionWindow accumulates content until we can decide the mode.
	// Capped at detectThreshold bytes — past that we commit to Format B
	// (no JSON started yet → must be text).
	detectionWindow strings.Builder
	// buf accumulates ALL content for Format A; in Format B mode it's
	// never populated (text streams directly through callbacks).
	buf strings.Builder

	mode StreamMode

	// Detection threshold. If we've buffered this many bytes and still
	// see no JSON-shaped start, commit to Format B.
	detectThreshold int
}

// NewStreamingParser builds a parser configured with the same known-tool
// list as the non-streaming JSONToolCallParser. detectThreshold ≤ 0
// defaults to 64 bytes.
func NewStreamingParser(knownToolNames []string, detectThreshold int) *StreamingParser {
	if detectThreshold <= 0 {
		detectThreshold = 64
	}
	return &StreamingParser{
		parser:          NewJSONToolCallParser(knownToolNames),
		detectThreshold: detectThreshold,
	}
}

// Mode returns the currently-detected stream mode. Stable once non-Unknown.
func (p *StreamingParser) Mode() StreamMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mode
}

// Feed accepts an incoming content delta. Returns:
//
//	textForUser: bytes safe to pipe to OnStream callback right now (i.e.
//	             everything that's been confirmed as Format B). Empty in
//	             Format A mode and during pre-detection.
//	mode:        current detected mode after this chunk.
//
// Caller is responsible for invoking OnStream(textForUser) when non-empty.
func (p *StreamingParser) Feed(delta string) (textForUser string, mode StreamMode) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if delta == "" {
		return "", p.mode
	}

	switch p.mode {
	case StreamModeUnknown:
		// Accumulate into detection window.
		p.detectionWindow.WriteString(delta)
		detected := detectModeFromPrefix(p.detectionWindow.String())
		if detected == StreamModeUnknown && p.detectionWindow.Len() < p.detectThreshold {
			// Still undecided; hold the bytes.
			return "", StreamModeUnknown
		}
		if detected == StreamModeUnknown {
			// Buffered enough non-JSON content → commit to Format B and
			// flush everything we've held.
			detected = StreamModeFormatB
		}
		p.mode = detected

		if detected == StreamModeFormatA {
			// Move accumulated bytes into the JSON buffer.
			p.buf.WriteString(p.detectionWindow.String())
			p.detectionWindow.Reset()
			return "", StreamModeFormatA
		}
		// Format B: flush accumulated content to caller.
		flushed := p.detectionWindow.String()
		p.detectionWindow.Reset()
		return flushed, StreamModeFormatB

	case StreamModeFormatA:
		// JSON mode → keep buffering.
		p.buf.WriteString(delta)
		return "", StreamModeFormatA

	case StreamModeFormatB:
		// Text mode → pass through directly.
		return delta, StreamModeFormatB
	}
	return "", p.mode
}

// Finish completes the stream. For Format A it parses the accumulated
// buffer into ToolCalls; for Format B it returns any remaining buffered
// text (typically empty since chunks already flushed). For Unknown it
// commits to Format B and flushes everything held in the detection window.
//
// Returns:
//
//	calls:        parsed tool calls if Format A succeeded
//	text:         any final text to deliver (Format B leftovers or
//	              Format A's fallback content if JSON failed)
//	mode:         final committed mode
//	parseErr:     non-nil if Format A buffer wouldn't parse
func (p *StreamingParser) Finish() (calls []ToolCall, text string, mode StreamMode, parseErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Promote Unknown → Format B (whatever we held is plain text).
	if p.mode == StreamModeUnknown {
		text = p.detectionWindow.String()
		p.detectionWindow.Reset()
		p.mode = StreamModeFormatB
		return nil, text, p.mode, nil
	}

	if p.mode == StreamModeFormatB {
		// No leftover; chunks streamed directly. Caller already saw them.
		return nil, "", p.mode, nil
	}

	// Format A: parse the buffered JSON.
	full := p.buf.String()
	result := p.parser.Parse(full)
	if len(result.Calls) > 0 {
		return result.Calls, "", p.mode, nil
	}
	// Parse failed → return the raw buffer as text fallback (engine will
	// treat it as Format B). ParseError preserved for caller logging.
	return nil, full, p.mode, result.ParseError
}

// Reset clears parser state for re-use. Tests call this between scenarios.
func (p *StreamingParser) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.detectionWindow.Reset()
	p.buf.Reset()
	p.mode = StreamModeUnknown
}

// detectModeFromPrefix returns Format A if the prefix shape unambiguously
// indicates a JSON tool call, Format B if it looks like Markdown / prose,
// or Unknown if we need more bytes to decide.
//
// Heuristics (case-insensitive on keywords):
//   - leading whitespace ignored
//   - starts with ``` → Format A (code fence; assume JSON inside)
//   - starts with { → Format A
//   - starts with markdown heading (#) → Format B
//   - starts with > or - or 0-9. → Format B (list/blockquote)
//   - contains "tool_calls" within first 50 chars → Format A
//   - long enough non-JSON prefix → caller should commit Format B
func detectModeFromPrefix(s string) StreamMode {
	trimmed := strings.TrimLeft(s, " \t\n\r")
	if trimmed == "" {
		return StreamModeUnknown
	}
	// Strong Format A signals.
	if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "{") {
		return StreamModeFormatA
	}
	// Strong Format B signals (markdown structures).
	if strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, ">") ||
		strings.HasPrefix(trimmed, "- ") ||
		strings.HasPrefix(trimmed, "* ") {
		return StreamModeFormatB
	}
	// Looks-like-prose detector: lowercase or Chinese chars early.
	first := []rune(trimmed)[0]
	if first >= 0x4E00 && first <= 0x9FFF {
		// Chinese character at start → almost certainly prose answer.
		return StreamModeFormatB
	}
	// Mid-stream signal: "tool_calls" keyword appearing.
	if strings.Contains(trimmed, "tool_calls") || strings.Contains(trimmed, "\"name\"") {
		return StreamModeFormatA
	}
	return StreamModeUnknown // still undecided
}
