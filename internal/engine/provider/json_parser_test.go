/*-------------------------------------------------------------------------
 *
 * json_parser_test.go
 *	  Edge-case tests for JSONToolCallParser. Each test name describes the
 *	  LLM output quirk being exercised.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/json_parser_test.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"strings"
	"testing"
)

var defaultKnownTools = []string{"health", "alert", "topsql", "waits", "sqltune", "wdranalyze", "sqlfetch", "explain"}

func TestParse_PerfectJSONFence(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := "```json\n" +
		`{"tool_calls": [{"name": "health", "args": {}}]}` +
		"\n```"
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	if r.Calls[0].Name != "health" {
		t.Errorf("name: got %q, want health", r.Calls[0].Name)
	}
	if r.FallbackContent != "" {
		t.Errorf("unexpected fallback: %q", r.FallbackContent)
	}
}

func TestParse_PlainCodeFence(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := "```\n" + `{"tool_calls":[{"name":"topsql","args":{"args":"60s"}}]}` + "\n```"
	r := p.Parse(in)
	if len(r.Calls) != 1 || r.Calls[0].Name != "topsql" {
		t.Errorf("plain fence parse failed: %+v", r)
	}
}

func TestParse_RawJSONNoFence(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{"tool_calls": [{"name": "alert", "args": {}}]}`
	r := p.Parse(in)
	if len(r.Calls) != 1 || r.Calls[0].Name != "alert" {
		t.Errorf("raw json parse failed: %+v", r)
	}
}

func TestParse_PrefixedWithExplanation(t *testing.T) {
	// LLM stubbornly adds "I'll call..." before the JSON.
	p := NewJSONToolCallParser(defaultKnownTools)
	in := "我先调用 health 工具：\n```json\n" +
		`{"tool_calls":[{"name":"health","args":{}}]}` +
		"\n```"
	r := p.Parse(in)
	if len(r.Calls) != 1 || r.Calls[0].Name != "health" {
		t.Errorf("prefixed parse failed: %+v", r)
	}
}

func TestParse_TrailingComma(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{"tool_calls": [{"name": "health", "args": {},},]}`
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Errorf("trailing comma not fixed: %+v", r)
	}
}

func TestParse_SingleQuotes(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{'tool_calls': [{'name': 'health', 'args': {}}]}`
	r := p.Parse(in)
	if len(r.Calls) != 1 || r.Calls[0].Name != "health" {
		t.Errorf("single quotes not fixed: %+v", r)
	}
}

func TestParse_BlockComment(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{"tool_calls": [/* call topsql for top SQL */ {"name": "topsql", "args": {}}]}`
	r := p.Parse(in)
	if len(r.Calls) != 1 || r.Calls[0].Name != "topsql" {
		t.Errorf("block comment not stripped: %+v", r)
	}
}

func TestParse_LineComment(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := "{\"tool_calls\": [\n  // analyze recent waits\n  {\"name\": \"waits\", \"args\": {}}\n]}"
	r := p.Parse(in)
	if len(r.Calls) != 1 || r.Calls[0].Name != "waits" {
		t.Errorf("line comment not stripped: %+v", r)
	}
}

func TestParse_MultipleToolCalls(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{"tool_calls": [
		{"name": "health", "args": {}},
		{"name": "alert", "args": {}},
		{"name": "topsql", "args": {"args": "60s"}}
	]}`
	r := p.Parse(in)
	if len(r.Calls) != 3 {
		t.Fatalf("want 3 calls, got %d", len(r.Calls))
	}
	names := []string{r.Calls[0].Name, r.Calls[1].Name, r.Calls[2].Name}
	expect := []string{"health", "alert", "topsql"}
	for i := range names {
		if names[i] != expect[i] {
			t.Errorf("calls[%d].Name: got %q, want %q", i, names[i], expect[i])
		}
	}
}

func TestParse_LevenshteinCorrection(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	cases := map[string]string{
		"heath":  "health", // missing 'l'
		"healtg": "health", // 'g' instead of 'h'
		"topsq1": "topsql", // '1' instead of 'l'
		"HEALTH": "health", // case mismatch
	}
	for typo, want := range cases {
		in := `{"tool_calls":[{"name":"` + typo + `","args":{}}]}`
		r := p.Parse(in)
		if len(r.Calls) != 1 {
			t.Fatalf("typo=%q: want 1 call, got %d", typo, len(r.Calls))
		}
		if r.Calls[0].Name != want {
			t.Errorf("typo=%q: got %q, want %q (corrected=%d)", typo, r.Calls[0].Name, want, r.Corrected)
		}
	}
}

func TestParse_NoCorrectionWhenDistanceTooLarge(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	// 3 edits away from any known tool — leave as-is.
	in := `{"tool_calls":[{"name":"foobarz","args":{}}]}`
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call passed through, got %d", len(r.Calls))
	}
	if r.Calls[0].Name != "foobarz" {
		t.Errorf("should not have corrected unknown name, got %q", r.Calls[0].Name)
	}
}

func TestParse_FormatBPassthrough(t *testing.T) {
	// Pure text answer — no JSON anywhere.
	p := NewJSONToolCallParser(defaultKnownTools)
	in := "## 根因分析\nshared_buffers 不足，建议调到 4GB"
	r := p.Parse(in)
	if len(r.Calls) != 0 {
		t.Errorf("want no calls for Format B, got %d", len(r.Calls))
	}
	if r.FallbackContent != in {
		t.Errorf("fallback should preserve original; got %q", r.FallbackContent)
	}
}

func TestParse_EmptyInput(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	r := p.Parse("")
	if len(r.Calls) != 0 || r.FallbackContent != "" {
		t.Errorf("empty in should produce empty out; got %+v", r)
	}
}

func TestParse_WhitespaceOnly(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	r := p.Parse("   \n\n   ")
	if len(r.Calls) != 0 {
		t.Errorf("whitespace in should produce no calls; got %+v", r)
	}
}

func TestParse_MalformedJSONReportsError(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	// Mismatched braces, deliberately unrecoverable.
	in := `{"tool_calls": [{"name": "health"`
	r := p.Parse(in)
	if r.ParseError == nil {
		// The brace scanner may not find a balanced } → falls back to Format B.
		// That's also acceptable — what matters is we don't return spurious calls.
		if len(r.Calls) != 0 {
			t.Errorf("malformed input should not yield calls; got %+v", r)
		}
		return
	}
	if !strings.Contains(r.ParseError.Error(), "json parse") {
		t.Errorf("ParseError should mention json parse: %v", r.ParseError)
	}
}

func TestParse_BareToolCallObject(t *testing.T) {
	// LLM forgets the {tool_calls: [...]} envelope and just outputs {name, args}.
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{"name": "health", "args": {}}`
	r := p.Parse(in)
	if len(r.Calls) != 1 || r.Calls[0].Name != "health" {
		t.Errorf("bare tool object not recovered: %+v", r)
	}
}

func TestParse_SingularToolCallEnvelopeArguments(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := "```json\n" + `{
  "tool_call": {
    "name": "health",
    "arguments": {}
  }
}` + "\n```"
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d: %+v", len(r.Calls), r)
	}
	if r.Calls[0].Name != "health" {
		t.Fatalf("unexpected call: %+v", r.Calls[0])
	}
	if r.Calls[0].Arguments != "{}" {
		t.Fatalf("unexpected args: %q", r.Calls[0].Arguments)
	}
	if r.Format != "json_tool_call_single" {
		t.Fatalf("format = %q, want json_tool_call_single", r.Format)
	}
}

func TestParse_BareFunctionToolCallObject(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{"function":{"name":"health","arguments":{}}}`
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d: %+v", len(r.Calls), r)
	}
	if r.Calls[0].Name != "health" || r.Calls[0].Arguments != "{}" {
		t.Fatalf("unexpected call: %+v", r.Calls[0])
	}
}

func TestParse_NullArgsBecomesEmptyObject(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{"tool_calls":[{"name":"health","args":null}]}`
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	if r.Calls[0].Arguments != "{}" {
		t.Errorf("null args should become {}; got %q", r.Calls[0].Arguments)
	}
}

func TestParse_ComplexSQLArgument(t *testing.T) {
	// SQL with embedded quotes is the hard case for JSON encoding.
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `{"tool_calls":[{"name":"sqltune","args":{"args":"SELECT * FROM users WHERE name = \"John\""}}]}`
	r := p.Parse(in)
	if len(r.Calls) != 1 || r.Calls[0].Name != "sqltune" {
		t.Errorf("complex SQL parse failed: %+v", r)
	}
	if !strings.Contains(r.Calls[0].Arguments, "John") {
		t.Errorf("SQL content lost: %q", r.Calls[0].Arguments)
	}
}

func TestParse_ICBCXMLToolCalls(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `<tool_call>
<tool_name>health</tool_name>
<tool_argument>{}</tool_argument>
</tool_call>
<tool_call>
<tool_name>alert</tool_name>
<tool_argument>{}</tool_argument>
</tool_call>`
	r := p.Parse(in)
	if len(r.Calls) != 2 {
		t.Fatalf("want 2 calls, got %d: %+v", len(r.Calls), r)
	}
	if r.Calls[0].Name != "health" || r.Calls[1].Name != "alert" {
		t.Fatalf("unexpected calls: %+v", r.Calls)
	}
	if r.Calls[0].Arguments != "{}" || r.Calls[1].Arguments != "{}" {
		t.Fatalf("unexpected args: %+v", r.Calls)
	}
}

func TestParse_ICBCSimpleXMLToolAfterThink(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `<think>
用户问"当前数据库有什么问题"，这是聚类层问题。
</think>

我先检查数据库整体健康状态。

<tool>
<name>health</name>
<args>
{}
</args>
</tool>`
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d: %+v", len(r.Calls), r)
	}
	if r.Calls[0].Name != "health" {
		t.Fatalf("unexpected call: %+v", r.Calls[0])
	}
	if r.Calls[0].Arguments != "{}" {
		t.Fatalf("unexpected args: %q", r.Calls[0].Arguments)
	}
}

func TestParse_ICBCXMLTextArgumentFallsBackToArgs(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `<tool_call><tool_name>topsql</tool_name><tool_argument>60s</tool_argument></tool_call>`
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d: %+v", len(r.Calls), r)
	}
	if r.Calls[0].Arguments != `{"args":"60s"}` {
		t.Fatalf("unexpected text arg normalization: %q", r.Calls[0].Arguments)
	}
}

func TestParse_XMLFunctionEqualsToolCall(t *testing.T) {
	p := NewJSONToolCallParser(append(defaultKnownTools, "activesessions"))
	in := `<tool_call>
<function=activesessions>
</function>
</tool_call>`
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d: %+v", len(r.Calls), r)
	}
	if r.Calls[0].Name != "activesessions" {
		t.Fatalf("unexpected call: %+v", r.Calls[0])
	}
	if r.Calls[0].Arguments != "{}" {
		t.Fatalf("unexpected args: %q", r.Calls[0].Arguments)
	}
	if r.Format != "xml_function_equals" {
		t.Fatalf("format = %q, want xml_function_equals", r.Format)
	}
}

func TestParse_XMLFunctionEqualsParameters(t *testing.T) {
	p := NewJSONToolCallParser(defaultKnownTools)
	in := `<tool_call>
<function=topsql>
  <parameter=args>60s</parameter>
  <parameter=limit>10</parameter>
</function>
</tool_call>`
	r := p.Parse(in)
	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d: %+v", len(r.Calls), r)
	}
	if r.Calls[0].Name != "topsql" {
		t.Fatalf("unexpected call: %+v", r.Calls[0])
	}
	if !strings.Contains(r.Calls[0].Arguments, `"args":"60s"`) ||
		!strings.Contains(r.Calls[0].Arguments, `"limit":10`) {
		t.Fatalf("unexpected args: %q", r.Calls[0].Arguments)
	}
}

func TestExtractBracedJSON_RespectsStringLiterals(t *testing.T) {
	// Braces inside a string literal should not confuse the depth counter.
	in := `prefix {"key": "value with } inside"} suffix`
	got, ok := extractBracedJSON(in)
	if !ok {
		t.Fatal("should have found JSON")
	}
	want := `{"key": "value with } inside"}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractBracedJSON_HandlesEscapedQuote(t *testing.T) {
	in := `{"sql": "SELECT \"x\" FROM t"}`
	got, ok := extractBracedJSON(in)
	if !ok {
		t.Fatal("should have found JSON")
	}
	if got != in {
		t.Errorf("got %q, want %q", got, in)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"health", "health", 0},
		{"health", "heath", 1},
		{"health", "wealth", 1},
		{"health", "healt", 1},
		{"abc", "xyz", 3},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, c := range cases {
		got := levenshtein(c.a, c.b)
		if got != c.want {
			t.Errorf("levenshtein(%q,%q): got %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
