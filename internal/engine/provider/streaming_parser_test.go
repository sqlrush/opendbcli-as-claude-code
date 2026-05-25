/*-------------------------------------------------------------------------
 *
 * streaming_parser_test.go
 *	  Tests for StreamingParser mode detection + chunk routing.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/streaming_parser_test.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"strings"
	"testing"
)

func TestStreamingParser_DetectsFormatAOnFenceOpen(t *testing.T) {
	p := NewStreamingParser([]string{"health"}, 64)
	out, mode := p.Feed("```")
	if mode != StreamModeFormatA {
		t.Errorf("got mode %s, want format_a", mode)
	}
	if out != "" {
		t.Errorf("Format A should buffer, not emit text yet; got %q", out)
	}
}

func TestStreamingParser_DetectsFormatAOnRawBrace(t *testing.T) {
	p := NewStreamingParser([]string{"health"}, 64)
	out, mode := p.Feed("{")
	if mode != StreamModeFormatA {
		t.Errorf("got mode %s, want format_a", mode)
	}
	if out != "" {
		t.Errorf("Format A should buffer, got %q", out)
	}
}

func TestStreamingParser_DetectsFormatBOnMarkdown(t *testing.T) {
	p := NewStreamingParser(nil, 64)
	out, mode := p.Feed("## 根因分析\n")
	if mode != StreamModeFormatB {
		t.Errorf("got mode %s, want format_b", mode)
	}
	if !strings.Contains(out, "根因分析") {
		t.Errorf("Format B should pass through, got %q", out)
	}
}

func TestStreamingParser_DetectsFormatBOnChinesePrefix(t *testing.T) {
	p := NewStreamingParser(nil, 64)
	out, mode := p.Feed("根据现象分析,")
	if mode != StreamModeFormatB {
		t.Errorf("got mode %s, want format_b on Chinese prose", mode)
	}
	if !strings.Contains(out, "根据现象") {
		t.Errorf("Chinese prose should pass through, got %q", out)
	}
}

func TestStreamingParser_FormatA_FullFlow(t *testing.T) {
	p := NewStreamingParser([]string{"health", "topsql"}, 64)
	// Stream in 3 chunks.
	chunks := []string{
		"```json\n",
		`{"tool_calls":[{"name":"health",`,
		`"args":{}}]}` + "\n```",
	}
	for _, c := range chunks {
		out, _ := p.Feed(c)
		if out != "" {
			t.Errorf("Format A should never emit text mid-stream, got %q", out)
		}
	}
	calls, text, mode, err := p.Finish()
	if err != nil {
		t.Fatalf("finish err: %v", err)
	}
	if mode != StreamModeFormatA {
		t.Errorf("final mode: got %s, want format_a", mode)
	}
	if len(calls) != 1 || calls[0].Name != "health" {
		t.Errorf("expected 1 call to health, got %+v", calls)
	}
	if text != "" {
		t.Errorf("Format A should not produce text on success, got %q", text)
	}
}

func TestStreamingParser_FormatB_RealtimeStream(t *testing.T) {
	p := NewStreamingParser(nil, 64)
	// First chunk triggers mode detection AND should flush.
	out1, _ := p.Feed("## 分析\n")
	if !strings.Contains(out1, "## 分析") {
		t.Errorf("first chunk should flush in Format B, got %q", out1)
	}
	// Subsequent chunks should pass through directly.
	out2, mode := p.Feed("详情如下...")
	if mode != StreamModeFormatB {
		t.Errorf("stable mode: got %s, want format_b", mode)
	}
	if out2 != "详情如下..." {
		t.Errorf("Format B should pass through verbatim, got %q", out2)
	}
}

func TestStreamingParser_UnknownPromotedToFormatBOnFinish(t *testing.T) {
	p := NewStreamingParser(nil, 64)
	// Tiny prefix — not enough to decide.
	p.Feed(" ")
	if mode := p.Mode(); mode != StreamModeUnknown {
		t.Errorf("whitespace-only should leave mode Unknown, got %s", mode)
	}
	_, text, mode, _ := p.Finish()
	if mode != StreamModeFormatB {
		t.Errorf("Unknown should promote to Format B on Finish, got %s", mode)
	}
	if text != " " {
		t.Errorf("buffered content should flush on Finish, got %q", text)
	}
}

func TestStreamingParser_FormatA_ParseFailureReturnsFallback(t *testing.T) {
	p := NewStreamingParser(nil, 64)
	// Looks like Format A (fence open) but the body is unparseable.
	p.Feed("```json\n{broken without closing brace")
	calls, text, mode, err := p.Finish()
	if mode != StreamModeFormatA {
		t.Errorf("mode: got %s, want format_a", mode)
	}
	if len(calls) != 0 {
		t.Errorf("unparseable input should yield no calls, got %+v", calls)
	}
	if err == nil {
		// May or may not surface — but text should be returned as fallback.
		t.Logf("note: no ParseError, expected possibly (acceptable)")
	}
	if !strings.Contains(text, "broken") {
		t.Errorf("fallback text should preserve original buffer, got %q", text)
	}
}

func TestStreamingParser_DetectionThresholdCommitsToFormatB(t *testing.T) {
	// Threshold is small (10) for this test. After 10 bytes of non-JSON
	// non-Markdown content, parser should commit to Format B.
	p := NewStreamingParser(nil, 10)
	// 11 chars of plain English (no markdown markers, not a Chinese char).
	out, mode := p.Feed("abcdefghijk")
	if mode != StreamModeFormatB {
		t.Errorf("after threshold, mode should commit to format_b, got %s", mode)
	}
	if out != "abcdefghijk" {
		t.Errorf("committed content should flush, got %q", out)
	}
}

func TestStreamingParser_Reset(t *testing.T) {
	p := NewStreamingParser([]string{"health"}, 64)
	p.Feed("```")
	if mode := p.Mode(); mode == StreamModeUnknown {
		t.Fatal("expected mode set before reset")
	}
	p.Reset()
	if mode := p.Mode(); mode != StreamModeUnknown {
		t.Errorf("after Reset, mode should be Unknown; got %s", mode)
	}
}

func TestDetectModeFromPrefix(t *testing.T) {
	cases := map[string]StreamMode{
		"```json\n":         StreamModeFormatA,
		"```":               StreamModeFormatA,
		`{"tool_calls":`:    StreamModeFormatA,
		"## 根因":            StreamModeFormatB,
		"# header":          StreamModeFormatB,
		"> blockquote":      StreamModeFormatB,
		"- bullet":          StreamModeFormatB,
		"* bullet":          StreamModeFormatB,
		"根据现象":             StreamModeFormatB,
		"":                  StreamModeUnknown,
		" ":                 StreamModeUnknown,
		"some plain text":   StreamModeUnknown,
	}
	for in, want := range cases {
		got := detectModeFromPrefix(in)
		if got != want {
			t.Errorf("detectModeFromPrefix(%q): got %s, want %s", in, got, want)
		}
	}
}

func TestStreamingParser_StringMode(t *testing.T) {
	if StreamModeUnknown.String() != "unknown" {
		t.Errorf("unknown.String mismatch")
	}
	if StreamModeFormatA.String() != "format_a_tool_call" {
		t.Errorf("format_a.String mismatch")
	}
	if StreamModeFormatB.String() != "format_b_answer" {
		t.Errorf("format_b.String mismatch")
	}
}
