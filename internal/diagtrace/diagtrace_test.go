package diagtrace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderLast(t *testing.T) {
	t.Setenv("OPENDB_DIAGTRACE_DIR", t.TempDir())
	store.Lock()
	store.last = nil
	store.Unlock()
	SetLast(Event{
		Input:         "当前有哪几个wdr报告",
		Intent:        "wdr_list",
		Mode:          "direct_skill",
		Skill:         "wdr",
		Params:        map[string]any{},
		Reason:        "matched WDR list intent",
		Confidence:    0.95,
		LLMUsed:       false,
		Model:         "qwen3-32b-prompt",
		PromptSummary: "用户问题: 当前有哪几个wdr报告",
		PromptBytes:   42,
		PromptHash:    "abcdef1234567890",
		InputTokens:   12,
		OutputTokens:  34,
		Status:        "ok",
		StartedAt:     time.Unix(1, 0),
		EndedAt:       time.Unix(2, 0),
		ToolCalls:     []ToolCall{{Name: "wdr", Status: "ok", Elapsed: time.Second, OutputSummary: "WDR 快照 20 条", OutputBytes: 256, OutputHash: "0123456789abcdef"}},
	})
	out := RenderLast()
	for _, want := range []string{"intent: wdr_list", "mode: direct_skill", "route_kind: direct skill", "skill: wdr", "model: qwen3-32b-prompt", "prompt: 用户问题", "prompt_bytes: 42", "prompt_hash: abcdef1234567890", "tokens: input=12 output=34 total=46", "tool_call_count: 1", "output=256B", "sha256=0123456789abcdef", "output_summary: WDR 快照 20 条", "tool_calls", "llm: false"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderLast missing %q:\n%s", want, out)
		}
	}
}

func TestRenderLastLoadsPersistedTrace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENDB_DIAGTRACE_DIR", dir)
	store.Lock()
	store.last = nil
	store.Unlock()

	SetLast(Event{
		Input:        "当前数据库存在什么问题",
		Intent:       "current_db_diag",
		Mode:         "evidence_then_llm",
		LLMUsed:      true,
		Model:        "opus",
		Status:       "ok",
		StartedAt:    time.Unix(10, 0),
		EndedAt:      time.Unix(12, 0),
		Rounds:       []string{"第1轮: 调用 health", "第2轮: 基于已采集证据生成诊断报告"},
		RoundDetails: []RoundDetail{{Round: 1, Summary: "调用 health", Elapsed: 1200 * time.Millisecond}, {Round: 2, Summary: "基于已采集证据生成诊断报告", Elapsed: 500 * time.Millisecond}},
	})
	if _, err := os.Stat(filepath.Join(dir, "last.json")); err != nil {
		t.Fatalf("last.json not persisted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history.jsonl")); err != nil {
		t.Fatalf("history.jsonl not persisted: %v", err)
	}

	store.Lock()
	store.last = nil
	store.Unlock()
	out := RenderLast()
	for _, want := range []string{"intent: current_db_diag", "mode: evidence_then_llm", "model: opus", "round_count: 2", "第1轮: 调用 health", "1.2s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("persisted RenderLast missing %q:\n%s", want, out)
		}
	}
}

func TestRenderLastJSON(t *testing.T) {
	t.Setenv("OPENDB_DIAGTRACE_DIR", t.TempDir())
	store.Lock()
	store.last = nil
	store.Unlock()
	SetLast(Event{Input: "sql id 581990336 如何优化", Intent: "sqltune", Mode: "evidence_then_llm", Model: "qwen3-32b-prompt", InputTokens: 101, OutputTokens: 202, Status: "ok"})
	out := RenderLastJSON()
	for _, want := range []string{`"Input": "sql id 581990336 如何优化"`, `"Intent": "sqltune"`, `"InputTokens": 101`, `"OutputTokens": 202`} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderLastJSON missing %q:\n%s", want, out)
		}
	}
}

func TestRenderHistoryJSON(t *testing.T) {
	t.Setenv("OPENDB_DIAGTRACE_DIR", t.TempDir())
	store.Lock()
	store.last = nil
	store.Unlock()
	SetLast(Event{Input: "one", Intent: "current_db_diag", Status: "ok"})
	SetLast(Event{Input: "two", Intent: "wdr_list", Status: "ok"})
	out := RenderHistoryJSON(1)
	if strings.Contains(out, `"Input": "one"`) || !strings.Contains(out, `"Input": "two"`) {
		t.Fatalf("RenderHistoryJSON limit not honored:\n%s", out)
	}
}
