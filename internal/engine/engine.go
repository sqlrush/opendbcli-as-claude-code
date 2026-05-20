/*-------------------------------------------------------------------------
 *
 * engine.go
 *	  Engine is the unified LLM communication engine.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/engine.go
 *
 *-------------------------------------------------------------------------
 */
package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	econtext "github.com/sqlrush/opendb/internal/engine/context"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/engine/policy"
	"github.com/sqlrush/opendb/internal/engine/profile"
	"github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/engine/retry"
	"github.com/sqlrush/opendb/internal/engine/session"
	"github.com/sqlrush/opendb/internal/engine/telemetry"
	"github.com/sqlrush/opendb/internal/engine/tool"
	"github.com/sqlrush/opendb/internal/odberr"
)

// Engine is the unified LLM communication engine.
type Engine struct {
	adapter        provider.ProviderAdapter
	contextBuilder *econtext.Builder
	contextManager *econtext.Manager
	toolOrch       *tool.Orchestrator
	toolLister     tool.ToolLister
	resultHandler  *tool.ResultHandler
	retryPolicy    *retry.Policy
	config         EngineConfig
	profile        profile.PromptProfile
	sessionStore   session.SessionStore // nil = no session persistence
	memoryStore    *memory.Store        // nil = no memory system

	streamDisabled bool // set after first failed streaming attempt; skip streaming for remaining rounds
}

// Option configures an Engine.
type Option func(*Engine)

// WithConfig overrides the default engine configuration.
func WithConfig(cfg EngineConfig) Option {
	return func(e *Engine) { e.config = cfg }
}

// WithSessionStore enables session persistence.
func WithSessionStore(store session.SessionStore) Option {
	return func(e *Engine) { e.sessionStore = store }
}

// WithMemoryStore enables the instance memory system.
func WithMemoryStore(store *memory.Store) Option {
	return func(e *Engine) {
		e.memoryStore = store
		e.contextBuilder.SetMemoryStore(store)
	}
}

// WithPolicyLoader enables the 4-level policy system, replacing the legacy RulesLoader.
func WithPolicyLoader(loader *policy.Loader) Option {
	return func(e *Engine) {
		e.contextBuilder.SetPolicyLoader(loader)
	}
}

// New creates an Engine.
func New(
	adapter provider.ProviderAdapter,
	p profile.PromptProfile,
	executor tool.SkillExecutor,
	toolLister tool.ToolLister,
	opts ...Option,
) *Engine {
	caps := adapter.Capability()

	e := &Engine{
		adapter:        adapter,
		contextBuilder: econtext.NewBuilder(p, caps, ""),
		contextManager: econtext.NewManager(caps.MaxContextWindow, econtext.NewUsageTracker()),
		toolOrch:       tool.NewOrchestrator(executor, 5),
		toolLister:     toolLister,
		resultHandler:  tool.NewResultHandler(4000, ""),
		retryPolicy:    retry.NewPolicy(retry.DefaultConfig(), &caps.RateLimit),
		config:         DefaultConfig(),
		profile:        p,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Run executes the agentic diagnosis loop.
func (e *Engine) Run(ctx context.Context, input EngineInput) (*EngineResult, error) {
	// ── Diagnosis timeout ──
	if e.config.MaxDiagnosisTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.config.MaxDiagnosisTimeout)
		defer cancel()
	}

	// ── Session load ──
	var historyMessages []econtext.Message
	if e.sessionStore != nil && input.SessionID != "" {
		if prev, err := e.sessionStore.Load(ctx, input.SessionID); err == nil && prev != nil {
			historyMessages = make([]econtext.Message, len(prev.Messages))
			copy(historyMessages, prev.Messages)
		}
	}
	// v1.1.47 removed: automatic topic-drift detection (Jaccard-based).
	// Heuristic was unreliable — multiple bugs observed:
	//   1. Wrapped user messages had 67% boilerplate overlap, drift never fired
	//   2. Shared SQL_IDs/identifiers gave 20% overlap, drift never fired
	//   3. Threshold tuning is an endless arms race
	// Adopting Claude Code design: keep full history, let user explicitly
	// manage context via /clear (drops session + screen) or session resume.
	// Auto-compact for context-window-full will land separately (M9.x).

	built := e.contextBuilder.Build(econtext.BuildInput{
		UserMessage:      input.UserMessage,
		CompressedReport: input.CompressedReport,
		Product:          input.DatabaseInfo.Product,
		Version:          input.DatabaseInfo.Version,
		Instance:         input.DatabaseInfo.Instance,
		Host:             input.DatabaseInfo.Host,
		Mode:             string(input.Mode),
		MaxTurns:         input.MaxTurns,
		HistoryMessages:  historyMessages,
		Capability:       input.Capability,
	})

	// ── Session save helper (best-effort, called before each return) ──
	saveSession := func(msgs []econtext.Message, turnsUsed int) {
		if e.sessionStore == nil || input.SessionID == "" {
			return
		}
		// Derive instance from SessionID for consistent Save/Load paths
		saveInstance := input.SessionID.Instance()
		if input.DatabaseInfo.Instance != "" {
			saveInstance = input.DatabaseInfo.Instance
		}
		// Filter out IsMeta messages (turn-summary, env reminder, hint, etc.)
		// — these are transient per-turn context, regenerated each run.
		// Persisting them causes session pollution: every /llm invocation
		// loads them as history and the engine adds new ones, eventually
		// turning the saved session into 90%+ user/meta noise (observed:
		// 156 msgs, 150 user/meta, 5 assistant). Provider APIs reject this
		// shape (DeepSeek thinking mode 400 "reasoning_content must be
		// passed back" — the few assistant msgs lack reasoning_content).
		persisted := make([]provider.Message, 0, len(msgs))
		for _, m := range msgs {
			if m.IsMeta {
				continue
			}
			persisted = append(persisted, m)
		}
		_ = e.sessionStore.Save(ctx, &session.Session{
			ID:         input.SessionID,
			Instance:   saveInstance,
			Messages:   persisted,
			TurnCount:  turnsUsed,
			TokensUsed: e.contextManager.TokenUsage(msgs).Used,
			Status:     session.SessionActive,
		})
	}

	maxTurns := input.MaxTurns
	if maxTurns == 0 {
		maxTurns = e.config.DefaultMaxTurns
	}
	if input.Mode == ModePlaybook {
		maxTurns = 1
	}

	result := &EngineResult{}
	messages := built.Messages
	sysPrompt := built.SystemPrompt
	tools := e.buildTools(input.Mode)
	var totalUsage provider.Usage
	var outputRecoveries int

	// Accumulates content from "deliverable" rounds — final round (no tool
	// calls) plus rounds where the model emits its full analysis alongside
	// only side-effect tool calls (memory_write/update). Without this,
	// result.Content (used by batch mode and saveSession) only captures the
	// last round's brief wrap-up like "诊断已完成", losing the actual report.
	var deliverableContent strings.Builder
	captureDeliverable := func(content string) {
		if content == "" {
			return
		}
		if deliverableContent.Len() > 0 {
			deliverableContent.WriteString("\n\n")
		}
		deliverableContent.WriteString(content)
	}

	// Prompt cache telemetry: log final usage to ~/.opendb/telemetry/cache.log
	// on every exit path (success, max-turns, error). No-op when cache is unused.
	defer func() {
		recorder := telemetry.NewRecorder(telemetry.LogPath())
		_ = recorder.Record(telemetry.FromUsage(
			input.DatabaseInfo.Product,
			e.adapter.Capability().Name,
			totalUsage,
		))
	}()

	// v1.2.1: PromptToolAdapter parse-retry counter. Bumped each time the
	// LLM produces malformed JSON tool_call output; capped at maxParseRetries
	// inside the loop to avoid stubborn-LLM infinite loops.
	parseRetries := 0

	// v1.2.2: cross-turn tool call deduplication. Track signatures (name +
	// args hash) per turn. When the same signature shows up ≥ 2 times
	// across the run, inject a system-reminder before the next LLM call
	// telling the model to switch strategy. Without this, smaller models
	// (35B-class in prompt mode) get stuck calling the same failing tool
	// over and over until they hit MaxTurns or the diagnosis timeout.
	toolCallCounts := make(map[string]int)
	var dedupWarning string

	for turn := 0; turn < maxTurns; turn++ {
		// v1.2.2: inject dedup warning at top of turn if previous turn
		// detected repeated tool calls. The reminder reaches the LLM via
		// the next Chat() call's message history.
		if dedupWarning != "" {
			messages = append(messages, econtext.Message{
				Role: "user", IsMeta: true,
				Content: "<system-reminder>" + dedupWarning + "</system-reminder>",
			})
			dedupWarning = ""
		}

		// 2a. Compression
		if e.config.EnableCompression && turn > 0 {
			if e.contextManager.ShouldBlock(messages) {
				messages = e.contextManager.ForceCompress(messages)
			} else if compressed, did := e.contextManager.MaybeCompress(messages); did {
				messages = compressed
			}
		}

		// 2b. Turn context (pass tool invocation history for evidence tracking)
		msgsForTurn := e.contextBuilder.InjectTurnContext(messages, turn, maxTurns, result.ToolsInvoked)

		// 2c. Build request
		req := buildRequest(e.adapter, e.contextManager, sysPrompt, msgsForTurn, tools, e.config.DefaultMaxTokens)

		// 2d. Call — try streaming first, fall back to Chat() on failure.
		// After first failed streaming attempt, skip streaming for all subsequent rounds.
		var resp *provider.Response
		var err error
		usedStreamRound := false
		if input.OnStream != nil && !e.streamDisabled {
			usedStreamRound = true
			resp, err = e.streamRound(ctx, req, input.OnStream)
		} else {
			resp, err = e.callWithRetry(ctx, req)
		}
		if err != nil {
			modelErr := e.formatModelError(err)
			result.Errors = append(result.Errors, TurnError{Turn: turn, Error: modelErr})
			return result, fmt.Errorf("%s", modelErr)
		}

		// 2e. Usage
		totalUsage = totalUsage.Add(resp.Usage)

		// 2f. Truncation recovery
		if resp.Truncated && outputRecoveries < e.config.MaxOutputRecoveries {
			truncatedLen := len(resp.Content)
			outputRecoveries++
			if upgraded := e.recoverTruncatedOutput(ctx, sysPrompt, msgsForTurn, tools, resp); upgraded != nil {
				// Push the continuation (new part only) to OnStream so REPL renders it.
				if input.OnStream != nil && len(upgraded.Content) > truncatedLen {
					input.OnStream(upgraded.Content[truncatedLen:])
				}
				resp = upgraded
				totalUsage = totalUsage.Add(upgraded.Usage)
			}
		}

		// v1.2.1: PromptToolAdapter parse-failure retry. If the LLM produced
		// content that LOOKED like a tool_call but failed to parse (malformed
		// JSON, schema violation), PostProcessResponse sets NeedRetry. Engine
		// appends a corrective system-reminder and re-runs the LLM, up to
		// maxParseRetries times. Without this, parse failure silently turns
		// into "treat as final answer" — user sees a JSON snippet as the
		// answer, which is the worst possible UX.
		const maxParseRetries = 2
		if resp.NeedRetry && parseRetries < maxParseRetries {
			parseRetries++
			messages = e.contextBuilder.PrepareMessagesForNextTurn(
				append(messages, econtext.Message{
					Role: "assistant", Content: resp.Content, Thinking: resp.Thinking,
				}), false,
			)
			messages = append(messages, econtext.Message{
				Role: "user", IsMeta: true,
				Content: "<system-reminder>" + resp.RetryFeedback + "</system-reminder>",
			})
			continue
		}

		// 2g. No tool calls → done (final round)
		// streamRound handles OnStream internally; only call it here for the callWithRetry path.
		if len(resp.ToolCalls) == 0 {
			captureDeliverable(resp.Content)
			result.Content = deliverableContent.String()
			result.Thinking = resp.Thinking
			result.TotalUsage = totalUsage
			result.TurnsUsed = turn + 1
			// v1.1.30 fallback: model exited with empty content but had invoked
			// tools. Synthesize a "what was tried, where it stuck" summary so
			// the user gets actionable signal instead of a blank result. Common
			// trigger: small models (35B-class) silently emit EOS when they
			// hit an unsolvable subtask (e.g. complex placeholder SQL).
			if result.Content == "" && len(result.ToolsInvoked) > 0 {
				result.Content = synthesizePartialResult(result.ToolsInvoked, messages, turn+1)
			}
			if input.OnStream != nil && !usedStreamRound && resp.Content != "" {
				input.OnStream(resp.Content)
			}
			// Post-validation: append warning for unverified evidence sources
			if warning := validateEvidenceSources(result.Content, result.ToolsInvoked); warning != "" {
				result.Content += "\n\n" + warning
				if input.OnStream != nil {
					input.OnStream("\n\n" + warning)
				}
			}
			// Append final assistant reply to messages so the saved session
			// includes the model's final diagnosis (was missing — sessions
			// ended at last tool result, losing the actual report).
			messages = append(messages, econtext.Message{
				Role: "assistant", Content: resp.Content, Thinking: resp.Thinking,
			})
			e.triggerMemoryRoundIfNeeded(ctx, messages, input)
			saveSession(messages, turn+1)
			return result, nil
		}

		// 2h. Filter out ghost tool calls (empty ID or Name from streaming deltas)
		validCalls := make([]provider.ToolCall, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			if tc.ID != "" && tc.Name != "" {
				validCalls = append(validCalls, tc)
			}
		}
		resp.ToolCalls = validCalls

		// If all tool calls were ghosts but text indicates tool-call intent,
		// nudge the model to actually emit the tool call instead of ending.
		if len(resp.ToolCalls) == 0 && turn < maxTurns-1 && looksLikeAbortedToolCall(resp.Content) {
			messages = e.contextBuilder.PrepareMessagesForNextTurn(
				append(messages, econtext.Message{
					Role: "assistant", Content: resp.Content, Thinking: resp.Thinking,
				}), false,
			)
			messages = append(messages, econtext.Message{
				Role: "user", IsMeta: true,
				Content: "<system-reminder>你说了要查询但没有实际调用工具。请立即调用对应的工具来执行查询，不要只描述意图。</system-reminder>",
			})
			continue
		}

		// If all tool calls were ghosts, treat as final round
		if len(resp.ToolCalls) == 0 {
			captureDeliverable(resp.Content)
			result.Content = deliverableContent.String()
			result.Thinking = resp.Thinking
			result.TotalUsage = totalUsage
			result.TurnsUsed = turn + 1
			// v1.1.30 fallback: same as 2g — empty content + tools invoked
			// triggers partial-result synthesis instead of a blank return.
			if result.Content == "" && len(result.ToolsInvoked) > 0 {
				result.Content = synthesizePartialResult(result.ToolsInvoked, messages, turn+1)
			}
			if input.OnStream != nil && !usedStreamRound && resp.Content != "" {
				input.OnStream(resp.Content)
			}
			// Append final assistant reply to messages so saved session
			// includes the model's final diagnosis (see 2g comment above).
			messages = append(messages, econtext.Message{
				Role: "assistant", Content: resp.Content, Thinking: resp.Thinking,
			})
			e.triggerMemoryRoundIfNeeded(ctx, messages, input)
			saveSession(messages, turn+1)
			return result, nil
		}

		// Side-effect-only rounds (e.g., model emits the full diagnosis report
		// alongside a trailing memory_write) carry the actual deliverable in
		// resp.Content — capture it so result.Content isn't just the next
		// round's brief wrap-up. shouldFlushContent gates the streaming side;
		// this gates persistence into result.Content.
		if shouldFlushContent(resp.ToolCalls) {
			captureDeliverable(resp.Content)
		}

		// Execute tools
		assistantMsg := econtext.Message{
			Role:     "assistant",
			Content:  resp.Content,
			Thinking: resp.Thinking,
		}
		for _, tc := range resp.ToolCalls {
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, econtext.ToolCall{
				ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
			})
		}
		messages = e.contextBuilder.PrepareMessagesForNextTurn(
			append(messages, assistantMsg), false,
		)

		// v1.2.2: track tool call signatures before execution. If a
		// signature repeats, queue a system-reminder for next turn.
		for _, tc := range resp.ToolCalls {
			sig := toolCallSignature(tc.Name, tc.Arguments)
			toolCallCounts[sig]++
			if toolCallCounts[sig] >= 2 && dedupWarning == "" {
				dedupWarning = fmt.Sprintf(
					"⚠️ 检测到重复工具调用: 你已经用相同参数调用过 `%s` 工具 %d 次. "+
						"重复调用大概率拿到相同结果. 请换一种策略:\n"+
						"  - 如果之前调用失败 → 换工具 (e.g. 失败的 sqltune 换 explain 或 sql)\n"+
						"  - 如果之前已拿到数据 → 直接基于已有数据给最终答案 (格式 B)\n"+
						"  - 如果是 SQL_ID 调优场景: 必须先 sqlfetch 再 sqltune, 别反复调 sqltune",
					tc.Name, toolCallCounts[sig])
				break
			}
		}

		remaining := e.contextManager.RemainingTokens(messages)
		rawResults := e.toolOrch.Execute(ctx, resp.ToolCalls)

		// v1.1.51: passthrough check on RAW (untruncated) tool output.
		// SmartTruncate runs next and can drop content mid-Layer-2, so we
		// detect the WDR/sqltune marker on the original full output and
		// keep it as the user-facing response if found.
		var passthrough string
		for _, tr := range rawResults {
			if tr.Error != "" {
				continue
			}
			if containsPassthroughMarker(tr.Content) {
				passthrough = stripPassthroughMarker(tr.Content)
				break
			}
		}

		toolResults := e.resultHandler.Process(rawResults, remaining)

		// Persist tool results into message history (truncated, marker stripped
		// to prevent re-quote on later session loads).
		for _, tr := range toolResults {
			result.ToolsInvoked = append(result.ToolsInvoked, tr.Name)
			if tr.Error != "" {
				result.Errors = append(result.Errors, TurnError{Turn: turn, Tool: tr.Name, Error: tr.Error})
			}
			content := tr.Content
			if tr.Error != "" {
				content = "Error: " + tr.Error
			}
			if containsPassthroughMarker(content) {
				content = stripPassthroughMarker(content)
			}
			messages = append(messages, econtext.Message{
				Role: "tool", Content: content, ToolCallID: tr.ToolCallID,
			})
		}

		if passthrough != "" {
			// v1.1.54: REPL renders streamed content (not result.Content), so
			// passthrough text was invisible in interactive sessions even
			// though batch mode worked. Push the full report via OnStream so
			// REPL displays it. Add a leading "\n\n" separator in case the
			// LLM already streamed pre-tool reasoning text.
			if input.OnStream != nil {
				input.OnStream("\n\n" + passthrough)
			}
			result.Content = passthrough
			result.TurnsUsed = turn + 1
			result.TotalUsage = totalUsage
			e.triggerMemoryRoundIfNeeded(ctx, messages, input)
			saveSession(messages, maxTurns)
			return result, nil
		}

		if input.OnRound != nil {
			names := make([]string, len(resp.ToolCalls))
			for i, tc := range resp.ToolCalls {
				names[i] = tc.Name
			}
			input.OnRound(turn+1, names)
		}
	}

	// Phase 3: Max turns
	result.MaxTurnsHit = true
	result.TurnsUsed = maxTurns
	result.TotalUsage = totalUsage
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && messages[i].Content != "" {
			result.Content = messages[i].Content
			break
		}
	}
	// Synthesize partial results when content is empty but tools were invoked.
	if result.Content == "" && len(result.ToolsInvoked) > 0 {
		result.Content = synthesizePartialResult(result.ToolsInvoked, messages, maxTurns)
	}
	e.triggerMemoryRoundIfNeeded(ctx, messages, input)
	saveSession(messages, maxTurns)
	return result, nil
}

// triggerMemoryRoundIfNeeded runs the memory-write extra round after diagnosis.
// This is the fallback path: code checks trigger conditions, then gives the LLM
// one chance to write memories. Skipped if memoryStore is nil.
func (e *Engine) triggerMemoryRoundIfNeeded(ctx context.Context, messages []econtext.Message, input EngineInput) {
	if e.memoryStore == nil {
		return
	}

	// Collect user messages and tools called
	var userMsgs []string
	var toolsCalled []string
	for _, m := range messages {
		if m.Role == "user" && !m.IsMeta {
			userMsgs = append(userMsgs, m.Content)
		}
	}
	// ToolsInvoked is tracked in EngineResult but we need it here;
	// extract from messages (assistant tool calls)
	for _, m := range messages {
		for _, tc := range m.ToolCalls {
			toolsCalled = append(toolsCalled, tc.Name)
		}
	}

	if !memory.ShouldTriggerMemoryRound(userMsgs, input.MaxTurns, toolsCalled, string(input.Mode)) {
		return
	}

	// Fire one extra LLM round for memory writing
	memPrompt := econtext.Message{
		Role: "user",
		Content: `诊断已完成。请检查以上对话中是否有尚未保存的重要信息。
如果有遗漏，调用 memory_write 补充保存。可以一次写入多条。
如果之前已经保存过或没有值得记忆的新信息，直接回复"无需补充"。`,
		IsMeta: true,
	}
	memMsgs := make([]econtext.Message, len(messages))
	copy(memMsgs, messages)
	memMsgs = append(memMsgs, memPrompt)

	// Only provide memory_write tool
	memTools := []provider.ToolSchema{{
		Name:        "memory_write",
		Description: "保存一条关于当前数据库实例的记忆",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":    map[string]any{"type": "string", "enum": []string{"incident", "solution", "preference", "workload", "pattern"}},
				"title":   map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"type", "title", "content"},
		},
	}}

	sysPrompt := []econtext.SystemPromptBlock{{Text: "你是记忆助手，负责将诊断中的关键发现保存为记忆。"}}
	req := buildRequest(e.adapter, e.contextManager, sysPrompt, memMsgs, memTools, e.config.DefaultMaxTokens)

	resp, err := e.adapter.Chat(ctx, req)
	if err != nil || resp == nil {
		return
	}

	// Execute any memory_write tool calls
	if len(resp.ToolCalls) > 0 {
		e.toolOrch.Execute(ctx, resp.ToolCalls)
	}
}

// buildTools creates filtered tool schemas based on diagnosis mode.
func (e *Engine) buildTools(mode DiagnoseMode) []provider.ToolSchema {
	if e.toolLister == nil || mode == ModePlaybook {
		return nil
	}

	filter := e.profile.ToolFilter(string(mode))
	allTools := e.toolLister.ListTools()
	schemas := make([]provider.ToolSchema, 0, len(allTools))

	for _, t := range allTools {
		if filter(t.Name, t.SecurityLevel) {
			schemas = append(schemas, provider.ToolSchema{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	return schemas
}

func buildRequest(
	adapter provider.ProviderAdapter,
	ctxMgr *econtext.Manager,
	sysPrompt []econtext.SystemPromptBlock,
	msgs []econtext.Message,
	tools []provider.ToolSchema,
	maxTokens int,
) *provider.Request {
	// Convert context types to provider types
	var sp []provider.SystemPromptBlock
	for _, b := range sysPrompt {
		block := provider.SystemPromptBlock{Text: b.Text}
		if b.CacheControl != nil {
			block.CacheControl = &provider.CacheControl{Type: b.CacheControl.Type, TTL: b.CacheControl.TTL}
		}
		sp = append(sp, block)
	}

	pMsgs := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		pMsgs[i] = provider.Message{
			Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID,
			Thinking: m.Thinking, IsMeta: m.IsMeta,
		}
		for _, tc := range m.ToolCalls {
			pMsgs[i].ToolCalls = append(pMsgs[i].ToolCalls, provider.ToolCall{
				ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
			})
		}
	}

	// Clamp maxTokens to provider's MaxOutputTokens limit
	caps := adapter.Capability()
	if caps != nil && caps.MaxOutputTokens > 0 && maxTokens > caps.MaxOutputTokens {
		maxTokens = caps.MaxOutputTokens
	}

	req := &provider.Request{
		Messages: pMsgs, SystemPrompt: sp, Tools: tools, MaxTokens: maxTokens,
		Extra: make(map[string]any),
	}
	req.Extra["_remaining_budget"] = ctxMgr.RemainingTokens(msgs)
	adapter.EnhanceRequest(req)
	return req
}

// streamRound uses ChatStream to execute one round and buffers text deltas.
// Buffered text is flushed to onStream when:
//   1. Round is final (no tool calls), OR
//   2. Round only triggers side-effect tools (memory_write/update) — these
//      don't return diagnostic data, so the assistant text IS the deliverable.
//
// Without (2), the LLM's full diagnosis report gets silently suppressed when
// the model emits it alongside a trailing memory_write call (observed with
// deepseek-v4-pro: 4 KB analysis hidden because session ended with both
// the report and a memory_write tool call in the same round).
//
// Falls back to non-streaming callWithRetry on any stream error.
func (e *Engine) streamRound(
	ctx context.Context,
	req *provider.Request,
	onStream func(string),
) (*provider.Response, error) {
	resp, chunks, fellBack, err := e.streamFinalResponse(ctx, req)
	if err != nil {
		// ChatStream + Chat both failed
		e.streamDisabled = true
		return e.callWithRetry(ctx, req)
	}
	if fellBack {
		// Streaming not supported by this provider; skip streaming for remaining rounds.
		e.streamDisabled = true
	}

	if onStream != nil && shouldFlushContent(resp.ToolCalls) {
		for _, chunk := range chunks {
			onStream(chunk)
		}
	}
	return resp, nil
}

// shouldFlushContent returns true when the round's text content should be
// streamed to the user. True for final rounds (no tool calls) or rounds that
// only call side-effect tools (memory_write/update) — the latter case is when
// the model puts its final answer + a trailing memory write in one round.
func shouldFlushContent(toolCalls []provider.ToolCall) bool {
	if len(toolCalls) == 0 {
		return true
	}
	for _, tc := range toolCalls {
		if !isSideEffectTool(tc.Name) {
			return false
		}
	}
	return true
}

// isSideEffectTool identifies tools that don't return diagnostic data the
// model needs to reason about further. Content emitted in a round that only
// calls these is the model's final deliverable, not intermediate reasoning.
func isSideEffectTool(name string) bool {
	switch name {
	case "memory_write", "memory_update":
		return true
	}
	return false
}

// streamEventTimeout is the maximum time to wait for a single SSE event.
// If no event arrives within this duration, the stream is considered stalled
// and we fall back to non-streaming Chat(). Covers providers whose SSE
// implementation hangs (e.g., some third-party proxies).
const streamEventTimeout = 60 * time.Second

// streamReasoningOnlyTimeout caps how long a stream can produce ONLY
// reasoning_content chunks (deepseek-v4 / kimi-k2.6 thinking mode) before
// being considered stuck. Without this guard, a model that thinks for many
// minutes without producing any actual content holds the streamFinalResponse
// goroutine indefinitely — to the user it looks like the process hung.
// 180s of pure thinking is a generous upper bound (most reasoning <60s).
const streamReasoningOnlyTimeout = 180 * time.Second

// streamFinalResponse streams the response via ChatStream and collects the
// complete result. Returns (resp, chunks, fellBack, error).
// fellBack=true when streaming was attempted but fell back to Chat().
//
// Three fallback paths to Chat():
//  1. ChatStream() returns error → immediate fallback
//  2. Stream produces nothing (non-SSE body) → fallback after read
//  3. Stream stalls (no event within streamEventTimeout) → timeout fallback
func (e *Engine) streamFinalResponse(
	ctx context.Context,
	req *provider.Request,
) (*provider.Response, []string, bool, error) {
	reqWithStream := *req
	reqWithStream.Stream = true
	stream, err := e.adapter.ChatStream(ctx, &reqWithStream)
	if err != nil {
		// Fallback path 1: ChatStream() failed
		resp, chunks, chatErr := e.chatFallback(ctx, req)
		return resp, chunks, true, chatErr
	}
	defer stream.Close()

	var content strings.Builder
	var thinking strings.Builder
	var chunks []string
	var inThink bool
	var finishReason string

	// Tool call accumulator: streaming sends deltas (partial ID, partial arguments)
	// that must be merged into complete tool calls.
	var toolCalls []provider.ToolCall
	var lastTCIdx int = -1 // index of last tool call being accumulated

	// Reasoning-only watchdog: tracks when content / tool_calls last appeared.
	// If only reasoning_content arrives for >streamReasoningOnlyTimeout, abort
	// — protects against thinking-mode models hanging without producing output.
	streamStart := time.Now()
	lastContentTime := streamStart

	for {
		// Per-event timeout: read in a goroutine with deadline.
		type nextResult struct {
			ev  provider.StreamEvent
			err error
		}
		ch := make(chan nextResult, 1)
		odberr.SafeGo(odberr.ErrLLMRequest, func() {
			ev, evErr := stream.Next()
			ch <- nextResult{ev, evErr}
		})

		// Reasoning-only watchdog: if the model has been streaming pure
		// reasoning_content (thinking) without any content / tool_call for
		// longer than streamReasoningOnlyTimeout, abort — bug surfaced
		// 2026-04-27 with deepseek-v4-pro hanging indefinitely.
		if time.Since(lastContentTime) > streamReasoningOnlyTimeout && thinking.Len() > 0 {
			stream.Close()
			return nil, nil, false, fmt.Errorf(
				"[%s] model produced only thinking for %v (%d bytes), no content / tool_calls — aborting",
				odberr.ErrDiagLLMTimeout,
				time.Since(streamStart).Round(time.Second),
				thinking.Len(),
			)
		}

		var nr nextResult
		select {
		case nr = <-ch:
		case <-time.After(streamEventTimeout):
			// Fallback path 3: stream stalled
			stream.Close()
			if content.Len() == 0 && thinking.Len() == 0 && len(toolCalls) == 0 {
				resp, chunks, chatErr := e.chatFallback(ctx, req)
				return resp, chunks, true, chatErr
			}
			// Partial data received before stall — return what we have
			goto done
		case <-ctx.Done():
			return nil, nil, false, ctx.Err()
		}

		if nr.err != nil {
			// Capture finish reason even on EOF — needed for truncation detection.
			if nr.ev.FinishReason != "" {
				finishReason = nr.ev.FinishReason
			}
			break
		}

		switch nr.ev.Type {
		case provider.StreamTextDelta:
			text := nr.ev.Content
			if !inThink && strings.HasPrefix(strings.TrimSpace(content.String()+text), "<think>") {
				inThink = true
				thinking.WriteString(text)
				continue
			}
			if inThink {
				thinking.WriteString(text)
				if strings.Contains(text, "</think>") {
					inThink = false
					after := text[strings.Index(text, "</think>")+len("</think>"):]
					if trimmed := strings.TrimSpace(after); trimmed != "" {
						content.WriteString(after)
						chunks = append(chunks, after)
					}
				}
				continue
			}
			content.WriteString(text)
			chunks = append(chunks, text)
			lastContentTime = time.Now() // reset reasoning-only watchdog

		case provider.StreamThinkingDelta:
			thinking.WriteString(nr.ev.Content)

		case provider.StreamToolCallDelta:
			lastContentTime = time.Now() // tool calls also reset watchdog
			if nr.ev.ToolCall != nil {
				tc := nr.ev.ToolCall
				if tc.ID != "" {
					// New tool call (has ID) — start accumulating
					toolCalls = append(toolCalls, *tc)
					lastTCIdx = len(toolCalls) - 1
				} else if lastTCIdx >= 0 {
					// Continuation delta (no ID) — append arguments to last tool call
					toolCalls[lastTCIdx].Arguments += tc.Arguments
					if tc.Name != "" {
						toolCalls[lastTCIdx].Name = tc.Name
					}
				}
			}

		case provider.StreamDone:
			finishReason = nr.ev.FinishReason
		}
	}

done:
	// Fallback path 2: stream produced nothing (e.g., non-SSE response body)
	if content.Len() == 0 && thinking.Len() == 0 && len(toolCalls) == 0 {
		resp, chunks, chatErr := e.chatFallback(ctx, req)
		return resp, chunks, true, chatErr
	}

	resp := &provider.Response{
		Content:   content.String(),
		Thinking:  thinking.String(),
		ToolCalls: toolCalls,
		Truncated: finishReason == "length" || finishReason == "max_tokens",
	}
	return resp, chunks, false, nil
}

// chatFallback executes a non-streaming Chat() call and returns the response
// as a single chunk. Used when streaming fails or is unsupported.
func (e *Engine) chatFallback(ctx context.Context, req *provider.Request) (*provider.Response, []string, error) {
	resp, err := e.adapter.Chat(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	var chunks []string
	if resp.Content != "" {
		chunks = []string{resp.Content}
	}
	return resp, chunks, nil
}

func (e *Engine) callWithRetry(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	resp, err := e.retryPolicy.Execute(ctx, func(ctx context.Context) (*provider.Response, error) {
		return e.adapter.Chat(ctx, req)
	})
	if err != nil {
		var httpErr *provider.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == 413 {
			// 413 would need compression — handled by caller
		}
		return nil, err
	}
	return resp, nil
}

// recoverTruncatedOutput attempts to continue a truncated LLM response.
// Uses a 60s timeout and no retry — truncation recovery is best-effort.
// If it fails, the caller returns the truncated content as-is.
func (e *Engine) recoverTruncatedOutput(
	ctx context.Context,
	sysPrompt []econtext.SystemPromptBlock,
	msgs []econtext.Message,
	tools []provider.ToolSchema,
	truncatedResp *provider.Response,
) *provider.Response {
	caps := e.adapter.Capability()
	newMax := e.config.DefaultMaxTokens * 4
	if caps.MaxOutputTokens > 0 && newMax > caps.MaxOutputTokens {
		newMax = caps.MaxOutputTokens
	}

	resumeMsgs := make([]econtext.Message, len(msgs))
	copy(resumeMsgs, msgs)
	resumeMsgs = append(resumeMsgs,
		econtext.Message{Role: "assistant", Content: truncatedResp.Content},
		econtext.Message{
			Role: "user", IsMeta: true,
			Content: "<system-reminder>你的上一条回复因长度限制被截断。请从截断处继续，直接续写内容，不要重复已输出的部分。</system-reminder>",
		},
	)

	// 60s timeout, no retry. Truncation recovery is best-effort;
	// failing just returns the truncated content to the user.
	recoverCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req := buildRequest(e.adapter, e.contextManager, sysPrompt, resumeMsgs, tools, newMax)
	resp, err := e.adapter.Chat(recoverCtx, req)
	if err != nil {
		return nil
	}
	resp.Content = truncatedResp.Content + resp.Content
	return resp
}

// formatModelError produces a user-friendly error message:
// "xxx Model 不可用，原因是 xxx"
func (e *Engine) formatModelError(err error) string {
	modelName := e.adapter.Name()

	// HTTP error with status code from provider
	var httpErr *provider.HTTPError
	if errors.As(err, &httpErr) {
		reason := describeHTTPError(httpErr.StatusCode)
		if httpErr.Body != "" {
			return fmt.Sprintf("%s Model 不可用，原因是 %s (HTTP %d: %s)",
				modelName, reason, httpErr.StatusCode, truncateBody(httpErr.Body, 120))
		}
		return fmt.Sprintf("%s Model 不可用，原因是 %s (HTTP %d)",
			modelName, reason, httpErr.StatusCode)
	}

	// Context timeout or deadline exceeded
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("%s Model 不可用，原因是超时，超过 600 秒无响应", modelName)
	}

	// Network-level timeout (e.g., http.Client.Timeout)
	if isTimeoutError(err) {
		return fmt.Sprintf("%s Model 不可用，原因是超时，超过 600 秒无响应", modelName)
	}

	// Connection refused, DNS failure, etc.
	if strings.Contains(err.Error(), "connection refused") {
		return fmt.Sprintf("%s Model 不可用，原因是连接被拒绝，服务未启动或地址错误", modelName)
	}

	// Generic fallback
	return fmt.Sprintf("%s Model 不可用，原因是 %s", modelName, err.Error())
}

func describeHTTPError(code int) string {
	switch code {
	case 401:
		return "API Key 无效或已过期"
	case 403:
		return "无权访问该模型"
	case 429:
		return "请求频率超限 (rate limit)"
	case 500:
		return "服务端内部错误"
	case 502:
		return "网关错误，服务暂时不可达"
	case 503:
		return "服务暂时不可用"
	case 529:
		return "服务过载"
	default:
		return "请求失败"
	}
}

// isTimeoutError checks if an error is a network-level timeout.
func isTimeoutError(err error) bool {
	type timeouter interface{ Timeout() bool }
	var t timeouter
	if errors.As(err, &t) {
		return t.Timeout()
	}
	return strings.Contains(err.Error(), "timeout") ||
		strings.Contains(err.Error(), "deadline exceeded")
}

func truncateBody(body string, maxLen int) string {
	body = strings.ReplaceAll(body, "\n", " ")
	body = strings.Join(strings.Fields(body), " ")
	if len(body) > maxLen {
		return body[:maxLen] + "..."
	}
	return body
}

// looksLikeAbortedToolCall returns true if the model's text indicates it
// intended to call a tool but the actual tool call was dropped (ghost tool call).
// Common with some providers under streaming where the tool call delta arrives
// incomplete. Detecting this lets the engine nudge the model to retry.
func looksLikeAbortedToolCall(content string) bool {
	trimmed := strings.TrimRight(content, " \t\n\r")
	if len(trimmed) == 0 {
		return false
	}
	// Patterns where the model describes intent to call a tool then stops.
	abortPatterns := []string{
		"让我查询", "让我查看", "让我检查", "让我看看", "让我分析",
		"让我确认", "让我验证", "我来查询", "我来查看", "我来检查",
		"我来验证", "我来分析", "我需要查询", "我需要检查",
		"接下来查询", "先查询", "先检查", "先看看",
	}
	// Check last 60 chars for intent patterns.
	tail := trimmed
	if len(tail) > 60 {
		tail = tail[len(tail)-60:]
	}
	for _, p := range abortPatterns {
		if strings.Contains(tail, p) {
			return true
		}
	}
	// Also check if text ends with "：" or ":" indicating an aborted list/action.
	if strings.HasSuffix(trimmed, "：") || strings.HasSuffix(trimmed, ":") {
		return true
	}
	return false
}

// synthesizePartialResult generates a markdown summary when the engine exhausts
// max turns without producing text output (only tool calls). This prevents
// 0-byte diagnosis files by surfacing what evidence was collected.
func synthesizePartialResult(toolsInvoked []string, messages []econtext.Message, maxTurns int) string {
	// Deduplicate tool names while preserving order.
	seen := make(map[string]bool)
	var unique []string
	for _, t := range toolsInvoked {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}

	var b strings.Builder
	b.WriteString("## \u8bca\u65ad\u4e2d\u95f4\u7ed3\u679c\uff08\u8f6e\u6b21\u8017\u5c3d\uff09\n\n")
	b.WriteString(fmt.Sprintf("\u5df2\u6267\u884c %d \u8f6e\u5de5\u5177\u8c03\u7528\uff0c\u672a\u80fd\u751f\u6210\u6700\u7ec8\u7ed3\u8bba\u3002\n\n", maxTurns))

	b.WriteString("### \u5df2\u6536\u96c6\u7684\u8bc1\u636e\n\n")
	for _, t := range unique {
		b.WriteString("- ")
		b.WriteString(t)
		b.WriteByte('\n')
	}

	// Collect the last few tool output messages (truncated to 500 chars each).
	var lastOutputs []string
	for i := len(messages) - 1; i >= 0 && len(lastOutputs) < 3; i-- {
		if messages[i].Role == "tool" && messages[i].Content != "" {
			out := messages[i].Content
			if len(out) > 500 {
				out = out[:500] + "...(truncated)"
			}
			lastOutputs = append(lastOutputs, out)
		}
	}

	if len(lastOutputs) > 0 {
		b.WriteString("\n### \u6700\u8fd1\u5de5\u5177\u8f93\u51fa\n\n")
		// Reverse to show in chronological order.
		for i := len(lastOutputs) - 1; i >= 0; i-- {
			b.WriteString("```\n")
			b.WriteString(lastOutputs[i])
			b.WriteString("\n```\n\n")
		}
	}

	return b.String()
}

// knownToolNames lists tool names that can appear as evidence sources.
var knownToolNames = []string{
	"health", "activesessions", "sessions", "waits", "locks", "blocktree",
	"latches", "mutexes", "topsql", "slowsql", "explain", "tableinfo",
	"ash", "awr", "planhistory", "params", "space", "segments",
	"tempsess", "undosess", "sortusage", "pga", "sga", "redo", "fra",
	"asm", "jobs", "resource", "alert", "backup", "standby", "os", "sql",
}

// validateEvidenceSources scans the diagnosis text for tool names cited as
// evidence sources and compares against the actual tools invoked.
// Returns a warning string if unverified sources are found, empty string otherwise.
func validateEvidenceSources(content string, toolsInvoked []string) string {
	if content == "" || len(toolsInvoked) == 0 {
		return ""
	}

	invoked := make(map[string]bool)
	for _, t := range toolsInvoked {
		invoked[t] = true
	}

	// Extract only lines near "来源工具" / "来源" context to avoid matching
	// tool-like substrings in arbitrary prose (e.g., "diagnos" matching "os").
	sourceLines := extractSourceLines(content)
	if sourceLines == "" {
		return ""
	}

	var unverified []string
	seen := make(map[string]bool)
	lower := strings.ToLower(sourceLines)
	for _, tool := range knownToolNames {
		if seen[tool] || invoked[tool] {
			continue
		}
		// Word-boundary match to avoid "os" matching inside "chaos", "diagnos", etc.
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(tool) + `\b`)
		if re.MatchString(lower) {
			seen[tool] = true
			unverified = append(unverified, tool)
		}
	}

	if len(unverified) == 0 {
		return ""
	}
	return fmt.Sprintf("⚠ 以下证据来源未经工具验证: %s", strings.Join(unverified, ", "))
}

// extractSourceLines returns lines that contain "来源工具" or "来源" keywords.
// This narrows the search scope so tool name matching only applies to evidence
// citation context, not the entire diagnostic prose.
func extractSourceLines(content string) string {
	var buf strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "来源工具") || strings.Contains(line, "来源:") || strings.Contains(line, "来源：") {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

// passthroughMarker identifies tool results that are already finalized
// markdown reports (sqltune, wdranalyze) and should bypass post-tool LLM
// re-summarization. v1.1.51: smaller models reliably ignored prompt-level
// passthrough directives, so the engine enforces it.
const passthroughMarker = "<!-- WDR_REPORT_BEGIN"

func containsPassthroughMarker(content string) bool {
	return strings.Contains(content, passthroughMarker)
}

// stripPassthroughMarker removes the marker comment line so it doesn't
// leak into user-facing output. Keeps everything after the closing -->.
func stripPassthroughMarker(content string) string {
	idx := strings.Index(content, passthroughMarker)
	if idx == -1 {
		return content
	}
	rest := content[idx:]
	closeIdx := strings.Index(rest, "-->")
	if closeIdx == -1 {
		return content
	}
	out := content[:idx] + rest[closeIdx+3:]
	return strings.TrimLeft(out, "\n")
}

// toolCallSignature returns a string that uniquely identifies a (tool, args)
// pair for cross-turn deduplication. v1.2.2 uses this to detect when the
// LLM is stuck calling the same tool with identical arguments — common
// failure mode for smaller models (35B-class) in prompt mode that don't
// switch strategies after a failed tool.
//
// Normalization:
//   - args JSON is canonicalized via strings.TrimSpace + lowercase the
//     argument value when it's a short string. Real LLMs sometimes emit
//     args with cosmetic whitespace differences for the same intent.
//   - For long args (e.g., SQL text), use first 80 chars of trimmed content
//     so minor formatting tweaks (extra space, casing) don't dodge dedup.
//
// Not cryptographic — collisions are acceptable here (worst case = false
// positive dedup warning, which is recoverable).
func toolCallSignature(name string, args string) string {
	args = strings.TrimSpace(args)
	if len(args) > 80 {
		args = args[:80]
	}
	args = strings.ToLower(args)
	return name + "|" + args
}
