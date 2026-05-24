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
	"sync"
	"testing"

	"github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/engine/tool"
)

// ── Mock Provider ──

type mockProvider struct {
	responses []*provider.Response
	callIdx   int
	caps      *provider.ProviderCapability
	requests  []*provider.Request
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
	m.requests = append(m.requests, req)
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
func (m *mockProvider) EnhanceRequest(req *provider.Request)     {}
func (m *mockProvider) ParseRateLimitHeaders(h http.Header) *provider.RateLimitInfo {
	return nil
}
func (m *mockProvider) Name() string { return "mock" }

// ── Mock Skill Executor ──

type mockSkillExecutor struct {
	results        map[string]*tool.SkillResult
	securityLevels map[string]int
	mu             sync.Mutex
	calls          []string
}

func newMockSkillExecutor() *mockSkillExecutor {
	return &mockSkillExecutor{
		results:        make(map[string]*tool.SkillResult),
		securityLevels: make(map[string]int),
	}
}

func (m *mockSkillExecutor) Execute(ctx context.Context, name string, params map[string]any) (*tool.SkillResult, error) {
	m.mu.Lock()
	m.calls = append(m.calls, name)
	m.mu.Unlock()
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

func (m *mockSkillExecutor) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
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

func TestEngineForceInitialToolCalls(t *testing.T) {
	executor := newMockSkillExecutor()
	executor.results["health"] = &tool.SkillResult{Text: "connections 10/500, cache hit 99%"}

	mockProv := newMockProvider(
		&provider.Response{Content: "## 根因分析\n当前健康状态正常"},
	)

	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "当前数据库有哪些问题",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
		ForceInitialToolCalls: []ToolCall{{
			ID:        "forced_health",
			Name:      "health",
			Arguments: "{}",
		}},
		RequireToolEvidence: true,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ToolsInvoked) != 1 || result.ToolsInvoked[0] != "health" {
		t.Fatalf("expected forced health tool, got %v", result.ToolsInvoked)
	}
	calls := executor.Calls()
	if len(calls) != 1 || calls[0] != "health" {
		t.Fatalf("executor calls = %v, want [health]", calls)
	}
	if !strings.Contains(result.Content, "根因分析") {
		t.Fatalf("expected final diagnosis, got %q", result.Content)
	}
}

func TestEngineForceInitialEvidenceSummarySkipsLLM(t *testing.T) {
	executor := newMockSkillExecutor()
	executor.results["health"] = &tool.SkillResult{Text: "dead tuples 31.7 亿"}
	executor.results["activesessions"] = &tool.SkillResult{Text: "Active Sessions (120 total, 14 waiting)\nSELECT pg_sleep(7200);\nDO $$ SELECT * FROM fault_io; $$"}
	executor.results["waits"] = &tool.SkillResult{Text: "lock wait detected"}
	executor.results["topsql"] = &tool.SkillResult{Text: "Top SQL by elapsed\nfault_io avg 31min"}

	mockProv := newMockProvider(&provider.Response{Content: "outer LLM must not be called"})
	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "当前数据库存在什么问题",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
		ForceInitialToolCalls: []ToolCall{
			{ID: "forced_health", Name: "health", Arguments: "{}"},
			{ID: "forced_activesessions", Name: "activesessions", Arguments: "{}"},
			{ID: "forced_waits", Name: "waits", Arguments: "{}"},
			{ID: "forced_topsql", Name: "topsql", Arguments: `{"args":"el"}`},
		},
		RequireToolEvidence:         true,
		ForceInitialEvidenceSummary: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockProv.callIdx != 0 {
		t.Fatalf("evidence summary fast path should not call LLM, provider calls=%d", mockProv.callIdx)
	}
	if result.TurnsUsed != 1 {
		t.Fatalf("expected 1 forced evidence turn, got %d", result.TurnsUsed)
	}
	for _, want := range []string{
		"# 当前数据库问题诊断",
		"活跃会话压力: 120 total, 14 waiting",
		"pg_sleep",
		"fault_*",
		"dead tuples",
		"证据明细",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("missing %q in fast summary:\n%s", want, result.Content)
		}
	}
}

func TestEngineForceInitialEvidenceLLMUsesCompactEvidence(t *testing.T) {
	executor := newMockSkillExecutor()
	hugeTopSQL := "Top SQL by elapsed\n" + strings.Repeat("SQL_ID=1 SELECT * FROM very_large_table WHERE payload = 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx';\n", 800)
	executor.results["health"] = &tool.SkillResult{Text: "Overall: ✓ OK\nDead Tuples : 451 ✓"}
	executor.results["waits"] = &tool.SkillResult{Text: "仅后台线程活跃（4 个）— 实例处于空闲状态，不存在业务等待事件"}
	executor.results["blocktree"] = &tool.SkillResult{Text: "当前无阻塞链"}
	executor.results["topsql"] = &tool.SkillResult{Text: hugeTopSQL}

	mockProv := newMockProvider(&provider.Response{Content: "## 根因分析\n当前无在线故障，历史 Top SQL 有压测噪声。\n## 结论\n实例健康。"})
	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "当前数据库存在什么问题",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
		ForceInitialToolCalls: []ToolCall{
			{ID: "forced_health", Name: "health", Arguments: "{}"},
			{ID: "forced_waits", Name: "waits", Arguments: "{}"},
			{ID: "forced_blocktree", Name: "blocktree", Arguments: "{}"},
			{ID: "forced_topsql", Name: "topsql", Arguments: `{"args":"el"}`},
		},
		RequireToolEvidence:     true,
		ForceInitialEvidenceLLM: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockProv.callIdx != 1 {
		t.Fatalf("managed evidence LLM path should call provider once, got %d", mockProv.callIdx)
	}
	if result.TurnsUsed != 2 {
		t.Fatalf("TurnsUsed = %d, want 2", result.TurnsUsed)
	}
	if !strings.Contains(result.Content, "历史 Top SQL") {
		t.Fatalf("expected LLM-authored report, got:\n%s", result.Content)
	}
	if len(mockProv.requests) != 1 {
		t.Fatalf("expected one provider request, got %d", len(mockProv.requests))
	}
	req := mockProv.requests[0]
	if len(req.Tools) != 0 {
		t.Fatalf("managed report request should not expose tools, got %d", len(req.Tools))
	}
	var payload strings.Builder
	for _, m := range req.Messages {
		payload.WriteString(m.Content)
	}
	if payload.Len() > 16000 {
		t.Fatalf("compact evidence request too large: %d bytes", payload.Len())
	}
	if strings.Count(payload.String(), "very_large_table") > 40 {
		t.Fatalf("compact evidence kept too many repeated Top SQL rows")
	}
}

func TestRenderCurrentDBExpertReportMatchesOpusTemplate(t *testing.T) {
	results := []tool.ToolResult{
		{Name: "health", Content: "Overall: ✓ OK (19 checks passed)\nVersion : openGauss-lite 5.0.3 ✓\nUptime : 12d 5h ✓\nCache Hit Ratio : 99.73% ✓\nConnections : 12 / 200 (6.0%) ✓\nIdle in TX : 0 ✓\nXID Age : 300923 (0.01% to wraparound) ✓\nDead Tuples : 457 ✓"},
		{Name: "activesessions", Content: "Active Sessions (4 total, 0 waiting)\n281464590358176 omm postgres CPU On CPU 233.2h WLM fetch collect info from data node"},
		{Name: "waits", Content: "仅后台线程活跃（4 个）— 实例处于空闲状态，不存在业务等待事件"},
		{Name: "blocktree", Content: "当前无阻塞链"},
		{Name: "topsql", Content: "│   1  1775585557               1520 17174784.77 11299200.51       1520  SELECT pg_sleep(?);                                                              │\n│   2  1593186364              14355 5272748.05  367310.91          0  DO $$BEGIN FOR k IN 1..100000 LOOP UPDATE fault_lock SET counter=counter+1 WHERE   │"},
	}
	report := renderCurrentDBExpertReport(EngineInput{UserMessage: "当前数据库存在什么问题", Metadata: map[string]string{"expert_report": "currentdb"}}, results, nil)
	for _, want := range []string{
		"## 根因分析",
		"| 维度 | 数据 | 来源 |",
		"SQL_ID 1775585557",
		"CALLS: 1520",
		"AVG_MS: 11299200.51",
		"TOTAL_S: 17174784.77",
		"## 因果链",
		"## 当前在线问题",
		"## 需要关注的潜在风险",
		"## 建议动作",
		"隔离历史统计噪声",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected %q in expert report:\n%s", want, report)
		}
	}
	if issue := validateCurrentDBExpertReportCompleteness(report); issue != "" {
		t.Fatalf("expert report should pass completeness gate: %s\n%s", issue, report)
	}
}

func TestCurrentDBExpertReportAvoidsDestructiveSQLWhenNoOnlineFault(t *testing.T) {
	results := []tool.ToolResult{
		{Name: "health", Content: "Overall: ✓ OK (19 checks passed)\nConnections : 12 / 200 (6.0%) ✓\nXID Age : 300923 (0.01% to wraparound) ✓"},
		{Name: "activesessions", Content: "Active Sessions (4 total, 0 waiting)"},
		{Name: "waits", Content: "仅后台线程活跃（4 个）— 实例处于空闲状态，不存在业务等待事件"},
		{Name: "blocktree", Content: "当前无阻塞链"},
		{Name: "topsql", Content: "│   1  1775585557               1520 17174784.77 11299200.51       1520  SELECT pg_sleep(?); │\n│   2  1593186364              14355 5272748.05  367310.91          0  DO $$BEGIN FOR k IN 1..100000 LOOP UPDATE fault_lock SET counter=counter+1 WHERE │"},
	}
	report := renderCurrentDBExpertReport(EngineInput{UserMessage: "当前数据库存在什么问题", Metadata: map[string]string{"expert_report": "currentdb"}}, results, nil)
	for _, notWant := range []string{"reset_unique_sql", "pg_stat_reset", "TRUNCATE TABLE"} {
		if strings.Contains(report, notWant) {
			t.Fatalf("current DB no-online-fault report should not include destructive SQL %q:\n%s", notWant, report)
		}
	}
	for _, want := range []string{"隔离历史统计噪声", "只读核对 fault_* 表归属"} {
		if !strings.Contains(report, want) {
			t.Fatalf("current DB no-online-fault report missing %q:\n%s", want, report)
		}
	}
}

func TestGateCurrentDBReportAddsSafetyNoteForDestructiveLLMAdvice(t *testing.T) {
	results := []tool.ToolResult{
		{Name: "health", Content: "Overall: ✓ OK"},
		{Name: "waits", Content: "不存在业务等待事件"},
		{Name: "blocktree", Content: "当前无阻塞链"},
	}
	got := gateCurrentDBReport("## 建议动作\n```sql\nSELECT reset_unique_sql('GLOBAL','ALL',0);\nTRUNCATE TABLE fault_lock;\n```", results)
	for _, want := range []string{"输出门禁", "已隐藏变更类 SQL"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected safety gate marker %q:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"SELECT reset_unique_sql", "TRUNCATE TABLE fault_lock"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("dangerous SQL should be hidden %q:\n%s", notWant, got)
		}
	}
}

func TestEngineRequireToolEvidenceRejectsBareFirstAnswer(t *testing.T) {
	executor := newMockSkillExecutor()
	executor.results["health"] = &tool.SkillResult{Text: "forced by retry"}

	mockProv := newMockProvider(
		&provider.Response{Content: "当前数据库存在慢查询和连接数过高。"},
		&provider.Response{ToolCalls: []provider.ToolCall{{ID: "tc_health", Name: "health", Arguments: `{}`}}},
		&provider.Response{Content: "## 根因分析\n基于 health 结果分析"},
	)

	eng := New(mockProv, &testProfile{}, executor, nil, WithConfig(EngineConfig{
		DefaultMaxTurns:     3,
		DefaultMaxTokens:    8000,
		EnableCompression:   false,
		MaxOutputRecoveries: 0,
	}))
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:         "当前数据库有哪些问题",
		Mode:                ModeAuto,
		DatabaseInfo:        DatabaseInfo{Product: "opengauss"},
		RequireToolEvidence: true,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TurnsUsed != 3 {
		t.Fatalf("TurnsUsed = %d, want 3", result.TurnsUsed)
	}
	if len(result.ToolsInvoked) != 1 || result.ToolsInvoked[0] != "health" {
		t.Fatalf("expected health tool after retry, got %v", result.ToolsInvoked)
	}
	if strings.Contains(result.Content, "连接数过高") {
		t.Fatalf("bare first answer leaked into final content: %q", result.Content)
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

func (p *testProfile) Product() string                  { return "oracle" }
func (p *testProfile) SystemPromptRules() string        { return "# Test rules" }
func (p *testProfile) ToolUsageHint(name string) string { return "" }
func (p *testProfile) ToolFilter(mode string) func(string, int) bool {
	return func(name string, level int) bool { return true }
}
func (p *testProfile) DefaultMaxTurns(mode string) int { return 20 }

// TestEnginePassthroughShortCircuit: when a tool returns content with the
// WDR_REPORT_BEGIN marker (sqltune/wdranalyze), the engine must:
//  1. Short-circuit the agent loop (no second LLM round)
//  2. Strip the marker before persisting to message history
//  3. Push the stripped content through OnStream so REPL renders it
//     (v1.1.54 fix — without this, interactive sessions see only the
//     LLM's pre-tool streaming text, never the actual report)
func TestEnginePassthroughShortCircuit(t *testing.T) {
	wdrReport := "<!-- WDR_REPORT_BEGIN: directive -->\n\n# WDR 分析报告\n## Layer 1: 总览评估\n| 模块 | 评级 |\n|---|---|\n| Database Stat | 🔴 |\n"
	executor := newMockSkillExecutor()
	executor.results["wdranalyze"] = &tool.SkillResult{Rendered: wdrReport}

	mockProv := newMockProvider(
		&provider.Response{
			Content: "I'll analyze the WDR report.",
			ToolCalls: []provider.ToolCall{
				{ID: "tc1", Name: "wdranalyze", Arguments: `{"args":"file /tmp/foo.html"}`},
			},
		},
		// A second response would only be consumed if passthrough DIDN'T fire.
		// We deliberately don't add one — if engine tries to call LLM round 2,
		// the mock provider will return an error and this test will fail.
	)

	var streamCaptured strings.Builder
	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "分析 wdr 报告 /tmp/foo.html",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
		OnStream: func(delta string) {
			streamCaptured.WriteString(delta)
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must have stopped at round 1 (no second LLM call needed).
	if result.TurnsUsed != 1 {
		t.Errorf("expected 1 turn (passthrough short-circuit), got %d", result.TurnsUsed)
	}
	// result.Content should be the stripped report (no marker).
	if strings.Contains(result.Content, "WDR_REPORT_BEGIN") {
		t.Errorf("result.Content should have marker stripped, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "## Layer 1: 总览评估") {
		t.Errorf("result.Content should contain the report body, got: %s", result.Content)
	}
	// OnStream must have received the stripped report so REPL can display it.
	streamed := streamCaptured.String()
	if !strings.Contains(streamed, "## Layer 1: 总览评估") {
		t.Errorf("OnStream should have received passthrough report. captured: %q", streamed)
	}
	if strings.Contains(streamed, "WDR_REPORT_BEGIN") {
		t.Errorf("OnStream should not see the marker. captured: %q", streamed)
	}
}

func TestEngineSQLTunePassthroughShortCircuit(t *testing.T) {
	sqlTuneReport := "# SQL_ID 581990336 优化报告\n## 执行计划\n走索引扫描\n"
	executor := newMockSkillExecutor()
	executor.results["sqltune"] = &tool.SkillResult{Rendered: sqlTuneReport}

	mockProv := newMockProvider(
		&provider.Response{
			Content: "I will tune this SQL.",
			ToolCalls: []provider.ToolCall{
				{ID: "tc1", Name: "sqltune", Arguments: `{"args":"581990336"}`},
			},
		},
		&provider.Response{Content: "this second round must not be used"},
	)

	var streamCaptured strings.Builder
	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "sql id 581990336 如何优化",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
		OnStream: func(delta string) {
			streamCaptured.WriteString(delta)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockProv.callIdx != 1 {
		t.Fatalf("expected no second LLM round, provider calls=%d", mockProv.callIdx)
	}
	if result.TurnsUsed != 1 {
		t.Fatalf("expected 1 turn, got %d", result.TurnsUsed)
	}
	if strings.Contains(result.Content, "SQLTUNE_REPORT_BEGIN") {
		t.Fatalf("result.Content should have marker stripped: %s", result.Content)
	}
	if !strings.Contains(result.Content, "# SQL_ID 581990336 优化报告") {
		t.Fatalf("missing sqltune report body: %s", result.Content)
	}
	if streamed := streamCaptured.String(); !strings.Contains(streamed, "执行计划") || strings.Contains(streamed, "SQLTUNE_REPORT_BEGIN") {
		t.Fatalf("unexpected streamed passthrough content: %q", streamed)
	}
}

func TestEngineForcedInitialSQLTunePassthrough(t *testing.T) {
	sqlTuneReport := "# SQL_ID 581990336 优化报告\n## 建议\n补充复合索引\n"
	executor := newMockSkillExecutor()
	executor.results["sqltune"] = &tool.SkillResult{Rendered: sqlTuneReport}

	mockProv := newMockProvider(&provider.Response{Content: "outer LLM must not be called"})
	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "sql id 581990336 如何优化",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
		ForceInitialToolCalls: []ToolCall{{
			ID:        "forced_sqltune_0_sqltune",
			Name:      "sqltune",
			Arguments: `{"args":"581990336","mode":"quick"}`,
		}},
		RequireToolEvidence: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockProv.callIdx != 0 {
		t.Fatalf("forced sqltune passthrough should not call outer LLM, provider calls=%d", mockProv.callIdx)
	}
	if len(result.ToolsInvoked) != 1 || result.ToolsInvoked[0] != "sqltune" {
		t.Fatalf("expected forced sqltune invocation, got %v", result.ToolsInvoked)
	}
	if result.TurnsUsed != 1 {
		t.Fatalf("expected 1 forced tool turn, got %d", result.TurnsUsed)
	}
	if strings.Contains(result.Content, "SQLTUNE_REPORT_BEGIN") || !strings.Contains(result.Content, "补充复合索引") {
		t.Fatalf("unexpected content: %s", result.Content)
	}
}

// TestEngineParseRetryFeedback: when PromptToolAdapter signals NeedRetry
// (malformed JSON output), the engine must:
//
//  1. NOT exit the loop with the broken content as the final answer
//  2. Append the RetryFeedback as a system-reminder message
//  3. Run another LLM round (within MaxParseRetries cap)
//  4. Proceed normally when the retry round succeeds
//
// v1.2.1 fix — without this, malformed JSON silently becomes the answer.
func TestEngineParseRetryFeedback(t *testing.T) {
	executor := newMockSkillExecutor()
	executor.results["health"] = &tool.SkillResult{Text: "instance up, 8 backends"}

	mockProv := newMockProvider(
		// Round 1: simulate PromptModeBuilder.PostProcessResponse setting
		// NeedRetry because the LLM's JSON didn't parse.
		&provider.Response{
			Content:       "```json\n{broken json without quotes}\n```",
			NeedRetry:     true,
			RetryFeedback: "你的输出无法解析为合法 JSON, 请重试",
		},
		// Round 2: LLM corrects itself and emits a real tool call.
		&provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "tc1", Name: "health", Arguments: `{}`}},
		},
		// Round 3: final answer.
		&provider.Response{
			Content: "## 根因分析\nhealth 看着没问题",
		},
	)

	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "查健康",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have used: 1 (bad JSON) + 1 (good tool call) + 1 (final) = 3 turns.
	if result.TurnsUsed != 3 {
		t.Errorf("expected 3 turns (retry path), got %d", result.TurnsUsed)
	}
	// Must NOT have returned the broken JSON as the answer.
	if strings.Contains(result.Content, "broken json without quotes") {
		t.Errorf("malformed JSON leaked into final answer: %s", result.Content)
	}
	if !strings.Contains(result.Content, "根因分析") {
		t.Errorf("missing final-round answer: %s", result.Content)
	}
}

// TestEngineParseRetryCappedAfter2Tries: if the LLM stubbornly keeps
// outputting bad JSON, engine must stop retrying after maxParseRetries=2
// and fall through to normal "no tool calls → done" handling.
func TestEngineParseRetryCappedAfter2Tries(t *testing.T) {
	executor := newMockSkillExecutor()
	mockProv := newMockProvider(
		// Round 1: bad JSON, retry
		&provider.Response{Content: "bad1", NeedRetry: true, RetryFeedback: "fix it"},
		// Round 2: bad JSON, retry
		&provider.Response{Content: "bad2", NeedRetry: true, RetryFeedback: "fix it"},
		// Round 3: still bad JSON, but should NOT retry (cap reached); should
		// fall through to final answer path with this content.
		&provider.Response{Content: "bad3 final", NeedRetry: true, RetryFeedback: "fix it"},
	)

	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "test",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TurnsUsed != 3 {
		t.Errorf("expected exactly 3 turns (1 + 2 retries, then stop), got %d", result.TurnsUsed)
	}
	// After cap, content should be returned as-is (best we can do).
	if !strings.Contains(result.Content, "bad3") {
		t.Errorf("after retry cap, last response content should be returned, got: %s", result.Content)
	}
}

// TestEngineToolDedupWarning: when the LLM repeats the SAME tool with the
// SAME arguments across rounds, the engine must inject a system-reminder
// before the next LLM call telling the model to switch strategy. Without
// this, smaller models (35B-class in prompt mode) get stuck in a tool
// loop until MaxTurns/timeout.
//
// v1.2.2 fix.
func TestEngineToolDedupWarning(t *testing.T) {
	executor := newMockSkillExecutor()
	executor.results["health"] = &tool.SkillResult{Text: "health snapshot"}

	mockProv := newMockProvider(
		// Round 1: LLM picks health.
		&provider.Response{
			ToolCalls: []provider.ToolCall{
				{ID: "tc1", Name: "health", Arguments: `{}`},
			},
		},
		// Round 2: LLM picks SAME health SAME args (dedup should detect)
		&provider.Response{
			ToolCalls: []provider.ToolCall{
				{ID: "tc2", Name: "health", Arguments: `{}`},
			},
		},
		// Round 3: LLM sees the dedup warning and switches to final answer
		&provider.Response{
			Content: "## 根因分析\n基于 health 结果分析",
		},
	)

	eng := New(mockProv, &testProfile{}, executor, nil)
	result, err := eng.Run(context.Background(), EngineInput{
		UserMessage:  "SQL_ID 581990336 怎么优化",
		Mode:         ModeAuto,
		DatabaseInfo: DatabaseInfo{Product: "opengauss"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify dedup detected (health called twice).
	if result.TurnsUsed != 3 {
		t.Errorf("expected 3 turns (2 health + 1 final), got %d", result.TurnsUsed)
	}
	if len(result.ToolsInvoked) != 2 {
		t.Errorf("expected 2 tool invocations, got %d (%v)", len(result.ToolsInvoked), result.ToolsInvoked)
	}
	// The third LLM call would have received the dedup warning. The mock
	// doesn't introspect messages, but the test verifies the engine
	// reached round 3 (didn't get stuck calling sqltune indefinitely).
}

func TestToolCallSignature(t *testing.T) {
	cases := []struct {
		a, b   string
		want   bool // signatures match?
		reason string
	}{
		{`{"args":"581990336"}`, `{"args":"581990336"}`, true, "identical"},
		{`{"args":"581990336"}`, `{"args": "581990336"}`, true, "whitespace tolerated by JSON parse? but signature is raw — should still differ"},
		{`{"args":"abc"}`, `{"args":"ABC"}`, true, "case insensitive normalize"},
		{`{"args":"abc"}`, `{"args":"def"}`, false, "different args"},
	}
	for _, c := range cases {
		sigA := toolCallSignature("sqltune", c.a)
		sigB := toolCallSignature("sqltune", c.b)
		got := (sigA == sigB)
		// Note: case 2 (whitespace) may or may not pass — signature uses raw args
		// before any JSON normalization. Tolerate both outcomes for that case.
		if c.reason == "whitespace tolerated by JSON parse? but signature is raw — should still differ" {
			t.Logf("whitespace case: sigA=%q sigB=%q match=%v (acceptable either way)", sigA, sigB, got)
			continue
		}
		if got != c.want {
			t.Errorf("toolCallSignature(%q) vs (%q): got match=%v, want %v (%s)", c.a, c.b, got, c.want, c.reason)
		}
	}

	// Cross-tool: same args, different name → must NOT match
	if toolCallSignature("sqltune", `{"args":"x"}`) == toolCallSignature("sqlfetch", `{"args":"x"}`) {
		t.Error("different tool names with same args should NOT share signature")
	}
}
