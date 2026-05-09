/*-------------------------------------------------------------------------
 *
 * engine_test.go
 *	  Test cases for engine.go (engine package): TestEnginePlaybookMode,
 *	  TestEngineMultiTurnDiagnosis, TestEngineMaxTurnsHit.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/engine_test.go
 *
 *-------------------------------------------------------------------------
 */
package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/engine/tool"
)

// ── Mock Provider ──

type mockProvider struct {
	responses []*provider.Response
	callIdx   int
	caps      *provider.ProviderCapability
}

func newMockProvider(responses ...*provider.Response) *mockProvider {
	return &mockProvider{
		responses: responses,
		caps: &provider.ProviderCapability{
			Name:             "mock",
			MaxContextWindow: 32_000,
			MaxOutputTokens:  8000,
			ToolCalling: provider.ToolCallingCapability{
				Supported: true,
				Format:    provider.ToolFormatOpenAICompatible,
			},
		},
	}
}

func (m *mockProvider) Chat(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if m.callIdx >= len(m.responses) {
		return &provider.Response{Content: "(no more mock responses)"}, nil
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockProvider) ChatStream(ctx context.Context, req *provider.Request) (provider.Stream, error) {
	return nil, fmt.Errorf("streaming not supported by mock")
}

func (m *mockProvider) Capability() *provider.ProviderCapability { return m.caps }
func (m *mockProvider) EnhanceRequest(req *provider.Request)       {}
func (m *mockProvider) ParseRateLimitHeaders(h http.Header) *provider.RateLimitInfo {
	return nil
}
func (m *mockProvider) Name() string { return "mock" }

// ── Mock Skill Executor ──

type mockSkillExecutor struct {
	results        map[string]*tool.SkillResult
	securityLevels map[string]int
}

func newMockSkillExecutor() *mockSkillExecutor {
	return &mockSkillExecutor{
		results:        make(map[string]*tool.SkillResult),
		securityLevels: make(map[string]int),
	}
}

func (m *mockSkillExecutor) Execute(ctx context.Context, name string, params map[string]any) (*tool.SkillResult, error) {
	if r, ok := m.results[name]; ok {
		return r, nil
	}
	return &tool.SkillResult{Text: "(no mock result for " + name + ")"}, nil
}

func (m *mockSkillExecutor) SecurityLevel(name string) int {
	if l, ok := m.securityLevels[name]; ok {
		return l
	}
	return 0
}

// ── Tests ──

func TestEnginePlaybookMode(t *testing.T) {
	mockProv := newMockProvider(
		&provider.Response{Content: "## 根因分析\nI/O 等待过高\n## 紧急措施\nkill session\n## 根因修复\n建索引"},
	)

	eng := New(mockProv, &testProfile{}, newMockSkillExecutor(), nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "数据库响应变慢",
		Mode:         ModePlaybook,
		DatabaseInfo: DatabaseInfo{Product: "oracle", Version: "19c"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TurnsUsed != 1 {
		t.Errorf("playbook should use 1 turn, got %d", result.TurnsUsed)
	}
	if !strings.Contains(result.Content, "根因分析") {
		t.Error("expected diagnosis content")
	}
	if len(result.ToolsInvoked) != 0 {
		t.Error("playbook should not invoke tools")
	}
}

func TestEngineMultiTurnDiagnosis(t *testing.T) {
	executor := newMockSkillExecutor()
	executor.results["waits"] = &tool.SkillResult{Text: "db file sequential read 52%"}
	executor.results["topsql"] = &tool.SkillResult{Text: "SQL_ID=8a4kd3xmn1, elapsed=823ms"}

	mockProv := newMockProvider(
		&provider.Response{
			Content: "我先查等待事件和 Top SQL",
			ToolCalls: []provider.ToolCall{
				{ID: "tc1", Name: "waits", Arguments: `{}`},
				{ID: "tc2", Name: "topsql", Arguments: `{}`},
			},
		},
		&provider.Response{
			Content: "## 根因分析\ndb file sequential read 占 52%\n## 紧急措施\n优化SQL\n## 根因修复\n建索引",
		},
	)

	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "数据库响应变慢",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "oracle"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TurnsUsed != 2 {
		t.Errorf("expected 2 turns, got %d", result.TurnsUsed)
	}
	if len(result.ToolsInvoked) != 2 {
		t.Errorf("expected 2 tools, got %d", len(result.ToolsInvoked))
	}
	if !strings.Contains(result.Content, "根因分析") {
		t.Error("expected final diagnosis")
	}
}

func TestEngineMaxTurnsHit(t *testing.T) {
	executor := newMockSkillExecutor()
	executor.results["waits"] = &tool.SkillResult{Text: "data"}

	responses := make([]*provider.Response, 5)
	for i := range responses {
		responses[i] = &provider.Response{
			Content:   "继续查询",
			ToolCalls: []provider.ToolCall{{ID: "tc", Name: "waits", Arguments: `{}`}},
		}
	}
	mockProv := newMockProvider(responses...)

	eng := New(mockProv, &testProfile{}, executor, nil, WithConfig(EngineConfig{
		DefaultMaxTurns:     3,
		DefaultMaxTokens:    8000,
		EnableCompression:   false,
		MaxOutputRecoveries: 0,
	}))

	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "test",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "oracle"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.MaxTurnsHit {
		t.Error("expected MaxTurnsHit=true")
	}
	if result.TurnsUsed != 3 {
		t.Errorf("expected 3 turns, got %d", result.TurnsUsed)
	}
}

func TestEngineProgressCallback(t *testing.T) {
	executor := newMockSkillExecutor()
	executor.results["waits"] = &tool.SkillResult{Text: "data"}

	mockProv := newMockProvider(
		&provider.Response{
			Content:   "查询中",
			ToolCalls: []provider.ToolCall{{ID: "tc", Name: "waits", Arguments: `{}`}},
		},
		&provider.Response{Content: "完成"},
	)

	var roundCalls []int
	eng := New(mockProv, &testProfile{}, executor, nil)
	_, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "test",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "oracle"},
		OnRound: func(turn int, toolNames []string) {
			roundCalls = append(roundCalls, turn)
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roundCalls) != 1 {
		t.Errorf("expected 1 OnRound call, got %d", len(roundCalls))
	}
}

func TestEngineStreamCallback(t *testing.T) {
	mockProv := newMockProvider(
		&provider.Response{Content: "final diagnosis output"},
	)

	var streamed string
	eng := New(mockProv, &testProfile{}, newMockSkillExecutor(), nil)
	_, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "test",
		Mode:         ModePlaybook,
		DatabaseInfo: DatabaseInfo{Product: "oracle"},
		OnStream:     func(delta string) { streamed += delta },
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if streamed != "final diagnosis output" {
		t.Errorf("expected streamed content, got %q", streamed)
	}
}

// ── Streaming Mock Provider ──

// mockStream replays a pre-built sequence of StreamEvents.
type mockStream struct {
	events []provider.StreamEvent
	idx    int
}

func (s *mockStream) Next() (provider.StreamEvent, error) {
	if s.idx >= len(s.events) {
		return provider.StreamEvent{Type: provider.StreamDone}, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *mockStream) Close() error { return nil }

// streamMockProvider is a mock that supports ChatStream with pre-built event sequences.
type streamMockProvider struct {
	mockProvider
	streamEvents [][]provider.StreamEvent // one event sequence per ChatStream call
	streamIdx    int
}

func newStreamMockProvider(
	chatResponses []*provider.Response,
	streamEvents [][]provider.StreamEvent,
) *streamMockProvider {
	base := newMockProvider(chatResponses...)
	return &streamMockProvider{
		mockProvider: *base,
		streamEvents: streamEvents,
	}
}

func (m *streamMockProvider) ChatStream(ctx context.Context, req *provider.Request) (provider.Stream, error) {
	if m.streamIdx >= len(m.streamEvents) {
		return nil, fmt.Errorf("no more stream events")
	}
	events := m.streamEvents[m.streamIdx]
	m.streamIdx++
	return &mockStream{events: events}, nil
}

func TestEngineOnStreamFinalRound(t *testing.T) {
	// OnStream callback receives full content on the final round (no tool calls)
	mockProv := newMockProvider(
		&provider.Response{Content: "Hello world!"},
	)

	var chunks []string
	eng := New(mockProv, &testProfile{}, newMockSkillExecutor(), nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "test",
		Mode:         ModePlaybook,
		DatabaseInfo: DatabaseInfo{Product: "oracle"},
		OnStream:     func(delta string) { chunks = append(chunks, delta) },
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Hello world!" {
		t.Errorf("expected 'Hello world!', got %q", result.Content)
	}
	if len(chunks) != 1 || chunks[0] != "Hello world!" {
		t.Errorf("expected OnStream called once with full content, got %v", chunks)
	}
}

func TestEngineOnStreamIntermediateRoundSuppressed(t *testing.T) {
	// Round 1: tool calls → intermediate, OnStream NOT called
	// Round 2: no tool calls → final, OnStream called with content
	executor := newMockSkillExecutor()
	executor.results["waits"] = &tool.SkillResult{Text: "wait data"}

	mockProv := newMockProvider(
		&provider.Response{
			Content:   "Checking...",
			ToolCalls: []provider.ToolCall{{ID: "tc1", Name: "waits", Arguments: `{}`}},
		},
		&provider.Response{Content: "Final answer"},
	)

	var chunks []string
	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "test",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "oracle"},
		OnStream:     func(delta string) { chunks = append(chunks, delta) },
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Final answer" {
		t.Errorf("expected 'Final answer', got %q", result.Content)
	}
	// Only the final round's content should be streamed
	if len(chunks) != 1 || chunks[0] != "Final answer" {
		t.Errorf("expected OnStream only for final round, got %v", chunks)
	}
}

func TestEngineOnStreamWithThinking(t *testing.T) {
	// Chat() returns content + thinking. OnStream only gets visible content.
	mockProv := newMockProvider(
		&provider.Response{Content: "Visible output", Thinking: "Let me analyze..."},
	)

	var chunks []string
	eng := New(mockProv, &testProfile{}, newMockSkillExecutor(), nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "test",
		Mode:         ModePlaybook,
		DatabaseInfo: DatabaseInfo{Product: "oracle"},
		OnStream:     func(delta string) { chunks = append(chunks, delta) },
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Visible output" {
		t.Errorf("expected 'Visible output', got %q", result.Content)
	}
	if result.Thinking != "Let me analyze..." {
		t.Errorf("expected thinking 'Let me analyze...', got %q", result.Thinking)
	}
	if len(chunks) != 1 || chunks[0] != "Visible output" {
		t.Errorf("expected OnStream with visible content only, got %v", chunks)
	}
}

func TestEngineStreamFallbackToChat(t *testing.T) {
	// When ChatStream fails, should fallback to Chat() via callWithRetry
	mockProv := newMockProvider(
		&provider.Response{Content: "fallback response"},
	)

	var chunks []string
	eng := New(mockProv, &testProfile{}, newMockSkillExecutor(), nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "test",
		Mode:         ModePlaybook,
		DatabaseInfo: DatabaseInfo{Product: "oracle"},
		OnStream:     func(delta string) { chunks = append(chunks, delta) },
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "fallback response" {
		t.Errorf("expected 'fallback response', got %q", result.Content)
	}
	// streamFinalResponse: ChatStream fails → falls back to Chat() → returns content as single chunk
	if len(chunks) != 1 || chunks[0] != "fallback response" {
		t.Errorf("expected 1 chunk with 'fallback response', got %v", chunks)
	}
}

// ── Test profile ──

type testProfile struct{}

func (p *testProfile) Product() string              { return "oracle" }
func (p *testProfile) SystemPromptRules() string    { return "# Test rules" }
func (p *testProfile) ToolUsageHint(name string) string { return "" }
func (p *testProfile) ToolFilter(mode string) func(string, int) bool {
	return func(name string, level int) bool { return true }
}
func (p *testProfile) DefaultMaxTurns(mode string) int { return 20 }
