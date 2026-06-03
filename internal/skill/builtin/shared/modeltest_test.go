package shared

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/skill"
)

type modelTestProvider struct {
	resp         *llm.Response
	err          error
	req          llm.ChatRequest
	streamEvents []llm.StreamEvent
	streamErr    error
	streamReq    llm.ChatRequest
}

func (p *modelTestProvider) Name() string { return "mock" }

func (p *modelTestProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.Response, error) {
	p.req = req
	if p.err != nil {
		return nil, p.err
	}
	return p.resp, nil
}

func (p *modelTestProvider) ChatStream(_ context.Context, req llm.ChatRequest) (llm.Stream, error) {
	p.streamReq = req
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	return &modelTestStream{events: p.streamEvents}, nil
}

type modelTestStream struct {
	events []llm.StreamEvent
	idx    int
}

func (s *modelTestStream) Next() (llm.StreamEvent, error) {
	if s.idx >= len(s.events) {
		return llm.StreamEvent{Type: llm.StreamDone}, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *modelTestStream) Close() error { return nil }

func TestModelTestSkillOK(t *testing.T) {
	provider := &modelTestProvider{resp: &llm.Response{
		Content: "pong",
		Usage:   llm.Usage{InputTokens: 12, OutputTokens: 3},
	}}
	mgr := model.NewManagerForTest(provider, "large")
	result, err := NewModelTestSkill(mgr).Execute(context.Background(), skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"status: ok", "content: pong", "tokens: input=12 output=3 total=15", "request: chat/completions"} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("modeltest output missing %q:\n%s", want, result.Rendered)
		}
	}
	if provider.req.MaxTokens != modelTestMaxTokens {
		t.Fatalf("MaxTokens=%d want %d", provider.req.MaxTokens, modelTestMaxTokens)
	}
	if provider.req.Temperature == nil || *provider.req.Temperature != 0 {
		t.Fatalf("Temperature=%v want 0", provider.req.Temperature)
	}
}

func TestModelTestSkillError(t *testing.T) {
	provider := &modelTestProvider{err: errors.New("openai: HTTP 504: upstream timeout")}
	mgr := model.NewManagerForTest(provider, "large")
	result, err := NewModelTestSkill(mgr).Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "timeout=120"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"status: error", "timeout: 2m0s", "HTTP 504"} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("modeltest error output missing %q:\n%s", want, result.Rendered)
		}
	}
}

func TestModelTestSkillNativeFCRequiresToolCall(t *testing.T) {
	provider := &modelTestProvider{resp: &llm.Response{
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "health", Arguments: "{}"}},
	}}
	mgr := model.NewManagerForTest(provider, "large")
	result, err := NewModelTestSkill(mgr).Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "fc"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"mode: fc", "status: ok", "tool_calls:", "health"} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("modeltest fc output missing %q:\n%s", want, result.Rendered)
		}
	}
	if len(provider.req.Tools) != 1 {
		t.Fatalf("native fc request tools=%d want 1", len(provider.req.Tools))
	}
	if provider.req.MaxTokens != modelTestNativeFCMaxTokens {
		t.Fatalf("native fc MaxTokens=%d want %d", provider.req.MaxTokens, modelTestNativeFCMaxTokens)
	}
	if provider.req.ToolChoice != nil {
		t.Fatalf("native fc default ToolChoice=%v want nil", provider.req.ToolChoice)
	}
}

func TestModelTestSkillNativeFCForceUsesToolChoice(t *testing.T) {
	provider := &modelTestProvider{resp: &llm.Response{
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "health", Arguments: "{}"}},
	}}
	mgr := model.NewManagerForTest(provider, "large")
	result, err := NewModelTestSkill(mgr).Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "fc force=true"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Rendered, "tool_choice=health") {
		t.Fatalf("modeltest fc force output missing tool_choice:\n%s", result.Rendered)
	}
	if provider.req.ToolChoice == nil {
		t.Fatalf("native fc force ToolChoice is nil")
	}
}

func TestModelTestSkillNativeFCNoToolCallsShowsContent(t *testing.T) {
	provider := &modelTestProvider{resp: &llm.Response{
		Content:    "我将检查健康状态。",
		Usage:      llm.Usage{InputTokens: 20, OutputTokens: 8},
		StopReason: "stop",
	}}
	mgr := model.NewManagerForTest(provider, "large")
	result, err := NewModelTestSkill(mgr).Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "fc"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"mode: fc", "status: error", "tools=1 max_tokens=256", "model returned no native tool_calls", "content: 我将检查健康状态。"} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("modeltest fc no-tool output missing %q:\n%s", want, result.Rendered)
		}
	}
	if strings.Contains(result.Rendered, "tool_choice=health") {
		t.Fatalf("modeltest fc default unexpectedly forced tool_choice:\n%s", result.Rendered)
	}
}

func TestModelTestSkillPromptFCParsesToolCall(t *testing.T) {
	provider := &modelTestProvider{resp: &llm.Response{
		Content: `{"tool_calls":[{"name":"health","args":{}}]}`,
	}}
	mgr := model.NewManagerForTest(provider, "large")
	result, err := NewModelTestSkill(mgr).Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "promptfc"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"mode: promptfc", "status: ok", "parser_status: ok", "health"} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("modeltest promptfc output missing %q:\n%s", want, result.Rendered)
		}
	}
	if len(provider.req.Tools) != 0 {
		t.Fatalf("promptfc request tools=%d want 0", len(provider.req.Tools))
	}
}

func TestModelTestSkillStream(t *testing.T) {
	provider := &modelTestProvider{streamEvents: []llm.StreamEvent{
		{Type: llm.StreamTextDelta, Content: "po"},
		{Type: llm.StreamTextDelta, Content: "ng"},
		{Type: llm.StreamDone, FinishReason: "stop"},
	}}
	mgr := model.NewManagerForTest(provider, "large")
	result, err := NewModelTestSkill(mgr).Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "stream"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"mode: stream", "status: ok", "chunks: 2", "content: pong"} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("modeltest stream output missing %q:\n%s", want, result.Rendered)
		}
	}
}
