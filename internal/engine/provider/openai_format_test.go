package provider

import (
	"testing"
)

// TestParseOpenAINestedFormat verifies parser handles {"function":{...}} form
// emitted by Qwen3/GLM in prompt mode (v1.2.6 fix).
func TestParseOpenAINestedFormat(t *testing.T) {
	p := NewJSONToolCallParser([]string{"sqltune", "health"})
	input := `{"tool_calls":[{"function":{"name":"sqltune","arguments":{"args":"581990336","mode":"quick"}}}]}`
	r := p.Parse(input)
	if r.ParseError != nil {
		t.Fatalf("unexpected parse error: %v", r.ParseError)
	}
	if len(r.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d (fallback=%q)", len(r.Calls), r.FallbackContent)
	}
	tc := r.Calls[0]
	if tc.Name != "sqltune" {
		t.Fatalf("expected name=sqltune, got %q", tc.Name)
	}
	if tc.Arguments == "" || tc.Arguments == "{}" {
		t.Fatalf("expected non-empty args, got %q", tc.Arguments)
	}
	t.Logf("parser direct: name=%s, args=%s", tc.Name, tc.Arguments)
}

// TestStreamingParserNestedFormat - same JSON but through StreamingParser.
func TestStreamingParserNestedFormat(t *testing.T) {
	p := NewStreamingParser([]string{"sqltune", "health"}, 64)
	input := `{"tool_calls":[{"function":{"name":"sqltune","arguments":{"args":"581990336","mode":"quick"}}}]}`
	// feed in chunks
	chunks := []string{input[:30], input[30:60], input[60:]}
	for _, c := range chunks {
		text, mode := p.Feed(c)
		t.Logf("Feed(%q): text=%q mode=%v", c, text, mode)
	}
	calls, text, mode, err := p.Finish()
	t.Logf("Finish: calls=%v text=%q mode=%v err=%v", calls, text, mode, err)
	if len(calls) != 1 {
		t.Fatalf("streaming: expected 1 call, got %d (text=%q)", len(calls), text)
	}
	if calls[0].Name != "sqltune" {
		t.Fatalf("streaming: expected name=sqltune, got %q", calls[0].Name)
	}
}
