/*-------------------------------------------------------------------------
 *
 * builder.go
 *	  BuildInput holds what the Builder needs from EngineInput.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/builder.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/engine/policy"
	"github.com/sqlrush/opendb/internal/engine/profile"
	"github.com/sqlrush/opendb/internal/engine/provider"
)

// BuildInput holds what the Builder needs from EngineInput.
type BuildInput struct {
	UserMessage      string
	CompressedReport string
	Product          string
	Version          string
	Instance         string
	Host             string
	Mode             string
	MaxTurns         int
	HistoryMessages  []Message // loaded from a previous session (may be empty)

	// Capability ("small" / "large") controls which universal prompt variant
	// is loaded. Large models get the strict v1.1.15 prompt with abstract
	// completion criteria; small/medium models get a templated prompt with
	// concrete output patterns to fill (since smaller models can't internalize
	// abstract rules as well). Empty string falls back to the strict variant
	// to preserve back-compat.
	Capability string
}

// BuildResult holds the assembled context for the first Engine turn.
type BuildResult struct {
	SystemPrompt []SystemPromptBlock
	Messages     []Message
}

// Builder assembles the 6-layer context for the Engine.
type Builder struct {
	profile      profile.PromptProfile
	capability   *provider.ProviderCapability
	rules        *RulesLoader
	policyLoader *policy.Loader // nil = use RulesLoader fallback
	memoryStore  *memory.Store  // nil = no memory injection
}

// NewBuilder creates a context builder.
func NewBuilder(
	p profile.PromptProfile,
	cap *provider.ProviderCapability,
	rulesDir string,
) *Builder {
	return &Builder{
		profile:    p,
		capability: cap,
		rules:      NewRulesLoader(rulesDir),
	}
}

// SetMemoryStore injects the memory store for PROFILE.md and MEMORY.md injection.
func (b *Builder) SetMemoryStore(store *memory.Store) {
	b.memoryStore = store
}

// SetPolicyLoader replaces the legacy RulesLoader with the 4-level PolicyLoader.
func (b *Builder) SetPolicyLoader(loader *policy.Loader) {
	b.policyLoader = loader
}

// Build constructs the initial context (system prompt + messages) for Engine.Run().
func (b *Builder) Build(input BuildInput) BuildResult {
	return BuildResult{
		SystemPrompt: b.buildSystemPrompt(input),
		Messages:     b.buildMessages(input),
	}
}

// buildSystemPrompt assembles system prompt blocks with optional caching.
func (b *Builder) buildSystemPrompt(input BuildInput) []SystemPromptBlock {
	var blocks []SystemPromptBlock

	// Block 1: Universal base (cacheable). Variant chosen by model capability:
	// strict (default/large) — abstract rules, expects model to internalize;
	// templated (small/medium) — concrete output patterns to fill in.
	base := SystemPromptBlock{Text: universalSystemPrompt(input.Capability)}
	if b.capability != nil && b.capability.Caching.Mode == provider.CachingExplicit {
		base.CacheControl = &CacheControl{Type: "ephemeral"}
	}
	blocks = append(blocks, base)

	// Block 2: DB-specific rules (cacheable)
	dbRules := b.profile.SystemPromptRules()
	if dbRules != "" {
		block := SystemPromptBlock{Text: dbRules}
		if b.capability != nil && b.capability.Caching.Mode == provider.CachingExplicit {
			block.CacheControl = &CacheControl{Type: "ephemeral"}
		}
		blocks = append(blocks, block)
	}

	// Block 3: Management prompts — context/memory/policy awareness (cacheable)
	mgmtParts := []string{ContextAwarenessPrompt}
	if b.memoryStore != nil {
		mgmtParts = append(mgmtParts, MemoryManagementPrompt)
	}
	if b.policyLoader != nil {
		mgmtParts = append(mgmtParts, PolicyCompliancePrompt)
	}
	mgmtBlock := SystemPromptBlock{Text: strings.Join(mgmtParts, "\n\n")}
	if b.capability != nil && b.capability.Caching.Mode == provider.CachingExplicit {
		mgmtBlock.CacheControl = &CacheControl{Type: "ephemeral"}
	}
	blocks = append(blocks, mgmtBlock)

	// Block 4: Policies / rules (not cached — may change anytime)
	if b.policyLoader != nil {
		// New 4-level PolicyLoader
		loaded := b.policyLoader.Load(
			b.profile.SystemPromptRules(),
			input.Instance,
			"", // session policies injected via EngineInput, not here
		)
		if loaded.Merged != "" {
			blocks = append(blocks, SystemPromptBlock{Text: loaded.Merged})
		}
	} else {
		// Legacy RulesLoader fallback
		userRules := b.rules.Load(input.Instance)
		if userRules != "" {
			blocks = append(blocks, SystemPromptBlock{
				Text: "# 用户自定义规则\n\nIMPORTANT: 以下是用户定义的诊断规范，必须遵守。当与默认规则冲突时，以用户规则为准。\n\n" + userRules,
			})
		}
	}

	// Block 5: Instance memory
	if b.memoryStore != nil && input.Instance != "" {
		// 5a: PROFILE.md — same across most /llm invocations (the LLM only
		// rewrites it occasionally via memory_update), so it's a good
		// prompt-cache candidate. With Anthropic explicit caching, marking
		// this block reduces input tokens on consecutive calls by ~10-20%
		// on top of the savings from blocks 1-3.
		if profileContent, err := b.memoryStore.LoadProfile(); err == nil && profileContent != "" {
			block := SystemPromptBlock{
				Text: "# 实例画像\n\n以下是当前数据库实例的画像，描述其负载特征和问题特征：\n\n" + profileContent,
			}
			if b.capability != nil && b.capability.Caching.Mode == provider.CachingExplicit {
				block.CacheControl = &CacheControl{Type: "ephemeral"}
			}
			blocks = append(blocks, block)
		}

		// 5b: MEMORY.md — index churns on every memory_write, so do NOT
		// cache (marking it would waste cache budget on 1-use blocks).
		if indexContent, err := b.memoryStore.LoadIndex(); err == nil && indexContent != "" {
			blocks = append(blocks, SystemPromptBlock{
				Text: "# 实例记忆\n\n以下是当前数据库实例的历史记忆索引。如需查看某条记忆的详细内容，请使用 memory_recall 工具。\n\n" + indexContent,
			})
		}
	}

	// Block 6: Mode modifier (dynamic)
	modeText := modeModifier(input.Mode)
	if modeText != "" {
		blocks = append(blocks, SystemPromptBlock{Text: modeText})
	}

	return blocks
}

// buildMessages assembles the initial message list.
// If HistoryMessages are present (session resume), they are prepended before
// the new environment and diagnostic context.
func (b *Builder) buildMessages(input BuildInput) []Message {
	var messages []Message

	// Prepend history from previous session (if resuming)
	if len(input.HistoryMessages) > 0 {
		messages = append(messages, input.HistoryMessages...)
	}

	// Layer 2: Environment context (hidden from user)
	envMsg := b.buildEnvironmentContext(input)
	messages = append(messages, envMsg)

	// Layer 3: Diagnostic context (user message + report)
	diagMsg := b.buildDiagnoseContext(input)
	messages = append(messages, diagMsg)

	return messages
}

// buildEnvironmentContext creates a hidden message with database environment info.
func (b *Builder) buildEnvironmentContext(input BuildInput) Message {
	parts := []string{
		"<system-reminder>",
		"# 当前环境",
	}

	if input.Product != "" {
		line := fmt.Sprintf("数据库: %s", input.Product)
		if input.Version != "" {
			line += " " + input.Version
		}
		parts = append(parts, line)
	}
	if input.Instance != "" {
		parts = append(parts, fmt.Sprintf("实例: %s", input.Instance))
	}
	if input.Host != "" {
		parts = append(parts, fmt.Sprintf("地址: %s", input.Host))
	}

	parts = append(parts, fmt.Sprintf("当前时间: %s", time.Now().Format("2006-01-02 15:04:05")))

	if input.Mode != "" {
		parts = append(parts, fmt.Sprintf("诊断模式: %s", input.Mode))
	}
	if input.MaxTurns > 0 {
		parts = append(parts, fmt.Sprintf("最大轮次: %d", input.MaxTurns))
	}

	parts = append(parts, "</system-reminder>")

	return Message{
		Role:    "user",
		Content: strings.Join(parts, "\n"),
		IsMeta:  true,
	}
}

// buildDiagnoseContext creates the user message with optional report.
func (b *Builder) buildDiagnoseContext(input BuildInput) Message {
	if input.CompressedReport == "" {
		return Message{
			Role:    "user",
			Content: input.UserMessage,
		}
	}

	content := fmt.Sprintf("%s\n\n以下是当前数据库异常报告：\n%s\n\n请基于以上数据和你的工具进行诊断。",
		input.UserMessage, input.CompressedReport)

	return Message{
		Role:    "user",
		Content: content,
	}
}

// InjectTurnContext adds per-turn guidance messages:
//   - Tool history reminder: injected EVERY round after first tool call (all models)
//   - Convergence hint: injected only on last 2 rounds
//
// toolsInvoked is the list of tools called so far in this diagnosis session.
// Returns a new message slice; does not mutate the input.
func (b *Builder) InjectTurnContext(messages []Message, turn, maxTurns int, toolsInvoked []string) []Message {
	// Build deduplicated tool history
	toolHistory := dedupTools(toolsInvoked)

	needToolReminder := len(toolHistory) > 0
	needConvergence := maxTurns > 0 && turn >= maxTurns-2

	if !needToolReminder && !needConvergence {
		return messages
	}

	result := make([]Message, len(messages))
	copy(result, messages)

	// Tool history reminder — every round after first tool call.
	// Prevents models (esp. Opus) from citing tools they never called.
	if needToolReminder {
		toolList := strings.Join(toolHistory, ", ")
		reminder := fmt.Sprintf(
			"<system-reminder>你在本次诊断中实际调用的工具: [%s]\n"+
				"结论中只能引用上述工具返回的数据。不能引用未调用的工具作为证据来源。如果需要更多数据请继续调用工具，不要编造。</system-reminder>",
			toolList,
		)
		result = append(result, Message{
			Role: "user", Content: reminder, IsMeta: true,
		})
	}

	// Convergence hint — last 2 rounds only.
	if needConvergence {
		hint := fmt.Sprintf(
			"<system-reminder>你已使用 %d/%d 轮。请在本轮直接给出最终诊断总结，不要再调用任何工具。如果信息不足，基于已有数据给出最佳判断。</system-reminder>",
			turn+1, maxTurns,
		)
		result = append(result, Message{
			Role: "user", Content: hint, IsMeta: true,
		})
	}

	return result
}

// dedupTools returns unique tool names preserving order.
func dedupTools(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var unique []string
	for _, t := range tools {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	return unique
}

// PrepareMessagesForNextTurn handles thinking content based on provider policy.
// Returns a new message slice; does not mutate the input.
func (b *Builder) PrepareMessagesForNextTurn(messages []Message, isNewUserTurn bool) []Message {
	if b.capability == nil {
		return messages
	}

	policy := b.capability.Thinking.MultiTurnPolicy
	if policy == provider.ThinkingPreserveAll {
		return messages
	}

	result := make([]Message, len(messages))
	for i, msg := range messages {
		newMsg := msg // Copy (immutable)

		switch policy {
		case provider.ThinkingStripBetweenTurns:
			if isNewUserTurn && msg.Role == "assistant" {
				newMsg.Thinking = ""
				newMsg.ThinkingBlocks = nil
			}
		case provider.ThinkingStripAll:
			newMsg.Thinking = ""
			newMsg.ThinkingBlocks = nil
		}

		result[i] = newMsg
	}

	return result
}

// ── System Prompt Content ──

// universalSystemPrompt picks the prompt variant by model capability.
// "small" / "medium" → templated variant (concrete patterns to fill in,
// fewer abstract rules) because smaller models lose attention budget when
// asked to internalize 6+ abstract requirements.
// Anything else (incl. "large", empty) → strict variant.
func universalSystemPrompt(capability string) string {
	switch capability {
	case "small", "medium":
		return universalSystemPromptTemplated()
	default:
		return universalSystemPromptStrict()
	}
}

func universalSystemPromptStrict() string {
	return `你是 OpenDB 数据库诊断专家。你通过调用工具采集数据库信息，基于证据进行链式推理，给出精准的根因和可执行的修复方案。用户是专业 DBA，无需解释基础概念。

# 核心原则

1. 用证据说话 — 每个结论必须引用工具返回的具体数据。没有查过的数据不能出现在结论中
2. 禁止编造 — 表名/数值/统计必须来自工具查询结果。SQL 文本中出现的对象名不代表对象存在
3. 先观察后判断 — 至少 2-3 个维度的数据再给结论，避免单一指标误判
4. 调查充分才收敛 — 证据"足够"的标准是：根因 + 完整证据链 + 可直接执行的修复 SQL 都到位。
   只识别出"可能根因"或"建议进一步调查"都不算充分，必须自己继续调用工具直到给出可执行方案

## 完成标准（凡是要给出最终诊断前必须自检）

- 提到任何 sql_id？必须先用工具拿到 SQL 文本（前 200 字符以上）
- 提到任何对象（表/索引/视图）？必须用 sql 工具验证它存在
- 给出修复 SQL？必须语法完整可直接执行，不能是骨架
- 提到 plan_hash / 执行计划相关？必须先调 explain
- 提到锁/阻塞？必须查 blocktree 拿到完整阻塞链
- 提到等待事件占比？必须有具体百分比，不能"高/低"

凡是上述任一项缺失却写进了结论 → 停下来，先调对应工具，再继续

# 推理流程

1. 观察现象 — 从异常报告或初始查询中识别关键异常指标
2. 建立假设 — 不超过 2-3 个根因方向
3. 收集证据 — 每轮 1-2 个工具，针对假设做最直接验证
4. 排除或确认 — 基于证据收窄方向
5. 深入验证 — 对最可能的方向做深层查询（如从 waits → SQL → 执行计划）
6. 给出结论 — 明确的根因 + 证据链 + 可执行方案

# 诊断归因原则

- 用户描述的症状优先 — 根因必须能解释症状的变化
- 区分"持续背景指标"和"与用户问题相关的异常指标"
- 持续写入负载（pgbench/批量导入）的 WAL 压力和 IO 等待是背景噪音，不作首要根因
- 当发现异常指标时追问因果链：现象 → 中间原因 → 根因。追问不超过 3 层

# 工具使用

- 优先用专用工具，sql 是最后手段
- 每轮 1-2 个工具，聚焦最关键的验证点
- 不重复查同一工具：前轮已查的数据直接引用
- 查询类工具可放心用；操作类工具（kill/alter）需更充分证据
- 模糊问题（"最近有点慢"）→ activesessions/waits 起步

## 工具选择决策（**重要**）

判断用户意图后选对应工具集，**不要混用**：

**用户意图: 单条 SQL 调优**（"这条 SQL 怎么优化 / 有没有优化空间 / SQL 慢 / 帮我看下这个 SQL / SQL_ID xxx 怎么优化"）
  → **第一调用 sqltune** 工具；用户给完整 SQL 就传完整 SQL，用户给 SQL_ID 就只传数字 ID
  → sqltune 内部跑 5 维度专项调优分析（SQL 重写 / 索引 / HINT / 表结构 / 统计），含真实 EXPLAIN 验证 + 等价性检查
  → **不要走 health/alert/activesessions 那一套**（那是聚类层诊断，跟单 SQL 调优无关）
  → sqltune 输出已经是完整 markdown 报告，你直接转给用户即可，不用再加自己的分析

**用户意图: 聚类层问题**（"数据库慢 / 出问题 / 卡了 / 死锁 / 哪里有问题"）
  → 走标准诊断流程: health → alert → activesessions → waits → blocktree → ...
  → 识别出主要问题是某条 SQL 后，**输出末尾建议** "用 /sqltune <SQL> 深挖此 SQL 的优化空间"
  → 不要自己尝试用 explain 做完整 SQL 调优（深度不够），转给 sqltune

**用户意图: WDR 报告分析**（"分析 wdr 报告 / wdr 风险 / 解读 wdr / wdr 怎么样 / wdr 文件 *.html"）
  → **第一调用 wdranalyze** 工具，传 ` + "`file <path>`" + ` 或 ` + "`latest`" + ` 或 ` + "`<snapA> <snapB>`" + `
  → wdranalyze 内部跑 7 阶段（采集 → 解析 → 规则评分 → TopSQL 深挖 → LLM 综合 → 渲染 → 持久化），
     输出含 Layer 1 总览评估 / Layer 2 风险详解 / Layer 3 优化方案 的**完整 markdown 报告**
  → **直接转给用户即可，不要再加自己的分析、不要重新格式化、不要抽取子章节**
     （工具结果开头会有 ` + "`<!-- WDR_REPORT_BEGIN: ... -->`" + ` 注释作为 passthrough 标记）
  → 同 sqltune: 工具输出已是终态报告, 二次加工只会损失结构化信息

**判断关键**：用户消息中是否粘贴了一条具体 SQL **或者给了 SQL_ID** + 问"怎么优化 / 怎么改 / 有没有优化空间"。
是 → sqltune；否 → 标准诊断。

### SQL_ID 调优策略（**重要 — 小模型必须遵守**）

  - **用户直接粘 SQL** → 用粘的版本传 sqltune
  - **用户只给 SQL_ID（如 "SQL_ID 33402943 怎么优化"）**：
    1. **直接调用 sqltune，args 只传数字 ID**，例如 ` + "`" + `{"args":"33402943","mode":"quick"}` + "`" + `
    2. sqltune 内部会走受控 sqlfetch 路径查 statement_history / statement，并处理占位符
    3. **禁止模型自己手写 SQL 查 dbe_perf，也禁止根据 SQL_ID 编造示例表或示例 SQL**

### og-lite 元数据列名速查（**实测多个模型都用错过 PG 命名约定，照下面来**）

  - dbe_perf.statement (归一化聚合视图):
      · id 字段 ` + "`unique_sql_id`" + `（不是 query_id / queryid）
      · query 是归一化版本 (字面量被替换成 ?)，**EXPLAIN 必挂**
      · 只有 query / n_calls / total_elapse_time 等基础字段，**无 schema_name**
  - dbe_perf.statement_history (历史快照视图，**带字面量 + schema_name**):
      · id 字段 ` + "`unique_query_id`" + `（注意 _id 后缀，与 statement 命名不同！）
      · 字段: query / schema_name / start_time / finish_time / db_time / query_plan / debug_query_id
      · **是给 sqltune 用的首选数据源**
  - 两视图链接：` + "`statement.unique_sql_id == statement_history.unique_query_id`" + `（同值，字段名不同）
  - statement_history.query 已淘汰时显示占位字符串
    ` + "`/* missing SQL statement, GUC instr_unique_sql_count is too small. */`" + `
    这类情况由 sqltune/sqlfetch 内部处理；外层模型不要手写 SQL 拉取。

### sqltune 失败处理（**重要**）

  - sqltune 返回错误时**不要静默切通用诊断**，必须在最终输出明确说明：
    "/sqltune 调用失败，原因：<error message>。已降级到通用诊断"
  - 错误含 ` + "`phase A`" + ` / ` + "`plan collection failed`" + ` / ` + "`relation does not exist`" + ` / ` + "`unbound parameter`" + ` / ` + "`placeholder`" + `：
    直接说明失败原因。不要用模型编造的示例表名重试。

## 主动深挖原则（强制 — 严格遵守）

**核心规则**：如果你想到任何"还需要查 X / Y / Z 才能更精准"的念头，
**立刻调用 X / Y / Z**，不要写在输出里让用户去决定。

### 禁用措辞（任何输出都不准出现以下任意一种）

- "本次诊断仅基于 X、Y..."
- "如需更精准 / 如需更详细 / 如需进一步分析..."
- "建议补充调用 X" / "建议进一步调用 X" / "可以补充 X 工具"
- "需要补充 SQL 文本" / "需要补充执行计划" / "需要补充对象状态"
- "请允许我调用 X" / "请补充查询 X"

发现自己要写这类句子时，**停下来调用对应工具**，把结果纳入诊断再写最终输出。

### 正例 / 反例

❌ 反例：
"本次诊断仅基于 health/blocktree 两个工具，未查被阻塞 SQL 7r1ux4xqxuvw0
 的具体文本，建议补充调用 ash sql 7r1ux4xqxuvw0 或查询 v\$sql 获取
 SQL 文本后再决定是否加索引。"

✅ 正例（应该这样做）：
[当轮直接调 ash sql 7r1ux4xqxuvw0 + sql 查 v$sql] →
[拿到 SQL 文本 + 计划] →
[在最终输出里直接写：sql 文本是 'UPDATE ... WHERE col=:1', 走 FTS,
 缺索引 idx_xxx (col), CREATE INDEX 方案如下...]

### 例外（仅这一类需要用户确认）

操作类工具（kill / alter / drop / 任何带 SecurityLevel > 0）需用户确认。
查询类工具（含 explain / sql / ash / topsql / tableinfo / 任何 SecurityLevel = 0）
**一律自己调**，不要请示。

# 输出格式

中间轮次：简短说明观察到什么、判断方向、下一步要查什么。

最终诊断按以下结构（无 Sentinel 报告时可省略"## 紧急措施"）：

**例外**: 当工具返回的已是完整 markdown 报告（sqltune / wdranalyze，标志：内容含
` + "`<!-- WDR_REPORT_BEGIN`" + ` 或 ` + "`## Layer 1`" + ` 等）— **跳过下面的"根因分析 /
紧急措施 / 根因修复"模板，把工具返回的 markdown 原样输出给用户**，不要重新组织、
不要抽取摘要、不要把表格换格式。

## 根因分析
- 先用 1 个表格列关键证据（指标 / 数据 / 来源工具），不超过 4 列
- 再给明确的根因结论 + 因果链
- 提到问题 SQL 时必须附 SQL 文本前 200 字符，不能只给 sql_id 和泛泛建议

## 紧急措施
可立即执行的止血方案，完整原生 SQL 放代码块。

## 根因修复
每个方案给：
- 操作（原生 SQL 或命令）
- ⚠️ 风险评估
- 📋 前置检查
- 🔄 回滚方案

# 全局约束

- 始终中文回复
- 段落间最多一个空行
- SQL 放代码块，语法完整可执行
- 禁止 DROP TABLE/DATABASE 等不可逆建议
- kill 仅在明确阻塞且影响业务时建议；alter 参数修改需说明影响范围`
}

// universalSystemPromptTemplated is the variant for small / medium models
// (GLM-5, Qwen, DeepSeek-V3 等). It replaces v1.1.15 的抽象规则 with concrete
// fill-in templates, since smaller models lose attention budget on abstract
// rules but follow well-named templates faithfully.
func universalSystemPromptTemplated() string {
	return `你是 OpenDB 数据库诊断专家。通过工具采集数据，给精准根因 + 可执行 SQL。用户是专业 DBA。

# 核心原则

1. 用证据说话 — 结论必须引用工具返回的具体数据
2. 禁止编造 — 表名/数值/统计必须来自工具结果
3. 至少 2-3 个维度的数据再下结论
4. 调查充分才收敛 — 不允许只识别"可能根因"或"建议进一步调查"

# 推理流程

1. 看现象 → 2. 想假设 → 3. 调工具验证 → 4. 排除/确认 → 5. 给根因+方案

每轮 1-2 个工具，不重复查同一工具。模糊问题（"最近有点慢"）用 activesessions/waits 起步。

# 工具选择决策（**重要**）

判断用户意图，选对应工具集，不要混用：

**意图 A: 单条 SQL 调优**（用户消息含一段具体 SQL，或 SQL_ID + "怎么优化 / 有没有优化空间 / 这条 SQL 慢"）
  → **第一调用 sqltune 工具**；完整 SQL 传完整 SQL，SQL_ID 只传数字 ID
  → sqltune 内部跑 5 维度专项调优分析（SQL 重写 / 索引 / HINT / 表结构 / 统计），含真实 EXPLAIN 验证 + 等价性检查
  → **不要走 health/alert/activesessions 那一套**（那是聚类层诊断）
  → sqltune 的输出是完整 markdown 报告，直接转给用户即可

**意图 B: 聚类层问题**（"数据库慢 / 出问题 / 卡了 / 死锁"）
  → 走标准诊断流程: health → alert → activesessions → waits → ...
  → 末尾建议"用 /sqltune <SQL> 深挖此 SQL"

**意图 C: WDR 报告分析**（"分析 wdr 报告 / wdr 风险 / 解读 wdr / wdr 文件 *.html"）
  → **第一调用 wdranalyze 工具**, 传 ` + "`file <path>`" + ` 或 ` + "`latest`" + ` 或 ` + "`<snapA> <snapB>`" + `
  → wdranalyze 输出含 Layer 1 总览评估 / Layer 2 风险详解 / Layer 3 优化方案 的**完整 markdown 报告**
  → **直接转给用户即可, 不要再加自己的根因分析、不要重新格式化、不要抽取子章节**
     (工具结果开头有 ` + "`<!-- WDR_REPORT_BEGIN: ... -->`" + ` 注释作为 passthrough 标记)
  → 同 sqltune: 工具输出已是终态报告, 二次加工只会损失结构化信息

**判断关键**：用户消息是否粘贴了一条具体 SQL，或是否给了 SQL_ID + 问"怎么优化"。是 → sqltune；
       是否提到 wdr 报告? 是 → wdranalyze; 否 → 标准诊断。

# 主动深挖（强制）

发现"还需要查 X"的念头 → **直接调用 X，不要写在输出里让用户决定**。

禁用任何下面这些措辞：
- "本次诊断仅基于 X..."
- "如需更精准/如需更详细..."
- "建议补充调用 X / 进一步调用 X"
- "需要补充 SQL 文本/执行计划/对象状态"
- "请允许我调用 X / 请补充查询 X"

唯一例外：操作类工具（kill/alter/drop）需用户确认。查询类（含 explain/sql/topsql/ash）一律自己调。

# 输出模板（必须严格按下面格式填充，不留抽象描述）

**例外: 工具结果已是完整报告时直接转发**
当工具返回 markdown 报告 (sqltune / wdranalyze) — 标志: 内容包含 ` + "`<!-- WDR_REPORT_BEGIN`" + ` 或
` + "`# SQL 调优`" + ` 或 ` + "`## Layer 1`" + ` 这类完整报告章节 — **跳过下面的 "根因分析 /
紧急措施 / 根因修复" 模板, 把工具返回的 markdown 原样输出给用户即可**, 不要重新组织
不要抽取摘要不要把表格换格式. 这是已加工好的终态报告.

## 根因分析

第一步: 用表格列证据，不超过 4 列。**每行必须包含具体数字/对象名**：

| 指标          | 数据                          | 来源工具 |
|---------------|-------------------------------|----------|
| <具体指标名>   | <具体数字或对象，禁止"高/低/异常"> | <工具名>  |

第二步: 一段话写根因和因果链。例如:
"长事务（pid=12345，duration=18min，pg_sleep）持有 backend_xmin →
autovacuum 无法回收 → bench_mix_a dead_tup 200万 (dead_pct=200%)
→ 表扫描代价随 dead_tup 线性增长。"

## 紧急措施（无 Sentinel 报告时可省略）

可立即执行的止血 SQL，必须**完整**（含 schema.table 全名、值、参数），不能是 SQL 骨架：

` + "```sql" + `
-- 例: 终止 idle in transaction > 10 分钟的连接（先 cancel 再 terminate）
SELECT pg_cancel_backend(pid)
  FROM pg_stat_activity
 WHERE state = 'idle in transaction'
   AND now() - state_change > interval '10 min';
` + "```" + `

## 根因修复

每个方案严格按 4 字段填，**不能漏字段**：

### 方案 1: <一行方案名>
- **操作**:
  ` + "```sql" + `
  <完整可执行 SQL，含具体 schema.table 名、参数值>
  ` + "```" + `
- **⚠️ 风险评估**: <具体风险，例: "锁表 X 秒"、"WAL 增长 Y MB"、"会重写 Z 行"，不能写"无风险/有风险">
- **📋 前置检查**:
  ` + "```sql" + `
  <用于确认前置条件的查询 SQL，例: 查表大小、索引存在性>
  ` + "```" + `
- **🔄 回滚方案**:
  ` + "```sql" + `
  <完整的回滚 SQL，例: DROP INDEX、ALTER SYSTEM RESET、ROLLBACK>
  ` + "```" + `

### 方案 2: ...

# 自检清单（输出前必须逐条对照）

写完最终输出，**自检以下 6 条，任一项缺失就回头补全**：

- [ ] 提到任何 sql_id？必须先用工具拿到 SQL 文本前 200 字符以上
- [ ] 提到任何对象（表/索引/视图）？必须有具体名字（schema.table），不能写"某表"
- [ ] 修复 SQL 是完整可执行还是骨架？必须可直接 copy-paste 执行
- [ ] 提到 plan_hash / 执行计划？必须先调 explain
- [ ] 提到锁/阻塞？必须先调 blocktree
- [ ] 提到等待事件占比？必须有具体百分比，不能写"高/低"

# 全局约束

- 中文回复
- 段落间最多一个空行
- SQL 放代码块
- 禁止 DROP TABLE/DATABASE 建议
- kill 仅明确阻塞时建议；alter 参数需说明影响`
}

func modeModifier(mode string) string {
	switch mode {
	case "playbook":
		return `# 当前模式: playbook（单次分析）
- 你只有一次回复机会，不可调用任何工具
- 基于提供的异常报告直接给出分析和建议
- 必须输出完整的三段格式：根因分析 → 紧急措施 → 根因修复`

	case "assist":
		return `# 当前模式: assist（辅助诊断）
- 你可以调用查询类工具获取更多信息
- 只允许使用查询类工具，不允许执行修改操作
- 每轮查询 1-2 个最关键的信息，高效使用每一轮
- 信息足够时立即给出最终诊断`

	case "auto":
		return `# 当前模式: auto（自动诊断）
- 你可以自由调用所有工具进行深度诊断
- 请系统性地收集证据，逐步缩小问题范围
- 在给出最终结论前确保有充分的数据支撑
- 操作类工具仅在证据充分且有必要时使用`

	default:
		return ""
	}
}
