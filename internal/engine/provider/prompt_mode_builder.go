/*-------------------------------------------------------------------------
 *
 * prompt_mode_builder.go
 *	  v1.2.0 PromptModeBuilder — the prompt-based tool-calling adapter for
 *	  LLMs/deployments that don't expose native function calling.
 *
 *	  Strategy:
 *	    1. BuildSystemPrompt: append filtered tool descriptions + Format
 *	       A/B rules + few-shot examples to the engine's base prompt
 *	    2. PrepareRequest: clear req.Tools so the API call doesn't carry
 *	       tools both ways (some endpoints reject this)
 *	    3. PostProcessResponse: parse JSON tool_calls out of resp.Content,
 *	       populating resp.ToolCalls so the Engine sees the same structure
 *	       as native FC. Format B passes through unchanged.
 *
 *	  Few-shot examples in this file are intentionally curated for the
 *	  opendb diagnostic use cases (sqltune / health / wdranalyze /
 *	  multi-tool diagnostic flow). v1.2.x may swap them per-customer.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/prompt_mode_builder.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"fmt"
	"strings"
)

// PromptModeBuilder injects tool descriptions into the system prompt and
// parses JSON tool_calls back from the LLM response.
type PromptModeBuilder struct {
	parser     *JSONToolCallParser
	filter     ToolFilterFunc
	serializer ToolSerializerFunc
	// turnCtx is the per-turn FilterContext. Updated by the engine before
	// each LLM call via SetTurnContext (which the openaicompat wrapper
	// invokes). Default empty context = no filtering, fallback to all tools.
	turnCtx FilterContext
}

// ToolSerializerFunc renders a tool list as compact Markdown for prompt
// injection. tool.SerializeToolsCompact satisfies this.
type ToolSerializerFunc func(tools []ToolSchema) string

// PromptModeOption configures PromptModeBuilder construction.
type PromptModeOption func(*PromptModeBuilder)

// WithToolFilter sets the per-turn tool filter (typically
// tool.SceneBasedFilter.Filter). Pass nil or omit to skip filtering and
// include all tools in every prompt.
func WithToolFilter(f ToolFilterFunc) PromptModeOption {
	return func(b *PromptModeBuilder) { b.filter = f }
}

// WithToolSerializer sets the compact tool serializer
// (typically tool.SerializeToolsCompact). Required for prompt mode to
// work — without it tool descriptions are missing from the prompt.
func WithToolSerializer(s ToolSerializerFunc) PromptModeOption {
	return func(b *PromptModeBuilder) { b.serializer = s }
}

// NewPromptModeBuilder constructs the prompt-mode adapter. Pass
// knownToolNames to enable fuzzy correction of LLM-output tool names
// (e.g., "heath" → "health"); pass nil to disable correction.
func NewPromptModeBuilder(knownToolNames []string, opts ...PromptModeOption) *PromptModeBuilder {
	b := &PromptModeBuilder{
		parser: NewJSONToolCallParser(knownToolNames),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// SetTurnContext stores the per-turn context (user message, previous tool
// calls). The engine should call this before each LLM round so the next
// BuildSystemPrompt call can filter tools appropriately. Safe to call
// concurrently with respect to other turns (no shared mutation), but the
// builder is single-engine: don't share one builder across concurrent
// Engine.Run invocations.
func (b *PromptModeBuilder) SetTurnContext(ctx FilterContext) {
	b.turnCtx = ctx
}

// Mode returns "prompt".
func (b *PromptModeBuilder) Mode() string { return "prompt" }

// BuildSystemPrompt assembles the final system prompt:
//
//	{base engine system prompt}
//
//	# 可用工具
//	{serialized filtered tools}
//
//	# 输出格式规则
//	{Format A/B rules + few-shot}
//
// Returns the base prompt unchanged if no serializer is wired (degraded
// mode — engine sees no tool descriptions, will likely produce only
// Format B answers).
func (b *PromptModeBuilder) BuildSystemPrompt(base string, tools []ToolSchema) string {
	if b.serializer == nil {
		return base
	}

	filtered := tools
	if b.filter != nil {
		filtered = b.filter(tools, b.turnCtx)
	}

	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n# 可用工具 (PromptToolAdapter 模式)\n\n")
	sb.WriteString("当前 LLM 未开启 Function Calling, 工具通过 JSON 格式调用. ")
	sb.WriteString("下面列出本轮可用工具:\n\n")
	sb.WriteString(b.serializer(filtered))
	sb.WriteString("\n\n")
	sb.WriteString(formatRulesPrompt)
	sb.WriteString("\n\n")
	sb.WriteString(fewShotPrompt)
	return sb.String()
}

// PrepareRequest clears req.Tools so vLLM/OpenAI compat backends don't
// reject the call. Tool descriptions are now in the system prompt
// (PromptModeBuilder.BuildSystemPrompt put them there).
func (b *PromptModeBuilder) PrepareRequest(req *Request) {
	req.Tools = nil
}

// PostProcessResponse parses JSON tool_calls from resp.Content. Four
// outcomes:
//
//  1. Format A (JSON found, parsed OK): populate resp.ToolCalls, clear
//     resp.Content. Engine sees structured tool calls.
//  2. Format B (no JSON-like content): leave resp.Content alone. Engine
//     treats it as the final answer and exits the ReAct loop.
//  3. Parse error WITH JSON-like content: set NeedRetry + RetryFeedback
//     so Engine appends a corrective system message and re-runs the LLM
//     (max 2 retries via MaxParseRetries).
//  4. Pre-existing ToolCalls (hybrid backend): pass through untouched.
func (b *PromptModeBuilder) PostProcessResponse(resp *Response) *Response {
	if resp == nil {
		return resp
	}
	// Skip processing if the LLM already provided structured tool_calls
	// (some hybrid backends sneak them through even in prompt mode).
	if len(resp.ToolCalls) > 0 {
		return resp
	}
	if strings.TrimSpace(resp.Content) == "" {
		return resp
	}

	result := b.parser.Parse(resp.Content)
	if len(result.Calls) > 0 {
		resp.ToolCalls = result.Calls
		resp.Content = "" // Engine prefers structured calls over text when both present
		return resp
	}
	// Hard parse error on content that looked like JSON → request retry.
	// Engine respects MaxParseRetries to avoid infinite loops on a
	// stubborn LLM that never gets the format right.
	if result.ParseError != nil {
		resp.NeedRetry = true
		resp.RetryFeedback = buildRetryFeedback(result.ParseError)
		return resp
	}
	// Format B fallthrough — leave Content as-is.
	return resp
}

// buildRetryFeedback composes the system message that gets appended to
// the conversation when JSON parse fails. The message is intentionally
// concrete and ends with an example so even small models can lock onto
// the right format on the second try.
func buildRetryFeedback(parseErr error) string {
	var b strings.Builder
	b.WriteString("⚠️ 你上一轮的输出无法被解析为合法 JSON tool_call.\n\n")
	b.WriteString("**错误**: ")
	b.WriteString(parseErr.Error())
	b.WriteString("\n\n")
	b.WriteString("**请重试**, 严格按以下格式之一回复 (不要混用, 不要加解释前缀):\n\n")
	b.WriteString("- **格式 A** (调工具): 仅输出 JSON 代码块\n")
	b.WriteString("  ```json\n")
	b.WriteString("  {\"tool_calls\": [{\"name\": \"<工具名>\", \"args\": {...}}]}\n")
	b.WriteString("  ```\n\n")
	b.WriteString("- **格式 B** (给最终答案): 直接输出 markdown 文本, 不要 JSON 块\n")
	return b.String()
}

// ───────────────────────────────────────────────────────────────────────
// Prompt templates
// ───────────────────────────────────────────────────────────────────────

const formatRulesPrompt = `# 输出格式规则

你必须严格选用以下两种格式之一回复用户. **不要混用, 不要在格式 A 前加任何
解释文字**:

## 格式 A — 调用工具

当你需要采集更多数据时, **仅**输出下面这个 JSON 代码块, 不要任何前缀文字:

` + "```json" + `
{"tool_calls": [{"name": "<工具名>", "args": {"<参数key>": "<参数value>"}}]}
` + "```" + `

允许一次声明多个 tool_calls (数组), 引擎会优先并发执行只读工具.

## 格式 B — 给最终答案

当数据已经足够回答用户时, **直接输出 markdown 文本**, 不要 JSON 代码块.
答案应遵循三层诊断模板 (根因分析 / 紧急措施 / 根因修复 - 或 Layer 1/2/3).

## 关键约束

1. **第一个字符决定格式**: 以 ` + "`" + ` ` + "```" + ` ` + "`" + ` 开头表示格式 A, 其他字符表示格式 B
2. **不要写"我先调 health 工具"这类前缀**, 调工具就直接输出 JSON
3. **工具名严格匹配**, 不要简写或意译 (e.g. health 不能写成 heath / health_check)
4. **没必要的工具不要调**, 数据足够就直接给答案 (格式 B)
5. **格式 B 内不要嵌入 ` + "```json" + ` 块**, 那会被解析器当成工具调用
6. **SQL_ID 调优必须直接交给 sqltune**: 当用户提到 SQL_ID (一串数字, 如 581990336)
   并询问优化/调优/执行计划时, 直接调用 sqltune, args 只传这个数字 ID.
   sqltune 内部会走受控 sqlfetch 路径解析真实 SQL. **绝对禁止**根据 SQL_ID 自己编造、
   仿写或改写 SQL 文本.
7. **同一工具相同参数不要重复调用 >= 2 次**: 如果一个工具已经返回过结果 (成功或失败),
   重复调用大概率拿到同样结果. 该换策略或交付当前已有结论.`

const fewShotPrompt = `# 示例

## 示例 1: 单 SQL 调优 - SQL_ID 直接交给 sqltune (格式 A, 关键示例)

User: SQL_ID 4175761868 怎么优化
Assistant:
` + "```json" + `
{"tool_calls": [{"name": "sqltune", "args": {"args": "4175761868", "mode": "quick"}}]}
` + "```" + `

**反例 (绝对不能这么干)**:
` + "```json" + `
{"tool_calls": [{"name": "sqltune", "args": {"args": "SELECT * FROM <imagined_table> WHERE id = 4175761868"}}]}
` + "```" + `
错在: SQL_ID 不是表名、不是主键值, 模型不能根据数字编造示例表或示例 SQL.
正确做法是把数字 ID 原样传给 sqltune, 由 sqltune 查询 statement_history / statement.

## 示例 2: 聚类诊断起步 (格式 A, 多工具并发)

User: 数据库怎么有点慢, 帮我诊断下
Assistant:
` + "```json" + `
{"tool_calls": [
  {"name": "health", "args": {}},
  {"name": "alert", "args": {}},
  {"name": "activesessions", "args": {}}
]}
` + "```" + `

## 示例 3: WDR 报告分析 (格式 A, 带文件路径参数)

User: 分析下 /tmp/wdr_report.html 报告
Assistant:
` + "```json" + `
{"tool_calls": [{"name": "wdranalyze", "args": {"args": "file /tmp/wdr_report.html"}}]}
` + "```" + `

## 示例 4: 工具结果已足够, 给最终答案 (格式 B)

User: (前面已通过 health/alert/blocktree 拿到死锁详情)
Assistant:
## 根因分析

pid=12345 长事务 (idle in transaction, 18 分钟) 持有 customers.id=14523 行锁,
阻塞了 pid=23456 的 UPDATE. 死锁源于业务事务粒度过大未及时提交.

## 紧急措施

` + "```sql" + `
SELECT pg_cancel_backend(12345);
` + "```" + `

## 根因修复

业务侧将 18 分钟长事务拆为多个短事务, 每个不超过 30 秒.

## 示例 5: 无业务上下文的概念问题 (格式 B, 不调工具)

User: openGauss 跟 PostgreSQL 是什么关系
Assistant:
openGauss 基于 PostgreSQL 9.2 内核演进, 由华为主导开源. 主要差异:
1. 存储引擎: 增加列存 (Astore/Ustore) 与 MOT
2. 安全: 透明加密、细粒度审计
3. 高可用: 内置主备切换、流复制优化
...

## 示例 6: 上一轮 JSON 错误, 本轮修正 (格式 A, 错误恢复)

User: (前一轮 LLM 输出了无效 JSON, 引擎反馈了错误)
Assistant:
` + "```json" + `
{"tool_calls": [{"name": "health", "args": {}}]}
` + "```"

// JoinNonEmpty is a small helper for the wrappers in this package — keeps
// the file self-contained for testing the few-shot format.
func JoinNonEmpty(parts []string, sep string) string {
	filtered := parts[:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, sep)
}

// FormatPromptBytes returns the byte size of the static prompt overhead
// (format rules + few-shot, excluding tool descriptions). For telemetry.
func FormatPromptBytes() int {
	return len(formatRulesPrompt) + len(fewShotPrompt) + 64 // +64 for "# 可用工具" boilerplate
}

// inspectablePromptParts is exposed for test verification.
func (b *PromptModeBuilder) inspectablePromptParts(base string, tools []ToolSchema) (basePart, toolsPart, rulesPart, fewShotPart string) {
	filtered := tools
	if b.filter != nil {
		filtered = b.filter(tools, b.turnCtx)
	}
	basePart = base
	if b.serializer != nil {
		toolsPart = b.serializer(filtered)
	}
	rulesPart = formatRulesPrompt
	fewShotPart = fewShotPrompt
	return
}

// ensureNonEmpty is a defensive helper, currently unused but kept for
// v1.2.x extensions (e.g., guard against an LLM that returns no tool calls
// AND no content).
func ensureNonEmpty(s string) string {
	if s == "" {
		return fmt.Sprintf("(空响应 · %s)", "PromptModeBuilder")
	}
	return s
}
