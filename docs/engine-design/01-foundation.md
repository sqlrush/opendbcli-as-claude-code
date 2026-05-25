# 子计划 1: 基础层 — Core Types + ProviderCapability + RetryPolicy

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 Engine 的基础数据结构、接口定义和重试策略，让后续模块有清晰的类型依赖。

**Architecture:** 纯数据结构 + 接口定义 + 一个独立功能模块（RetryPolicy）。不依赖任何现有 OpenDB 代码（除了 Go 标准库），可独立编译和测试。

**Tech Stack:** Go 1.26+, 标准库 only（无第三方依赖）

---

## 文件结构

```
internal/engine/
├── types.go                    // Message, ToolCall, Request, Response, Usage, StreamEvent
├── config.go                   // EngineConfig, EngineInput, EngineResult, DiagnoseMode
├── provider/
│   ├── capability.go           // ProviderCapability 及其子结构体
│   ├── adapter.go              // ProviderAdapter 接口
│   └── httperror.go            // HTTPError 类型（供 RetryPolicy 使用）
├── retry/
│   ├── policy.go               // RetryPolicy 核心逻辑
│   ├── policy_test.go          // RetryPolicy 测试
│   ├── ratelimit.go            // 限流头解析
│   └── ratelimit_test.go       // 限流头解析测试
└── provider/
    └── capability_test.go      // Capability 构造测试
```

---

### Task 0: 项目初始化

**Files:**
- Create: `internal/engine/` 目录结构

- [ ] **Step 1: 创建开发分支**

```bash
cd ~/opendb
git checkout -b feature/engine-v2
```

- [ ] **Step 2: 确认代码可编译**

Run: `cd ~/opendb && go build ./...`
Expected: 编译成功，无错误

- [ ] **Step 3: 确认现有测试通过**

Run: `cd ~/opendb && go test ./internal/llm/... ./internal/oracle/agent/... -v -count=1 2>&1 | tail -20`
Expected: 现有测试 PASS（如有失败先记录，不影响新开发）

- [ ] **Step 4: 创建 engine 目录结构**

```bash
mkdir -p ~/opendb/internal/engine/provider
mkdir -p ~/opendb/internal/engine/retry
mkdir -p ~/opendb/internal/engine/context
mkdir -p ~/opendb/internal/engine/tool
mkdir -p ~/opendb/internal/engine/profile
mkdir -p ~/opendb/internal/engine/bridge
```

- [ ] **Step 5: Commit**

```bash
cd ~/opendb
git add -A
git commit -m "chore: create engine directory structure for LLM communication v2"
```

---

### Task 1: Core Types — Message + ToolCall

**Files:**
- Create: `internal/engine/types.go`
- Test: `internal/engine/types_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/types_test.go
package engine

import "testing"

func TestMessageIsMeta(t *testing.T) {
	msg := Message{
		Role:    "user",
		Content: "<system-reminder>test</system-reminder>",
		IsMeta:  true,
	}
	if !msg.IsMeta {
		t.Error("expected IsMeta to be true")
	}
	if msg.Role != "user" {
		t.Errorf("expected role 'user', got %q", msg.Role)
	}
}

func TestToolCallFields(t *testing.T) {
	tc := ToolCall{
		ID:        "call_123",
		Name:      "waits",
		Arguments: `{"args":""}`,
	}
	if tc.ID != "call_123" {
		t.Errorf("expected ID 'call_123', got %q", tc.ID)
	}
	if tc.Name != "waits" {
		t.Errorf("expected Name 'waits', got %q", tc.Name)
	}
}

func TestThinkingBlockFields(t *testing.T) {
	tb := ThinkingBlock{
		Type:      "thinking",
		Thinking:  "let me analyze...",
		Signature: "sig_abc",
	}
	if tb.Signature != "sig_abc" {
		t.Errorf("expected Signature 'sig_abc', got %q", tb.Signature)
	}
}

func TestCacheControlDefaults(t *testing.T) {
	cc := CacheControl{Type: "ephemeral"}
	if cc.Type != "ephemeral" {
		t.Errorf("expected Type 'ephemeral', got %q", cc.Type)
	}
	if cc.TTL != "" {
		t.Errorf("expected empty TTL, got %q", cc.TTL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/opendb && go test ./internal/engine/ -v -run TestMessage`
Expected: FAIL — package engine not found / types not defined

- [ ] **Step 3: Write minimal implementation**

```go
// internal/engine/types.go
package engine

// Message represents a conversation message sent to or received from an LLM.
type Message struct {
	Role    string `json:"role"`              // system / user / assistant / tool
	Content string `json:"content,omitempty"`

	// Tool calling
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`

	// Thinking/reasoning content (unified across all providers)
	Thinking       string          `json:"thinking,omitempty"`
	ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitempty"` // Anthropic structured

	// Meta flag — model sees it, user doesn't (like Claude Code's isMeta)
	IsMeta bool `json:"is_meta,omitempty"`

	// Cache control (Anthropic explicit caching)
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ThinkingBlock represents a structured thinking block (Anthropic format).
type ThinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

// CacheControl marks content for prompt caching.
type CacheControl struct {
	Type string `json:"type"`            // "ephemeral"
	TTL  string `json:"ttl,omitempty"`   // "1h" or empty (default 5min)
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSchema defines a tool's API description for the LLM.
type ToolSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// SystemPromptBlock is one block of the system prompt, with optional caching.
type SystemPromptBlock struct {
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ~/opendb && go test ./internal/engine/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/types.go internal/engine/types_test.go
git commit -m "feat(engine): add core Message, ToolCall, and related types"
```

---

### Task 2: Core Types — Request + Response + Usage

**Files:**
- Modify: `internal/engine/types.go`
- Test: `internal/engine/types_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
// append to internal/engine/types_test.go

func TestUsageTotalInputCost(t *testing.T) {
	u := Usage{
		InputTokens:         1000,
		CacheCreationTokens: 500,
		CacheReadTokens:     2000,
	}
	// CacheRead at 0.1x = 200, total = 1000 + 500 + 200 = 1700
	cost := u.TotalInputCost()
	if cost != 1700 {
		t.Errorf("expected TotalInputCost 1700, got %d", cost)
	}
}

func TestUsageAdd(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 50}
	b := Usage{InputTokens: 200, OutputTokens: 80, CacheReadTokens: 300}
	sum := a.Add(b)
	if sum.InputTokens != 300 {
		t.Errorf("expected InputTokens 300, got %d", sum.InputTokens)
	}
	if sum.OutputTokens != 130 {
		t.Errorf("expected OutputTokens 130, got %d", sum.OutputTokens)
	}
	if sum.CacheReadTokens != 300 {
		t.Errorf("expected CacheReadTokens 300, got %d", sum.CacheReadTokens)
	}
	// Verify original not mutated (immutability)
	if a.InputTokens != 100 {
		t.Error("original Usage was mutated")
	}
}

func TestResponseTruncatedDetection(t *testing.T) {
	resp := Response{
		Content:    "partial output...",
		StopReason: "length",
		Truncated:  true,
	}
	if !resp.Truncated {
		t.Error("expected Truncated to be true")
	}
}

func TestStreamEventTypes(t *testing.T) {
	if StreamTextDelta != 0 {
		t.Error("StreamTextDelta should be 0")
	}
	if StreamDone != 4 {
		t.Errorf("StreamDone should be 4, got %d", StreamDone)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/opendb && go test ./internal/engine/ -v -run "TestUsage|TestResponse|TestStream"`
Expected: FAIL — Usage, Response types not defined

- [ ] **Step 3: Write minimal implementation**

```go
// append to internal/engine/types.go

// ═══════════════════════════════════════════════════════
// Request — API request
// ═══════════════════════════════════════════════════════

// Request holds all parameters for an LLM API call.
type Request struct {
	Messages     []Message
	Tools        []ToolSchema
	SystemPrompt []SystemPromptBlock
	MaxTokens    int
	Temperature  *float64
	Stream       bool
	Extra        map[string]any // Provider-specific params (filled by EnhanceRequest)
}

// ═══════════════════════════════════════════════════════
// Response — API response
// ═══════════════════════════════════════════════════════

// Response holds the parsed result of an LLM API call.
type Response struct {
	Content    string
	Thinking   string
	ToolCalls  []ToolCall
	StopReason string
	Usage      Usage
	CacheStats CacheStats
	Truncated  bool
	RawHeaders map[string]string
}

// ═══════════════════════════════════════════════════════
// Usage — extended token usage tracking
// ═══════════════════════════════════════════════════════

// Usage tracks token consumption across all providers.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	ThinkingTokens      int
	CacheCreationTokens int
	CacheReadTokens     int
	CacheMissTokens     int
}

// TotalInputCost returns the effective input token cost.
// Cache reads are typically billed at 0.1x.
func (u Usage) TotalInputCost() int {
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens/10
}

// Add returns a new Usage with all fields summed. Does not mutate the receiver.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:         u.InputTokens + other.InputTokens,
		OutputTokens:        u.OutputTokens + other.OutputTokens,
		ThinkingTokens:      u.ThinkingTokens + other.ThinkingTokens,
		CacheCreationTokens: u.CacheCreationTokens + other.CacheCreationTokens,
		CacheReadTokens:     u.CacheReadTokens + other.CacheReadTokens,
		CacheMissTokens:     u.CacheMissTokens + other.CacheMissTokens,
	}
}

// CacheStats summarizes prompt cache performance.
type CacheStats struct {
	Hit        bool
	HitTokens  int
	MissTokens int
}

// ═══════════════════════════════════════════════════════
// Stream events
// ═══════════════════════════════════════════════════════

// StreamEventType classifies streaming events from the LLM.
type StreamEventType uint8

const (
	StreamTextDelta     StreamEventType = iota // Incremental text content
	StreamThinkingDelta                        // Thinking content delta
	StreamToolCallDelta                        // Partial tool call
	StreamToolResult                           // Tool execution result
	StreamDone                                 // Stream completed
	StreamError                                // Non-fatal stream error
)

// StreamEvent is a single event from an LLM streaming response.
type StreamEvent struct {
	Type     StreamEventType
	Content  string
	ToolCall *ToolCall
	Error    error
}

// Stream provides incremental access to an LLM streaming response.
type Stream interface {
	Next() (StreamEvent, error)
	Close() error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ~/opendb && go test ./internal/engine/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/types.go internal/engine/types_test.go
git commit -m "feat(engine): add Request, Response, Usage, StreamEvent types"
```

---

### Task 3: Engine Config — EngineInput + EngineResult + DiagnoseMode

**Files:**
- Create: `internal/engine/config.go`
- Test: `internal/engine/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/config_test.go
package engine

import "testing"

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultMaxTurns != 20 {
		t.Errorf("expected DefaultMaxTurns 20, got %d", cfg.DefaultMaxTurns)
	}
	if cfg.DefaultMaxTokens != 8000 {
		t.Errorf("expected DefaultMaxTokens 8000, got %d", cfg.DefaultMaxTokens)
	}
	if !cfg.EnableCompression {
		t.Error("expected EnableCompression true")
	}
	if cfg.MaxOutputRecoveries != 2 {
		t.Errorf("expected MaxOutputRecoveries 2, got %d", cfg.MaxOutputRecoveries)
	}
}

func TestDiagnoseModeIsValid(t *testing.T) {
	tests := []struct {
		mode  DiagnoseMode
		valid bool
	}{
		{ModePlaybook, true},
		{ModeAssist, true},
		{ModeAuto, true},
		{DiagnoseMode("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.mode.IsValid(); got != tt.valid {
			t.Errorf("DiagnoseMode(%q).IsValid() = %v, want %v", tt.mode, got, tt.valid)
		}
	}
}

func TestEngineResultToolsInvoked(t *testing.T) {
	r := EngineResult{
		Content:      "diagnosis...",
		TurnsUsed:    3,
		ToolsInvoked: []string{"waits", "topsql", "explain"},
	}
	if len(r.ToolsInvoked) != 3 {
		t.Errorf("expected 3 tools, got %d", len(r.ToolsInvoked))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/opendb && go test ./internal/engine/ -v -run "TestDefault|TestDiagnose|TestEngineResult"`
Expected: FAIL — DefaultConfig, DiagnoseMode not defined

- [ ] **Step 3: Write minimal implementation**

```go
// internal/engine/config.go
package engine

// EngineConfig holds configuration for the Engine.
type EngineConfig struct {
	DefaultMaxTurns     int
	DefaultMaxTokens    int
	ThinkingLevel       string // "low" / "medium" / "high"
	StreamFinalOnly     bool
	EnablePreExecution  bool
	EnableCompression   bool
	MaxOutputRecoveries int
}

// DefaultConfig returns sensible defaults for the Engine.
func DefaultConfig() EngineConfig {
	return EngineConfig{
		DefaultMaxTurns:     20,
		DefaultMaxTokens:    8000,
		ThinkingLevel:       "high",
		StreamFinalOnly:     false,
		EnablePreExecution:  true,
		EnableCompression:   true,
		MaxOutputRecoveries: 2,
	}
}

// DiagnoseMode represents the three diagnosis modes.
type DiagnoseMode string

const (
	ModePlaybook DiagnoseMode = "playbook"
	ModeAssist   DiagnoseMode = "assist"
	ModeAuto     DiagnoseMode = "auto"
)

// IsValid returns true if the mode is recognized.
func (m DiagnoseMode) IsValid() bool {
	switch m {
	case ModePlaybook, ModeAssist, ModeAuto:
		return true
	default:
		return false
	}
}

// EngineInput is the input to Engine.Run().
type EngineInput struct {
	UserMessage      string
	CompressedReport string
	DatabaseInfo     DatabaseInfo
	Metadata         map[string]string
	Mode             DiagnoseMode
	MaxTurns         int
	OnRound          func(turn int, toolNames []string)
	OnStream         func(delta string)
}

// DatabaseInfo describes the connected database.
type DatabaseInfo struct {
	Product  string // oracle / mysql / postgres / opengauss
	Version  string
	Instance string
	Host     string
}

// EngineResult is the output of Engine.Run().
type EngineResult struct {
	Content      string
	Thinking     string
	TotalUsage   Usage
	TurnsUsed    int
	MaxTurnsHit  bool
	ToolsInvoked []string
	Errors       []TurnError
}

// TurnError records an error that occurred during a specific turn.
type TurnError struct {
	Turn  int
	Tool  string
	Error string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ~/opendb && go test ./internal/engine/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/config.go internal/engine/config_test.go
git commit -m "feat(engine): add EngineConfig, DiagnoseMode, EngineInput, EngineResult"
```

---

### Task 4: ProviderCapability — 能力声明结构体

**Files:**
- Create: `internal/engine/provider/capability.go`
- Test: `internal/engine/provider/capability_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/provider/capability_test.go
package provider

import "testing"

func TestThinkingModeConstants(t *testing.T) {
	if ThinkingNone != 0 {
		t.Error("ThinkingNone should be 0")
	}
	if ThinkingAdaptive != 1 {
		t.Errorf("ThinkingAdaptive should be 1, got %d", ThinkingAdaptive)
	}
}

func TestCachingModeConstants(t *testing.T) {
	if CachingNone != 0 {
		t.Error("CachingNone should be 0")
	}
	if CachingExplicit != 1 {
		t.Errorf("CachingExplicit should be 1, got %d", CachingExplicit)
	}
}

func TestAnthropicCapability(t *testing.T) {
	// Verify a realistic Anthropic capability can be constructed
	cap := ProviderCapability{
		Name:             "anthropic",
		MaxContextWindow: 1_000_000,
		MaxOutputTokens:  128_000,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAdaptive,
			MultiTurnPolicy: ThinkingPreserveAll,
			ExtractField:    "thinking_blocks",
		},
		Caching: CachingCapability{
			Mode:             CachingExplicit,
			MaxBreakpoints:   4,
			MinCacheTokens:   1024,
			CacheReadField:   "cache_read_input_tokens",
			CacheCreateField: "cache_creation_input_tokens",
		},
		ToolCalling: ToolCallingCapability{
			Supported:      true,
			Format:         ToolFormatAnthropicNative,
			SupportsStrict: true,
		},
		RateLimit: RateLimitCapability{
			HeaderPrefix:  "anthropic-ratelimit",
			HasRetryAfter: true,
			OverloadCode:  529,
		},
		Output: OutputCapability{
			SupportsEffort:     true,
			EffortLevels:       []string{"low", "medium", "high", "max"},
			SupportsSpeed:      true,
			SupportsTaskBudget: true,
		},
	}

	if cap.MaxContextWindow != 1_000_000 {
		t.Errorf("expected 1M context, got %d", cap.MaxContextWindow)
	}
	if !cap.Thinking.Supported {
		t.Error("expected thinking supported")
	}
	if cap.Caching.Mode != CachingExplicit {
		t.Errorf("expected explicit caching, got %d", cap.Caching.Mode)
	}
	if cap.RateLimit.OverloadCode != 529 {
		t.Errorf("expected overload code 529, got %d", cap.RateLimit.OverloadCode)
	}
}

func TestOllamaCapability(t *testing.T) {
	cap := ProviderCapability{
		Name:             "ollama",
		MaxContextWindow: 32_768,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAutoTags,
			MultiTurnPolicy: ThinkingStripAll,
		},
		ToolCalling: ToolCallingCapability{
			Supported:    true,
			Format:       ToolFormatOpenAICompatible,
			TextFallback: true,
		},
		RateLimit: RateLimitCapability{
			IsLocal: true,
		},
	}

	if !cap.RateLimit.IsLocal {
		t.Error("expected IsLocal true for Ollama")
	}
	if !cap.ToolCalling.TextFallback {
		t.Error("expected TextFallback true for Ollama")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/opendb && go test ./internal/engine/provider/ -v`
Expected: FAIL — package / types not defined

- [ ] **Step 3: Write minimal implementation**

```go
// internal/engine/provider/capability.go
package provider

// ProviderCapability declares what a provider supports.
// Engine code checks these fields instead of hardcoding vendor names.
type ProviderCapability struct {
	Name             string
	MaxContextWindow int
	MaxOutputTokens  int

	Thinking    ThinkingCapability
	Caching     CachingCapability
	ToolCalling ToolCallingCapability
	RateLimit   RateLimitCapability
	Output      OutputCapability
}

// ── Thinking ──

// ThinkingMode describes how a provider enables thinking/reasoning.
type ThinkingMode int

const (
	ThinkingNone       ThinkingMode = iota // Not supported
	ThinkingAdaptive                       // Anthropic: model decides
	ThinkingEffortLevel                    // OpenAI: reasoning.effort
	ThinkingBudget                         // Gemini: token budget
	ThinkingEnableFlag                     // Qwen/MiMo/Kimi/GLM: bool flag
	ThinkingAutoTags                       // DeepSeek/Ollama: <think> in output
	ThinkingSplit                          // MiniMax: reasoning_split
)

// ThinkingMultiTurnPolicy controls how thinking content is handled across turns.
type ThinkingMultiTurnPolicy int

const (
	ThinkingPreserveAll      ThinkingMultiTurnPolicy = iota // Anthropic/OpenAI: keep all
	ThinkingStripBetweenTurns                               // DeepSeek/Kimi/Qwen: strip on new user turn
	ThinkingStripAll                                        // Local models: strip everything
)

// ThinkingCapability describes thinking/reasoning support.
type ThinkingCapability struct {
	Supported       bool
	Mode            ThinkingMode
	MultiTurnPolicy ThinkingMultiTurnPolicy
	ExtractField    string                         // "thinking_blocks" / "reasoning_content" / "reasoning_details" / ""
	EnableParams    func(level string) map[string]any // Returns provider-specific params
}

// ── Caching ──

// CachingMode describes how prompt caching works.
type CachingMode int

const (
	CachingNone        CachingMode = iota // No caching
	CachingExplicit                        // Anthropic: client marks breakpoints
	CachingAutomatic                       // DeepSeek/OpenAI/GLM: server-side
	CachingSeparateAPI                     // Gemini: separate caches.create() API
)

// CachingCapability describes prompt caching support.
type CachingCapability struct {
	Mode             CachingMode
	MaxBreakpoints   int
	MinCacheTokens   int
	CacheReadField   string // Response usage field for cache hits
	CacheMissField   string // Response usage field for cache misses
	CacheCreateField string // Response usage field for cache creation
}

// ── Tool Calling ──

// ToolCallFormat describes the wire format for tool definitions and calls.
type ToolCallFormat int

const (
	ToolFormatOpenAICompatible ToolCallFormat = iota // Most providers
	ToolFormatAnthropicNative                         // Anthropic Messages API
	ToolFormatGeminiNative                            // Gemini generateContent
	ToolFormatTextSimulation                          // Weak models: ```action blocks
)

// ToolCallingCapability describes tool/function calling support.
type ToolCallingCapability struct {
	Supported           bool
	Format              ToolCallFormat
	SupportsParallel    bool
	SupportsStrict      bool
	TextFallback        bool     // Fall back to text simulation if native fails
	ThinkingConstraints []string // Constraints when thinking is active
}

// ── Rate Limiting ──

// RateLimitCapability describes rate limiting behavior.
type RateLimitCapability struct {
	HeaderPrefix  string // "anthropic-ratelimit" / "x-ratelimit" / ""
	HasRetryAfter bool
	OverloadCode  int  // 529 for Anthropic, 0 for others
	IsLocal       bool // Local deployments don't retry network errors
}

// ── Output Control ──

// OutputCapability describes output tuning features.
type OutputCapability struct {
	SupportsEffort           bool
	EffortLevels             []string
	SupportsSpeed            bool
	SupportsTaskBudget       bool
	SupportsStructuredOutput bool
	SupportsPredictedOutput  bool
	SupportsSeed             bool
	FixedTemperature         bool // Some models don't allow temperature changes
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ~/opendb && go test ./internal/engine/provider/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/provider/capability.go internal/engine/provider/capability_test.go
git commit -m "feat(engine): add ProviderCapability with Thinking/Caching/ToolCalling/RateLimit/Output"
```

---

### Task 5: ProviderAdapter 接口 + HTTPError

**Files:**
- Create: `internal/engine/provider/adapter.go`
- Create: `internal/engine/provider/httperror.go`
- Test: `internal/engine/provider/httperror_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/provider/httperror_test.go
package provider

import (
	"errors"
	"net/http"
	"testing"
)

func TestHTTPErrorMessage(t *testing.T) {
	err := &HTTPError{
		StatusCode: 429,
		Body:       "rate limit exceeded",
		Headers:    http.Header{"Retry-After": {"5"}},
	}

	msg := err.Error()
	if msg != "HTTP 429: rate limit exceeded" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestHTTPErrorIs(t *testing.T) {
	err := &HTTPError{StatusCode: 529, Body: "overloaded"}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Error("expected errors.As to match HTTPError")
	}
	if httpErr.StatusCode != 529 {
		t.Errorf("expected status 529, got %d", httpErr.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/opendb && go test ./internal/engine/provider/ -v -run TestHTTP`
Expected: FAIL — HTTPError not defined

- [ ] **Step 3: Write minimal implementation**

```go
// internal/engine/provider/httperror.go
package provider

import (
	"fmt"
	"net/http"
)

// HTTPError carries HTTP status, headers, and body for retry classification.
type HTTPError struct {
	StatusCode int
	Headers    http.Header
	Body       string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}
```

```go
// internal/engine/provider/adapter.go
package provider

import (
	"context"
	"net/http"

	engine "github.com/sqlrush/opendb/internal/engine"
)

// ProviderAdapter is the unified interface for all LLM providers.
// Engine calls these methods without knowing the underlying vendor.
type ProviderAdapter interface {
	// Chat sends a non-streaming request.
	Chat(ctx context.Context, req *engine.Request) (*engine.Response, error)

	// ChatStream sends a streaming request.
	ChatStream(ctx context.Context, req *engine.Request) (engine.Stream, error)

	// Capability returns the provider's capability declaration.
	Capability() *ProviderCapability

	// EnhanceRequest injects provider-specific parameters into req.Extra.
	EnhanceRequest(req *engine.Request)

	// ParseRateLimitHeaders extracts rate limit info from response headers.
	ParseRateLimitHeaders(headers http.Header) *RateLimitInfo

	// Name returns the provider identifier.
	Name() string
}

// RateLimitInfo holds parsed rate limit data from response headers.
type RateLimitInfo struct {
	RetryAfterSeconds float64
	RemainingRequests int
	RemainingTokens   int
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ~/opendb && go test ./internal/engine/provider/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/provider/adapter.go internal/engine/provider/httperror.go internal/engine/provider/httperror_test.go
git commit -m "feat(engine): add ProviderAdapter interface and HTTPError type"
```

---

### Task 6: RetryPolicy — 核心重试逻辑

**Files:**
- Create: `internal/engine/retry/policy.go`
- Test: `internal/engine/retry/policy_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/retry/policy_test.go
package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/engine"
	"github.com/sqlrush/opendb/internal/engine/provider"
)

func TestExecuteSuccess(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond}, &provider.RateLimitCapability{})

	calls := 0
	resp, err := p.Execute(context.Background(), func(ctx context.Context) (*engine.Response, error) {
		calls++
		return &engine.Response{Content: "ok"}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got %q", resp.Content)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestExecuteRetryOn429(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond}, &provider.RateLimitCapability{HasRetryAfter: true})

	calls := 0
	resp, err := p.Execute(context.Background(), func(ctx context.Context) (*engine.Response, error) {
		calls++
		if calls < 3 {
			return nil, &provider.HTTPError{StatusCode: 429, Headers: http.Header{}, Body: "rate limited"}
		}
		return &engine.Response{Content: "ok"}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got %q", resp.Content)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestExecuteNoRetryOn400(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond}, &provider.RateLimitCapability{})

	calls := 0
	_, err := p.Execute(context.Background(), func(ctx context.Context) (*engine.Response, error) {
		calls++
		return nil, &provider.HTTPError{StatusCode: 400, Body: "bad request"}
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry on 400), got %d", calls)
	}
}

func TestExecuteMaxRetriesExceeded(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 2, BaseDelay: 10 * time.Millisecond, MaxDelay: 20 * time.Millisecond}, &provider.RateLimitCapability{})

	calls := 0
	_, err := p.Execute(context.Background(), func(ctx context.Context) (*engine.Response, error) {
		calls++
		return nil, &provider.HTTPError{StatusCode: 500, Body: "server error"}
	})

	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestExecuteContextCanceled(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 5, BaseDelay: 1 * time.Second}, &provider.RateLimitCapability{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := p.Execute(ctx, func(ctx context.Context) (*engine.Response, error) {
		return nil, &provider.HTTPError{StatusCode: 500, Body: "error"}
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestClassifyError429(t *testing.T) {
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{HasRetryAfter: true})
	info := p.ClassifyError(&provider.HTTPError{StatusCode: 429})
	if !info.Retryable {
		t.Error("429 should be retryable")
	}
	if !info.IsRateLimit {
		t.Error("429 should be rate limit")
	}
}

func TestClassifyError529(t *testing.T) {
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{OverloadCode: 529})
	info := p.ClassifyError(&provider.HTTPError{StatusCode: 529})
	if !info.IsOverload {
		t.Error("529 should be overload when OverloadCode=529")
	}
}

func TestClassifyErrorLocal(t *testing.T) {
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{IsLocal: true})
	info := p.ClassifyError(errors.New("connection refused"))
	if info.Retryable {
		t.Error("network errors should not be retryable for local providers")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/opendb && go test ./internal/engine/retry/ -v`
Expected: FAIL — package retry not found

- [ ] **Step 3: Write minimal implementation**

```go
// internal/engine/retry/policy.go
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/sqlrush/opendb/internal/engine"
	"github.com/sqlrush/opendb/internal/engine/provider"
)

// Config configures retry behavior.
type Config struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultConfig returns sensible retry defaults.
func DefaultConfig() Config {
	return Config{
		MaxRetries: 5,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   30 * time.Second,
	}
}

// Policy implements exponential backoff with error classification.
type Policy struct {
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	capability *provider.RateLimitCapability
}

// NewPolicy creates a retry policy.
func NewPolicy(cfg Config, cap *provider.RateLimitCapability) *Policy {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 500 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	return &Policy{
		maxRetries: cfg.MaxRetries,
		baseDelay:  cfg.BaseDelay,
		maxDelay:   cfg.MaxDelay,
		capability: cap,
	}
}

// RetryInfo describes how an error should be handled.
type RetryInfo struct {
	Retryable        bool
	RetryAfter       time.Duration
	IsRateLimit      bool
	IsOverload       bool
	IsServerError    bool
	IsNetworkError   bool
	IsContextTooLong bool
	IsOutputTruncated bool
}

// Execute wraps an API call with retry logic.
func (p *Policy) Execute(
	ctx context.Context,
	fn func(ctx context.Context) (*engine.Response, error),
) (*engine.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}

		info := p.ClassifyError(err)
		if !info.Retryable {
			return nil, err
		}

		lastErr = err

		if attempt == p.maxRetries {
			break
		}

		delay := p.calculateDelay(attempt, info)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", p.maxRetries, lastErr)
}

// ClassifyError determines how an error should be handled.
func (p *Policy) ClassifyError(err error) RetryInfo {
	info := RetryInfo{}

	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		if p.capability != nil && p.capability.IsLocal {
			return info // Local: don't retry network errors
		}
		info.Retryable = true
		info.IsNetworkError = true
		return info
	}

	switch httpErr.StatusCode {
	case 429:
		info.Retryable = true
		info.IsRateLimit = true
		info.RetryAfter = parseRetryAfter(httpErr.Headers)

	case 413:
		info.IsContextTooLong = true
		// Not retryable here — Engine handles compression + retry

	case 408, 409:
		info.Retryable = true

	default:
		if p.capability != nil && httpErr.StatusCode == p.capability.OverloadCode {
			info.Retryable = true
			info.IsOverload = true
			info.RetryAfter = parseRetryAfter(httpErr.Headers)
		} else if httpErr.StatusCode >= 500 {
			info.Retryable = true
			info.IsServerError = true
		}
	}

	return info
}

func (p *Policy) calculateDelay(attempt int, info RetryInfo) time.Duration {
	if info.RetryAfter > 0 {
		return info.RetryAfter
	}

	delay := p.baseDelay * time.Duration(1<<uint(attempt))
	if delay > p.maxDelay {
		delay = p.maxDelay
	}

	// Add 0-25% jitter
	jitter := time.Duration(rand.Int63n(int64(delay) / 4))
	delay += jitter

	return delay
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ~/opendb && go test ./internal/engine/retry/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/retry/policy.go internal/engine/retry/policy_test.go
git commit -m "feat(engine): add RetryPolicy with exponential backoff and error classification"
```

---

### Task 7: Rate Limit Header Parsing

**Files:**
- Create: `internal/engine/retry/ratelimit.go`
- Test: `internal/engine/retry/ratelimit_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/retry/ratelimit_test.go
package retry

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterSeconds(t *testing.T) {
	headers := http.Header{"Retry-After": {"5"}}
	d := parseRetryAfter(headers)
	if d != 5*time.Second {
		t.Errorf("expected 5s, got %v", d)
	}
}

func TestParseRetryAfterFloat(t *testing.T) {
	headers := http.Header{"Retry-After": {"2.5"}}
	d := parseRetryAfter(headers)
	if d != 2500*time.Millisecond {
		t.Errorf("expected 2.5s, got %v", d)
	}
}

func TestParseRetryAfterMissing(t *testing.T) {
	headers := http.Header{}
	d := parseRetryAfter(headers)
	if d != 0 {
		t.Errorf("expected 0, got %v", d)
	}
}

func TestParseRateLimitInfoAnthropic(t *testing.T) {
	headers := http.Header{
		"Anthropic-Ratelimit-Requests-Remaining": {"10"},
		"Anthropic-Ratelimit-Tokens-Remaining":   {"50000"},
	}
	info := ParseRateLimitInfo(headers, "anthropic-ratelimit")
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.RemainingRequests != 10 {
		t.Errorf("expected 10 remaining requests, got %d", info.RemainingRequests)
	}
	if info.RemainingTokens != 50000 {
		t.Errorf("expected 50000 remaining tokens, got %d", info.RemainingTokens)
	}
}

func TestParseRateLimitInfoOpenAI(t *testing.T) {
	headers := http.Header{
		"X-Ratelimit-Remaining-Requests": {"20"},
		"X-Ratelimit-Remaining-Tokens":   {"100000"},
	}
	info := ParseRateLimitInfo(headers, "x-ratelimit")
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.RemainingRequests != 20 {
		t.Errorf("expected 20, got %d", info.RemainingRequests)
	}
}

func TestParseRateLimitInfoNoPrefix(t *testing.T) {
	info := ParseRateLimitInfo(http.Header{}, "")
	if info != nil {
		t.Error("expected nil for empty prefix")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/opendb && go test ./internal/engine/retry/ -v -run "TestParse"`
Expected: FAIL — parseRetryAfter, ParseRateLimitInfo not defined

- [ ] **Step 3: Write minimal implementation**

```go
// internal/engine/retry/ratelimit.go
package retry

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// parseRetryAfter extracts the Retry-After duration from response headers.
func parseRetryAfter(headers http.Header) time.Duration {
	v := headers.Get("Retry-After")
	if v == "" {
		return 0
	}

	if seconds, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(seconds * float64(time.Second))
	}

	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}

	return 0
}

// ParseRateLimitInfo extracts rate limit info from provider-specific headers.
// Returns nil if prefix is empty.
func ParseRateLimitInfo(headers http.Header, prefix string) *provider.RateLimitInfo {
	if prefix == "" {
		return nil
	}

	info := &provider.RateLimitInfo{}

	// Try both header naming conventions:
	// Anthropic: {prefix}-requests-remaining
	// OpenAI:    {prefix}-remaining-requests
	remaining := headers.Get(prefix + "-requests-remaining")
	if remaining == "" {
		remaining = headers.Get(prefix + "-remaining-requests")
	}
	if v, err := strconv.Atoi(remaining); err == nil {
		info.RemainingRequests = v
	}

	tokenRemaining := headers.Get(prefix + "-tokens-remaining")
	if tokenRemaining == "" {
		tokenRemaining = headers.Get(prefix + "-remaining-tokens")
	}
	if v, err := strconv.Atoi(tokenRemaining); err == nil {
		info.RemainingTokens = v
	}

	_ = strings.TrimSpace // avoid unused import if needed

	return info
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ~/opendb && go test ./internal/engine/retry/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/retry/ratelimit.go internal/engine/retry/ratelimit_test.go
git commit -m "feat(engine): add rate limit header parsing for Anthropic and OpenAI"
```

---

### Task 8: 验证全部编译通过

- [ ] **Step 1: Run full build**

Run: `cd ~/opendb && go build ./...`
Expected: 编译成功

- [ ] **Step 2: Run all new tests**

Run: `cd ~/opendb && go test ./internal/engine/... -v -race -count=1`
Expected: ALL PASS, no race conditions

- [ ] **Step 3: Run linter**

Run: `cd ~/opendb && go vet ./internal/engine/...`
Expected: 无 vet 错误

- [ ] **Step 4: Commit with tag**

```bash
git add -A
git commit -m "feat(engine): complete foundation layer — types, capability, retry policy

Sub-plan 1 complete:
- Core types: Message, Request, Response, Usage, StreamEvent
- EngineConfig, EngineInput, EngineResult, DiagnoseMode
- ProviderCapability: Thinking/Caching/ToolCalling/RateLimit/Output
- ProviderAdapter interface
- RetryPolicy: exponential backoff, error classification, rate limit parsing
- HTTPError type for retry classification

All tests pass. Ready for sub-plan 2 (tool layer)."
```

---

## 子计划 1 完成后的状态

```
internal/engine/
├── types.go                    ✅ Message, ToolCall, Request, Response, Usage, Stream
├── types_test.go               ✅
├── config.go                   ✅ EngineConfig, EngineInput, EngineResult, DiagnoseMode
├── config_test.go              ✅
├── provider/
│   ├── capability.go           ✅ ProviderCapability (all sub-structs)
│   ├── capability_test.go      ✅
│   ├── adapter.go              ✅ ProviderAdapter interface
│   ├── httperror.go            ✅ HTTPError
│   └── httperror_test.go       ✅
└── retry/
    ├── policy.go               ✅ RetryPolicy (Execute, ClassifyError, backoff)
    ├── policy_test.go          ✅
    ├── ratelimit.go            ✅ parseRetryAfter, ParseRateLimitInfo
    └── ratelimit_test.go       ✅

Total: ~600 行代码 + ~400 行测试
测试覆盖: types, config, capability, retry, ratelimit
```

下一步: 子计划 2（工具层 — ToolOrchestrator + ResultHandler）
