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
	var forcedExpertReport string

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

	roundOffset := 0
	if len(input.ForceInitialToolCalls) > 0 {
		forced := e.executeForcedInitialTools(ctx, messages, result, input.ForceInitialToolCalls)
		if isCurrentDBExpertReport(input) {
			forcedExpertReport = renderCurrentDBExpertReport(input, forced.rawResults, forced.toolResults)
		}
		messages = forced.messages
		if input.OnRound != nil {
			input.OnRound(1, forcedToolNames(input.ForceInitialToolCalls))
			roundOffset = 1
		}
		if forced.passthrough != "" {
			if input.OnStream != nil {
				input.OnStream("\n\n" + forced.passthrough)
			}
			result.Content = forced.passthrough
			result.TurnsUsed = 1
			result.TotalUsage = totalUsage
			e.triggerMemoryRoundIfNeeded(ctx, messages, input)
			saveSession(messages, maxTurns)
			return result, nil
		}
		if input.ForceInitialEvidenceLLM {
			fallback := renderManagedEvidenceFallback(input, forced.rawResults, forced.toolResults)
			content := fallback
			turnsUsed := 2
			if input.OnRound != nil {
				input.OnRound(2, []string{"__llm_evidence_report__"})
			}
			if resp, err := e.generateForcedEvidenceReport(ctx, input, forced.rawResults, forced.toolResults); err == nil && resp != nil && strings.TrimSpace(resp.Content) != "" {
				content = resp.Content
				totalUsage = totalUsage.Add(resp.Usage)
			} else if err != nil {
				content = fallback
			}
			if input.OnStream != nil {
				input.OnStream("\n\n" + content)
			}
			result.Content = content
			result.TurnsUsed = turnsUsed
			result.TotalUsage = totalUsage
			messages = forcedEvidenceSessionMessages(input, content)
			saveSession(messages, turnsUsed)
			return result, nil
		}
		if input.ForceInitialEvidenceSummary {
			summary := renderManagedEvidenceFallback(input, forced.rawResults, forced.toolResults)
			if input.OnStream != nil {
				input.OnStream("\n\n" + summary)
			}
			result.Content = summary
			result.TurnsUsed = 1
			result.TotalUsage = totalUsage
			messages = append(messages, econtext.Message{Role: "assistant", Content: summary})
			saveSession(messages, 1)
			return result, nil
		}
	}
	if isCurrentDBExpertReport(input) && forcedExpertReport != "" && !input.ForceInitialEvidenceLLM {
		messages = append(messages, econtext.Message{
			Role:    "user",
			IsMeta:  true,
			Content: "<system-reminder>" + currentDBExpertReportRubricReminder(forcedExpertReport) + "</system-reminder>",
		})
	}

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
			if input.RequireToolEvidence && len(result.ToolsInvoked) == 0 && turn < maxTurns-1 {
				messages = e.contextBuilder.PrepareMessagesForNextTurn(
					append(messages, econtext.Message{
						Role:     "assistant",
						Content:  resp.Content,
						Thinking: resp.Thinking,
					}), false,
				)
				messages = append(messages, econtext.Message{
					Role:    "user",
					IsMeta:  true,
					Content: "<system-reminder>当前问题必须先调用只读数据库工具采集证据。不要直接给最终诊断。请立即调用 health、activesessions、waits、topsql、slowsql 或 blocktree 中的相关工具。</system-reminder>",
				})
				continue
			}
			captureDeliverable(resp.Content)
			result.Content = deliverableContent.String()
			if isCurrentDBExpertReport(input) && forcedExpertReport != "" {
				before := result.Content
				result.Content = ensureCurrentDBExpertReportQuality(result.Content, forcedExpertReport)
				if input.OnStream != nil && result.Content != before {
					input.OnStream("\n\n" + strings.TrimPrefix(result.Content, before))
				}
			}
			if input.RequireToolEvidence && len(result.ToolsInvoked) == 0 {
				result.Content = "⚠️ 未完成数据库采集，不能给出诊断结论。请先调用 health、activesessions、waits、topsql、slowsql 或 blocktree 等只读工具获取证据。"
			}
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
			if input.RequireToolEvidence && len(result.ToolsInvoked) == 0 && turn < maxTurns-1 {
				messages = e.contextBuilder.PrepareMessagesForNextTurn(
					append(messages, econtext.Message{
						Role:     "assistant",
						Content:  resp.Content,
						Thinking: resp.Thinking,
					}), false,
				)
				messages = append(messages, econtext.Message{
					Role:    "user",
					IsMeta:  true,
					Content: "<system-reminder>当前问题必须先调用只读数据库工具采集证据。不要直接给最终诊断。请立即调用 health、activesessions、waits、topsql、slowsql 或 blocktree 中的相关工具。</system-reminder>",
				})
				continue
			}
			captureDeliverable(resp.Content)
			result.Content = deliverableContent.String()
			if isCurrentDBExpertReport(input) && forcedExpertReport != "" {
				before := result.Content
				result.Content = ensureCurrentDBExpertReportQuality(result.Content, forcedExpertReport)
				if input.OnStream != nil && result.Content != before {
					input.OnStream("\n\n" + strings.TrimPrefix(result.Content, before))
				}
			}
			if input.RequireToolEvidence && len(result.ToolsInvoked) == 0 {
				result.Content = "⚠️ 未完成数据库采集，不能给出诊断结论。请先调用 health、activesessions、waits、topsql、slowsql 或 blocktree 等只读工具获取证据。"
			}
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
						"  - 如果是 SQL_ID 调优场景: sqltune 的 args 只传数字 ID, 不要编造 SQL 文本反复重试",
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
			if content, ok := passthroughToolContent(tr.Name, tr.Content); ok {
				passthrough = content
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
			input.OnRound(turn+1+roundOffset, names)
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
	if input.RequireToolEvidence && len(result.ToolsInvoked) == 0 {
		result.Content = "⚠️ 未完成数据库采集，不能给出诊断结论。请先调用 health、activesessions、waits、topsql、slowsql 或 blocktree 等只读工具获取证据。"
	}
	// Synthesize partial results when content is empty but tools were invoked.
	if result.Content == "" && len(result.ToolsInvoked) > 0 {
		result.Content = synthesizePartialResult(result.ToolsInvoked, messages, maxTurns)
	}
	if isCurrentDBExpertReport(input) && forcedExpertReport != "" {
		result.Content = ensureCurrentDBExpertReportQuality(result.Content, forcedExpertReport)
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

type forcedToolExecution struct {
	messages    []econtext.Message
	passthrough string
	rawResults  []tool.ToolResult
	toolResults []tool.ToolResult
}

func forcedToolNames(calls []provider.ToolCall) []string {
	names := make([]string, 0, len(calls))
	for _, tc := range calls {
		if tc.Name != "" {
			names = append(names, tc.Name)
		}
	}
	return names
}

func (e *Engine) executeForcedInitialTools(
	ctx context.Context,
	messages []econtext.Message,
	result *EngineResult,
	calls []provider.ToolCall,
) forcedToolExecution {
	if len(calls) == 0 || e.toolOrch == nil {
		return forcedToolExecution{messages: messages}
	}

	normalized := make([]provider.ToolCall, 0, len(calls))
	for i, tc := range calls {
		if tc.Name == "" {
			continue
		}
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("forced_%d_%s", i, tc.Name)
		}
		if tc.Arguments == "" {
			tc.Arguments = "{}"
		}
		normalized = append(normalized, tc)
	}
	if len(normalized) == 0 {
		return forcedToolExecution{messages: messages}
	}

	assistantMsg := econtext.Message{
		Role:    "assistant",
		Content: "系统强制先采集必要的数据库证据。",
	}
	for _, tc := range normalized {
		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, econtext.ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		})
	}
	messages = e.contextBuilder.PrepareMessagesForNextTurn(append(messages, assistantMsg), false)

	rawResults := e.toolOrch.Execute(ctx, normalized)

	var passthrough string
	for _, tr := range rawResults {
		if tr.Error != "" {
			continue
		}
		if content, ok := passthroughToolContent(tr.Name, tr.Content); ok {
			passthrough = content
			break
		}
	}

	toolResults := e.resultHandler.Process(rawResults, e.contextManager.RemainingTokens(messages))
	for _, tr := range toolResults {
		result.ToolsInvoked = append(result.ToolsInvoked, tr.Name)
		if tr.Error != "" {
			result.Errors = append(result.Errors, TurnError{Turn: 0, Tool: tr.Name, Error: tr.Error})
		}
		content := tr.Content
		if tr.Error != "" {
			content = "Error: " + tr.Error
		}
		if containsPassthroughMarker(content) {
			content = stripPassthroughMarker(content)
		}
		messages = append(messages, econtext.Message{
			Role:       "tool",
			Content:    content,
			ToolCallID: tr.ToolCallID,
		})
	}
	return forcedToolExecution{
		messages:    messages,
		passthrough: passthrough,
		rawResults:  rawResults,
		toolResults: toolResults,
	}
}

func (e *Engine) generateForcedEvidenceReport(
	ctx context.Context,
	input EngineInput,
	rawResults, toolResults []tool.ToolResult,
) (*provider.Response, error) {
	evidence := renderCompactEvidenceForLLM(input, rawResults, toolResults)
	prompt := fmt.Sprintf(`用户问题:
%s

DBAA_EVIDENCE:
%s

请基于 DBAA_EVIDENCE 输出最终诊断报告，质量必须达到 Opus 专家模板。要求:
1. 只使用证据中的事实，不要补充未采集的数据。
2. 严格区分“当前快照问题”和“历史 Top/Slow SQL 累计统计”。
3. 如果证据显示 health OK、无业务等待、无阻塞链，不要把历史 fault_* / pg_sleep 统计说成当前在线故障。
4. 必须包含这些模块：根因分析总表、当前在线问题、历史 Top/Slow SQL 明细、因果链、需要关注的潜在风险、建议动作、总结。
5. 历史 SQL 明细至少覆盖 DBAA_EVIDENCE 中列出的 SQL_ID / CALLS / AVG_MS / TOTAL_S / 性质判断。
6. 对没有证据支撑的收益、风险、严重程度不要写。
7. 若证据闭环显示“当前无在线故障”，建议动作只能包含只读复核和可选维护说明；不要直接给 reset_unique_sql、pg_stat_reset、TRUNCATE、kill、ALTER SYSTEM 这类变更 SQL。
8. WLM 后台线程只能作为“需确认的后台采集器”，不能和 fault_* 历史 SQL 建因果关系。
`, firstSummaryLine(input.UserMessage), evidence)

	req := buildRequest(
		e.adapter,
		e.contextManager,
		forcedEvidenceSystemPrompt(input),
		[]econtext.Message{{Role: "user", Content: prompt}},
		nil,
		managedEvidenceMaxTokens(e.config.DefaultMaxTokens),
	)
	callCtx, cancel := context.WithTimeout(ctx, managedEvidenceTimeout(input.Capability))
	defer cancel()
	resp, err := e.callWithRetry(callCtx, req)
	if err != nil {
		return nil, err
	}
	if isCurrentDBExpertReport(input) {
		resp.Content = gateCurrentDBReport(resp.Content, rawResults)
	}
	if issue := validateForcedEvidenceReport(resp.Content, rawResults); issue != "" {
		return nil, fmt.Errorf("受控 LLM 报告与采集证据冲突: %s", issue)
	}
	if isCurrentDBExpertReport(input) {
		if issue := validateCurrentDBExpertReportCompleteness(resp.Content); issue != "" {
			return nil, fmt.Errorf("受控 LLM 报告未达到 Opus 模板质量: %s", issue)
		}
	}
	return resp, nil
}

func forcedEvidenceSystemPrompt(input EngineInput) []econtext.SystemPromptBlock {
	return []econtext.SystemPromptBlock{{
		Text: `你是 openGauss / GaussDB DBA 诊断专家。
DBAA 已经完成只读工具采集；你现在只负责基于压缩证据写最终报告，不需要也不能再调用工具。
硬约束:
- 只能引用 DBAA_EVIDENCE 中出现的事实。
- 证据写“当前无阻塞链”时，不得输出“存在阻塞/锁阻塞严重”。
- 证据写“不存在业务等待/仅后台线程活跃”时，不得输出“当前存在 I/O 等待/锁等待/CPU 等待问题”。
- health 项带 OK/✓ 的指标不能被描述成故障。
- Top SQL / Slow SQL 是历史累计统计，除非活跃会话或等待事件也支持，否则不能当成当前在线故障。
- 输出必须达到 Opus 专家报告的信息密度：总表、Top SQL 明细、因果链、风险、动作都要完整。
- 如果当前快照闭环为健康/无等待/无阻塞，不要直接输出 reset_unique_sql、pg_stat_reset、TRUNCATE、kill、ALTER SYSTEM 等变更 SQL；只能写成另行审批的可选维护方向。
输出中文，尽量接近资深 DBA 的判断口吻，给结论也给证据来源。`,
	}}
}

func managedEvidenceMaxTokens(defaultMax int) int {
	if defaultMax <= 0 {
		return 4096
	}
	if defaultMax > 6000 {
		return 6000
	}
	return defaultMax
}

func managedEvidenceTimeout(capability string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(capability)) {
	case "small":
		return 4 * time.Minute
	default:
		return 3 * time.Minute
	}
}

func forcedEvidenceSessionMessages(input EngineInput, content string) []econtext.Message {
	user := firstSummaryLine(input.UserMessage)
	if user == "" {
		user = input.UserMessage
	}
	return []econtext.Message{
		{Role: "user", Content: user},
		{Role: "assistant", Content: content},
	}
}

func prependManagedEvidenceFallbackNote(summary string, err error) string {
	if err == nil {
		return summary
	}
	return "⚠️ 受控 LLM 报告生成失败，已回退为 DBAA 确定性证据摘要。\n原因: " + err.Error() + "\n\n" + summary
}

func renderCompactEvidenceForLLM(input EngineInput, rawResults, toolResults []tool.ToolResult) string {
	results := rawResults
	if len(results) == 0 {
		results = toolResults
	}
	var b strings.Builder
	if q := firstSummaryLine(input.UserMessage); q != "" {
		b.WriteString("USER_QUESTION: " + q + "\n\n")
	}
	if draft := renderCurrentDBExpertReport(input, results, toolResults); strings.TrimSpace(draft) != "" {
		b.WriteString("OPUS_STYLE_EXPERT_DRAFT:\n")
		b.WriteString(draft)
		b.WriteString("\n")
	}

	b.WriteString("HARD_FACTS:\n")
	for _, fact := range hardEvidenceFacts(results) {
		b.WriteString("- " + fact + "\n")
	}
	findings := extractEvidenceFindings(results)
	if len(findings) > 0 {
		b.WriteString("\nCANDIDATE_FINDINGS:\n")
		for _, f := range findings {
			b.WriteString("- " + f + "\n")
		}
	}
	b.WriteString("\nTOOL_STATUS:\n")
	for _, r := range results {
		status := "ok"
		if r.Error != "" {
			status = "error: " + r.Error
		}
		b.WriteString(fmt.Sprintf("- %s: %s", r.Name, status))
		if r.Duration > 0 {
			b.WriteString(fmt.Sprintf(" (%s)", r.Duration.Round(time.Millisecond)))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nTOOL_EVIDENCE:\n")
	totalLimit := 12000
	for _, r := range results {
		b.WriteString("\n[" + r.Name + "]\n")
		if r.Error != "" {
			b.WriteString("ERROR: " + r.Error + "\n")
			continue
		}
		b.WriteString(compactToolEvidence(r.Name, r.Content))
		if b.Len() > totalLimit {
			b.WriteString("\n... evidence compacted to fit model context budget\n")
			break
		}
	}
	return b.String()
}

func hardEvidenceFacts(results []tool.ToolResult) []string {
	var facts []string
	if evidenceHasHealthOK(results) {
		facts = append(facts, "health 显示 Overall OK。")
	}
	if evidenceHasNoBusinessWait(results) {
		facts = append(facts, "waits 显示仅后台线程活跃或不存在业务等待事件。")
	}
	if evidenceHasNoBlocking(results) {
		facts = append(facts, "blocktree 显示当前无阻塞链。")
	}
	if len(facts) == 0 {
		facts = append(facts, "未提取到硬性正常/异常约束，需按工具明细谨慎判断。")
	}
	return facts
}

func compactToolEvidence(name, content string) string {
	content = stripANSI(content)
	content = strings.TrimSpace(content)
	if content == "" {
		return "(empty)\n"
	}
	limit := 1200
	lines := 18
	lineWidth := 220
	switch name {
	case "health":
		limit, lines, lineWidth = 1600, 30, 180
	case "activesessions":
		limit, lines, lineWidth = 1200, 18, 200
	case "waits", "blocktree":
		limit, lines, lineWidth = 900, 16, 200
	case "topsql", "slowsql":
		limit, lines, lineWidth = 1800, 18, 240
	}
	compact := truncateEvidenceLines(content, lines, lineWidth)
	if len([]rune(compact)) > limit {
		compact = compactEvidenceExcerpt(compact, limit)
	}
	return compact + "\n"
}

func truncateEvidenceLines(content string, maxLines, maxLineRunes int) string {
	var out []string
	omitted := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > maxLineRunes {
			line = string(runes[:maxLineRunes]) + "..."
		}
		if len(out) >= maxLines {
			omitted++
			continue
		}
		out = append(out, line)
	}
	if omitted > 0 {
		out = append(out, fmt.Sprintf("... (%d lines omitted)", omitted))
	}
	return strings.Join(out, "\n")
}

func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	return re.ReplaceAllString(s, "")
}

type currentDBMetricRow struct {
	Dimension string
	Data      string
	Source    string
}

type currentDBSQLRow struct {
	Rank           string
	SQLID          string
	Calls          string
	TotalS         string
	AvgMS          string
	Feature        string
	Classification string
}

func renderManagedEvidenceFallback(input EngineInput, rawResults, toolResults []tool.ToolResult) string {
	if isCurrentDBExpertReport(input) {
		return renderCurrentDBExpertReport(input, rawResults, toolResults)
	}
	return renderForcedEvidenceSummary(input, rawResults, toolResults)
}

func isCurrentDBExpertReport(input EngineInput) bool {
	if input.Metadata == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(input.Metadata["expert_report"]), "currentdb")
}

func renderCurrentDBExpertReport(input EngineInput, rawResults, toolResults []tool.ToolResult) string {
	results := rawResults
	if len(results) == 0 {
		results = toolResults
	}
	health := resultContent(results, "health")
	active := resultContent(results, "activesessions")
	waits := resultContent(results, "waits")
	blocktree := resultContent(results, "blocktree")
	topsql := resultContent(results, "topsql")
	slowsql := resultContent(results, "slowsql")

	rows := currentDBMetricRows(health, active, waits, blocktree, results)
	sqlRows := currentDBSQLRows(topsql, slowsql, 6)
	noOnlineFault := evidenceHasHealthOK(results) && evidenceHasNoBusinessWait(results) && evidenceHasNoBlocking(results)

	var b strings.Builder
	b.WriteString("## 根因分析\n\n")
	b.WriteString("| 维度 | 数据 | 来源 |\n")
	b.WriteString("|---|---|---|\n")
	for _, row := range rows {
		b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", escapeMarkdownCell(row.Dimension), escapeMarkdownCell(row.Data), escapeMarkdownCell(row.Source)))
	}
	b.WriteString("\n")
	if noOnlineFault {
		b.WriteString("结论：当前数据库无在线故障，实例健康，无业务等待事件，无阻塞，连接数与 XID 均处于安全范围。\n\n")
	} else {
		b.WriteString("结论：当前数据库存在需要关注的实时信号，请优先结合等待事件、阻塞链和活跃会话确认是否影响业务。\n\n")
	}

	if len(sqlRows) > 0 {
		b.WriteString("但 历史 SQL 统计（topsql / slowsql）暴露了显著的异常负载痕迹，需要关注：\n\n")
		for i, row := range sqlRows {
			b.WriteString(fmt.Sprintf("--- %d/%d #%s ---\n", i+1, len(sqlRows), row.Rank))
			b.WriteString(fmt.Sprintf("SQL 特征: %s (SQL_ID %s)\n", row.Feature, row.SQLID))
			b.WriteString(fmt.Sprintf("CALLS: %s\n", row.Calls))
			b.WriteString(fmt.Sprintf("AVG_MS: %s\n", row.AvgMS))
			b.WriteString(fmt.Sprintf("TOTAL_S: %s\n", row.TotalS))
			b.WriteString(fmt.Sprintf("性质: %s\n\n", row.Classification))
		}
	}

	b.WriteString("## 因果链\n\n")
	if len(sqlRows) > 0 {
		b.WriteString("所有 Top SQL 的高耗时项集中在 fault_* 表（lock / cpu / io / wal）及 pg_sleep，命名模式和 SQL 特征高度一致，更符合故障注入、混沌测试或压测脚本留下的历史负载，而非真实业务 SQL。")
		if activeSummary := firstRegexSubmatch(stripANSI(active), `Active Sessions \(([^)]*)\)`); activeSummary != "" {
			b.WriteString("当前活跃会话只有 " + activeSummary + "。")
		}
		if evidenceHasNoBusinessWait(results) {
			b.WriteString("等待事件快照明确显示不存在业务等待事件。")
		}
		if evidenceHasNoBlocking(results) {
			b.WriteString("阻塞树显示当前无阻塞链。")
		}
		b.WriteString("因此这些记录应作为历史统计沉淀处理，不能直接判定为当前在线故障。\n\n")
	} else {
		b.WriteString("当前采集结果未提取到可展开的 Top/Slow SQL 明细，需结合完整 topsql/slowsql 输出继续判断历史负载来源。\n\n")
	}

	b.WriteString("## 当前在线问题\n\n")
	if noOnlineFault {
		b.WriteString("无。健康检查全通过，无阻塞、无业务等待、无 idle in tx 堆积，连接数充裕，XID 与 dead tuples 处于安全范围。\n\n")
	} else {
		b.WriteString("存在未完全闭环的实时信号。请优先查看等待事件、活跃会话和阻塞链的完整输出，确认是否有业务会话处于等待或被阻塞状态。\n\n")
	}

	b.WriteString("## 需要关注的潜在风险\n\n")
	risks := currentDBRisks(active, sqlRows)
	for i, risk := range risks {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, risk))
	}
	b.WriteString("\n")

	b.WriteString("## 建议动作\n\n")
	for i, action := range currentDBActions(sqlRows, noOnlineFault) {
		b.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, action.Title))
		if action.Body != "" {
			b.WriteString(action.Body + "\n\n")
		}
		if action.SQL != "" {
			b.WriteString("```sql\n" + action.SQL + "\n```\n\n")
		}
	}

	b.WriteString("---\n\n")
	if noOnlineFault {
		b.WriteString("总结：当前没有在线故障，数据库处于健康空闲状态。")
	} else {
		b.WriteString("总结：当前存在需要继续核实的实时信号，请优先按建议动作完成二次确认。")
	}
	if len(sqlRows) > 0 {
		b.WriteString("Top/Slow SQL 中的高耗时项主要源于历史故障注入脚本（fault_* 系列 + pg_sleep），建议确认这些脚本不会再次被触发，避免污染性能基线或冲击业务。")
	}
	if q := firstSummaryLine(input.UserMessage); q != "" {
		b.WriteString("\n\n> 用户问题: " + q + "\n")
	}
	return b.String()
}

func resultContent(results []tool.ToolResult, name string) string {
	for _, r := range results {
		if r.Name == name && r.Error == "" {
			return stripANSI(r.Content)
		}
	}
	return ""
}

func currentDBMetricRows(health, active, waits, blocktree string, results []tool.ToolResult) []currentDBMetricRow {
	var rows []currentDBMetricRow
	add := func(dim, data, src string) {
		data = strings.TrimSpace(data)
		if data == "" {
			return
		}
		rows = append(rows, currentDBMetricRow{Dimension: dim, Data: data, Source: src})
	}
	if evidenceHasHealthOK(results) {
		add("整体健康", "Overall OK (health 检查通过)", "health")
	} else if v := metricValue(health, "Overall"); v != "" {
		add("整体健康", v, "health")
	}
	version := metricValue(health, "Version")
	uptime := metricValue(health, "Uptime")
	if version != "" || uptime != "" {
		add("实例版本", strings.Trim(strings.Join(nonEmptyStrings(version, "运行 "+uptime), "，"), "，"), "health")
	}
	add("缓存命中率", metricValue(health, "Cache Hit Ratio"), "health")
	conn := metricValue(health, "Connections")
	idleTX := metricValue(health, "Idle in TX")
	if conn != "" && idleTX != "" {
		conn += "，Idle in TX = " + strings.Fields(idleTX)[0]
	}
	add("连接数", conn, "health")
	if activeSummary := firstRegexSubmatch(active, `Active Sessions \(([^)]*)\)`); activeSummary != "" {
		add("活跃会话", activeSummary, "activesessions / waits")
	}
	if evidenceHasNoBlocking(results) {
		add("阻塞链", "当前无阻塞链", "blocktree")
	} else if strings.TrimSpace(blocktree) != "" {
		add("阻塞链", firstNonEmptyLine(blocktree), "blocktree")
	}
	add("XID Age", metricValue(health, "XID Age"), "health")
	add("死元组", metricValue(health, "Dead Tuples"), "health")
	return rows
}

func metricValue(content, label string) string {
	if content == "" {
		return ""
	}
	pattern := fmt.Sprintf(`(?im)%s\s*:\s*([^\n│]+)`, regexp.QuoteMeta(label))
	value := firstRegexSubmatch(content, pattern)
	value = strings.TrimSpace(value)
	value = strings.Trim(value, " │")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimSuffix(value, " ✓")
	return value
}

func currentDBSQLRows(topsql, slowsql string, limit int) []currentDBSQLRow {
	rows := parseCurrentDBSQLRows(topsql, limit)
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.SQLID] = true
	}
	if len(rows) < limit {
		for _, row := range parseCurrentDBSQLRows(slowsql, limit) {
			if seen[row.SQLID] {
				continue
			}
			rows = append(rows, row)
			seen[row.SQLID] = true
			if len(rows) >= limit {
				break
			}
		}
	}
	return rows
}

func parseCurrentDBSQLRows(content string, limit int) []currentDBSQLRow {
	content = stripANSI(content)
	re := regexp.MustCompile(`^\s*│\s*(\d+)\s+(\d{5,})\s+(\d+)\s+([0-9.]+)\s+([0-9.]+)\s+\S+\s+(.*?)\s*│?\s*$`)
	var rows []currentDBSQLRow
	for _, line := range strings.Split(content, "\n") {
		m := re.FindStringSubmatch(line)
		if len(m) != 7 {
			continue
		}
		query := strings.TrimSpace(m[6])
		query = strings.TrimSuffix(query, "│")
		row := currentDBSQLRow{
			Rank:           m[1],
			SQLID:          m[2],
			Calls:          m[3],
			TotalS:         m[4],
			AvgMS:          m[5],
			Feature:        summarizeSQLFeature(query),
			Classification: classifySQLFeature(query),
		}
		rows = append(rows, row)
		if len(rows) >= limit {
			break
		}
	}
	return rows
}

func summarizeSQLFeature(query string) string {
	query = strings.Join(strings.Fields(query), " ")
	query = strings.TrimSpace(query)
	if query == "" {
		return "未知 SQL 特征"
	}
	runes := []rune(query)
	if len(runes) > 96 {
		return string(runes[:96]) + "..."
	}
	return query
}

func classifySQLFeature(query string) string {
	lower := strings.ToLower(query)
	switch {
	case strings.Contains(lower, "pg_sleep"):
		return "显式 sleep，疑似故障注入"
	case strings.Contains(lower, "fault_lock") && strings.Contains(lower, "loop"):
		return "锁争用故障演练"
	case strings.Contains(lower, "fault_cpu"):
		return "CPU 压测故障演练"
	case strings.Contains(lower, "fault_io"):
		return "IO 压测故障演练"
	case strings.Contains(lower, "fault_wal"):
		return "WAL 压测故障演练"
	case strings.Contains(lower, "fault_lock"):
		return "单行高频更新"
	case strings.Contains(lower, "fault_"):
		return "故障注入 / 压测脚本"
	default:
		return "历史高耗时 SQL，需结合业务确认"
	}
}

type currentDBAction struct {
	Title string
	Body  string
	SQL   string
}

func currentDBRisks(active string, sqlRows []currentDBSQLRow) []string {
	var risks []string
	if wlm := firstRegexSubmatch(active, `(?m)([0-9.]+h)\s+WLM fetch collect info`); wlm != "" {
		risks = append(risks, "WLM 后台线程已运行 "+wlm+"，虽属后台线程不直接说明业务故障，但建议确认是否为正常常驻采集器。")
	}
	if len(sqlRows) > 0 {
		risks = append(risks, "历史故障注入数据沉淀：dbe_perf.statement 中累积了 fault_* / pg_sleep 的高耗时记录，会污染 Top SQL 排行，掩盖真实业务 SQL 性能问题。")
	}
	if len(risks) == 0 {
		risks = append(risks, "当前未从采集证据中提取到明确潜在风险，保持常规监控即可。")
	}
	return risks
}

func currentDBActions(sqlRows []currentDBSQLRow, noOnlineFault bool) []currentDBAction {
	hasFault := false
	for _, row := range sqlRows {
		lower := strings.ToLower(row.Feature)
		if strings.Contains(lower, "fault_") || strings.Contains(lower, "pg_sleep") {
			hasFault = true
			break
		}
	}
	if !hasFault {
		return []currentDBAction{{
			Title: "保持常规监控",
			Body:  "当前证据未显示在线故障。后续如业务反馈性能异常，重新采集 waits、activesessions、blocktree 和 topsql/slowsql 做对比。",
		}}
	}
	base := []currentDBAction{
		{
			Title: "确认故障注入脚本是否仍在调度",
			SQL: strings.TrimSpace(`-- 检查是否有定时任务在拉起 fault_* 或 pg_sleep 相关逻辑
SELECT jobid, what, last_start_date, next_run_date, broken
FROM pg_job
WHERE what ILIKE '%fault%' OR what ILIKE '%pg_sleep%';

-- 如启用了 pg_cron，再检查 cron 任务
SELECT * FROM cron.job
WHERE command ILIKE '%fault%' OR command ILIKE '%pg_sleep%';`),
		},
	}
	if noOnlineFault {
		return append(base,
			currentDBAction{
				Title: "隔离历史统计噪声（不在当前诊断中直接重置）",
				Body:  "当前快照无在线故障，fault_* / pg_sleep 只是历史累计统计。可先导出 topsql/slowsql 作为复盘材料；是否重置统计应作为单独维护动作审批，当前报告不直接给不可逆重置 SQL。",
			},
			currentDBAction{
				Title: "只读核对 fault_* 表归属",
				Body:  "仅确认这些表是否属于故障演练资产；不要在无业务确认时清空或删除。",
				SQL: strings.TrimSpace(`SELECT relname, n_live_tup, pg_size_pretty(pg_total_relation_size(c.oid)) AS size
FROM pg_class c
JOIN pg_stat_user_tables t ON c.oid = t.relid
WHERE relname LIKE 'fault_%';`),
			},
		)
	}
	return append(base, currentDBAction{
		Title: "按现场流程处理历史统计和演练资产",
		Body:  "若当前快照也显示相关脚本正在运行，应先停止调度或隔离负载；统计重置、表清理等不可逆操作必须另走维护窗口和备份流程。",
	})
}

func currentDBExpertReportRubricReminder(draft string) string {
	return `本次问题必须按 Opus 专家模板输出，信息密度不能低于 DBAA 提供的结构化初稿。
必须包含：根因分析总表、当前在线问题、历史 Top/Slow SQL 明细、因果链、需要关注的潜在风险、建议动作、总结。
如果判断当前无在线故障，也必须展开历史 Top/Slow SQL 前 5-6 条，说明为什么它们只是历史统计而非当前故障。
DBAA 结构化初稿如下，允许你自由组织语言，但不要遗漏其中的关键事实：

` + draft
}

func gateCurrentDBReport(report string, results []tool.ToolResult) string {
	report = strings.ReplaceAll(report, "<br>", "\n")
	report = strings.ReplaceAll(report, "<br/>", "\n")
	report = strings.ReplaceAll(report, "<br />", "\n")
	noOnlineFault := evidenceHasHealthOK(results) && evidenceHasNoBusinessWait(results) && evidenceHasNoBlocking(results)
	if !noOnlineFault {
		return strings.TrimSpace(report)
	}
	dangerous := []string{"reset_unique_sql", "pg_stat_reset", "TRUNCATE TABLE", "ALTER SYSTEM", "/kill ", "pg_terminate_backend"}
	containsDanger := false
	for _, token := range dangerous {
		if strings.Contains(strings.ToLower(report), strings.ToLower(token)) {
			containsDanger = true
			break
		}
	}
	if containsDanger {
		report = hideDangerousCurrentDBSQL(report, dangerous)
	}
	if containsDanger && !strings.Contains(report, "当前快照无在线故障时，变更类动作只允许作为单独维护项") {
		report += "\n\n> 输出门禁：当前快照无在线故障时，统计重置、表清理、kill、ALTER SYSTEM 等变更类动作只允许作为单独维护项审批；本诊断报告仅建议只读复核和历史统计隔离。"
	}
	return strings.TrimSpace(report)
}

func hideDangerousCurrentDBSQL(report string, dangerous []string) string {
	lines := strings.Split(report, "\n")
	inCode := false
	replacedInBlock := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			replacedInBlock = false
			continue
		}
		if !inCode {
			continue
		}
		lower := strings.ToLower(line)
		for _, token := range dangerous {
			if strings.Contains(lower, strings.ToLower(token)) {
				if !replacedInBlock {
					lines[i] = "-- 已隐藏变更类 SQL：当前快照无在线故障，需另走维护审批"
					replacedInBlock = true
				} else {
					lines[i] = ""
				}
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func validateCurrentDBExpertReportCompleteness(report string) string {
	trimmed := strings.TrimSpace(report)
	if trimmed == "" {
		return "空报告"
	}
	required := []string{"根因", "当前", "历史", "建议"}
	for _, token := range required {
		if !strings.Contains(trimmed, token) {
			return "缺少模块或关键词: " + token
		}
	}
	if !(strings.Contains(trimmed, "SQL_ID") || strings.Contains(strings.ToLower(trimmed), "topsql") || strings.Contains(trimmed, "Top SQL")) {
		return "缺少历史 Top/Slow SQL 明细"
	}
	if !(strings.Contains(trimmed, "风险") || strings.Contains(trimmed, "关注")) {
		return "缺少潜在风险或关注点"
	}
	return ""
}

func ensureCurrentDBExpertReportQuality(content, draft string) string {
	if validateCurrentDBExpertReportCompleteness(content) == "" {
		return content
	}
	if strings.TrimSpace(content) == "" {
		return draft
	}
	return strings.TrimSpace(content) + "\n\n---\n\n## DBAA 证据补全\n\n" + draft
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && v != "运行 " {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func escapeMarkdownCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func renderForcedEvidenceSummary(input EngineInput, rawResults, toolResults []tool.ToolResult) string {
	if len(toolResults) == 0 {
		toolResults = rawResults
	}
	var b strings.Builder
	b.WriteString("# 当前数据库问题诊断\n\n")
	b.WriteString("> 已执行 DBAA 确定性证据采集并直接汇总；未进入自由多轮 LLM 推理，避免小模型长时间空转。\n\n")

	findings := extractEvidenceFindings(rawResults)
	if len(findings) == 0 {
		findings = extractEvidenceFindings(toolResults)
	}
	b.WriteString("## 1. 核心发现\n\n")
	if len(findings) == 0 {
		b.WriteString("- 已完成基础证据采集，但未从工具输出中提取到明确高危信号；请查看下方工具明细确认。\n")
	} else {
		for _, finding := range findings {
			b.WriteString("- " + finding + "\n")
		}
	}
	b.WriteString("\n")

	if len(rawResults) > 0 {
		b.WriteString("## 2. 采集状态\n\n")
		for _, r := range rawResults {
			status := "ok"
			if r.Error != "" {
				status = "error"
			}
			b.WriteString(fmt.Sprintf("- %s: %s", r.Name, status))
			if r.Duration > 0 {
				b.WriteString(fmt.Sprintf(" (%s)", r.Duration.Round(time.Millisecond)))
			}
			if r.Error != "" {
				b.WriteString(" - " + r.Error)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## 3. 证据明细\n\n")
	for _, r := range toolResults {
		b.WriteString(fmt.Sprintf("### %s\n\n", r.Name))
		if r.Error != "" {
			b.WriteString("```text\nError: " + r.Error + "\n```\n\n")
			continue
		}
		excerpt := compactEvidenceExcerpt(r.Content, 1800)
		if excerpt == "" {
			excerpt = "(empty result)"
		}
		b.WriteString("```text\n" + excerpt + "\n```\n\n")
	}

	b.WriteString("## 4. 建议动作\n\n")
	actions := suggestEvidenceActions(findings)
	for _, action := range actions {
		b.WriteString("- " + action + "\n")
	}
	if len(actions) == 0 {
		b.WriteString("- 若仍怀疑异常，切换 Opus/GPT 等大模型做二次深度分析，或手动运行对应 slash skill 查看完整明细。\n")
	}

	if strings.TrimSpace(input.UserMessage) != "" {
		b.WriteString("\n> 用户问题: " + firstSummaryLine(input.UserMessage) + "\n")
	}
	return b.String()
}

func extractEvidenceFindings(results []tool.ToolResult) []string {
	var findings []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		findings = append(findings, s)
	}
	for _, r := range results {
		if r.Error != "" {
			add(fmt.Sprintf("%s 工具执行失败: %s", r.Name, r.Error))
			continue
		}
		content := strings.TrimSpace(r.Content)
		lower := strings.ToLower(content)
		switch r.Name {
		case "activesessions":
			if m := firstRegexSubmatch(content, `Active Sessions \(([^)]*)\)`); m != "" {
				add("活跃会话压力: " + m)
			}
			if strings.Contains(lower, "pg_sleep") {
				add("发现 pg_sleep 长连接/睡眠会话，占用连接资源。")
			}
			if strings.Contains(lower, "fault_") {
				add("发现 fault_* 测试/故障注入负载正在运行，可能抢占 CPU、锁或 I/O。")
			}
		case "health":
			if evidenceLineLooksBad(content, "dead tuples", "dead_tuples", "死元组") {
				add("健康检查提示 dead tuples/死元组异常，存在表膨胀或 autovacuum 追不上风险。")
			}
			if evidenceLineLooksBad(content, "temp", "临时文件") {
				add("健康检查提示临时文件或排序溢出相关风险。")
			}
		case "waits":
			noBusinessWait := strings.Contains(content, "不存在业务等待") || strings.Contains(content, "无业务等待") || strings.Contains(content, "仅后台线程活跃")
			if !noBusinessWait && (strings.Contains(lower, "lock") || strings.Contains(content, "锁")) {
				add("等待事件中存在锁相关信号，需要结合 blocktree/activesessions 确认阻塞链。")
			}
			if !noBusinessWait && (strings.Contains(lower, "i/o") || strings.Contains(lower, "io wait") || strings.Contains(lower, "iowait") ||
				strings.Contains(lower, "data file") || strings.Contains(lower, "temp file") || strings.Contains(lower, "buffer") ||
				strings.Contains(content, "IO等待")) {
				add("等待事件中存在 I/O 相关信号，需要检查慢 SQL 和存储压力。")
			}
		case "topsql", "slowsql":
			if strings.Contains(lower, "fault_") {
				add("Top/Slow SQL 中 fault_* 语句占比较高，优先确认是否为压测脚本。")
			}
			if strings.Contains(lower, "pg_sleep") {
				add("Top/Slow SQL 中出现 pg_sleep，优先清理非业务睡眠连接。")
			}
			if strings.Contains(lower, "top sql") || strings.Contains(content, "慢 SQL") {
				add("已采集历史 Top/Slow SQL 统计，需与当前等待/阻塞快照分开判断。")
			}
		case "blocktree":
			noBlock := strings.Contains(content, "当前无阻塞") || strings.Contains(content, "无阻塞链") || strings.Contains(lower, "no blocking")
			if !noBlock && (strings.Contains(lower, "blocked") || strings.Contains(lower, "blocking") || strings.Contains(content, "阻塞")) {
				add("阻塞树存在阻塞/被阻塞信号，需要优先定位根阻塞会话。")
			}
		}
	}
	if len(findings) > 8 {
		return findings[:8]
	}
	return findings
}

func suggestEvidenceActions(findings []string) []string {
	text := strings.ToLower(strings.Join(findings, "\n"))
	var actions []string
	if strings.Contains(text, "fault_*") || strings.Contains(text, "压测") {
		actions = append(actions, "先确认 fault_* 相关语句是否为压测/故障注入；若不是业务流量，立即停止或迁移到隔离环境。")
	}
	if strings.Contains(text, "pg_sleep") {
		actions = append(actions, "清理非业务 pg_sleep 长连接，释放连接槽和会话资源。")
	}
	if strings.Contains(text, "dead tuples") || strings.Contains(text, "死元组") || strings.Contains(text, "膨胀") {
		actions = append(actions, "对死元组严重表先做 autovacuum/ANALYZE 状态确认，再安排低峰清理和 autovacuum 参数修正。")
	}
	if strings.Contains(text, "阻塞树存在") || strings.Contains(text, "根阻塞") || strings.Contains(text, "锁相关信号") || strings.Contains(text, "lock wait") {
		actions = append(actions, "用 blocktree/activesessions 锁定根阻塞会话，先处理根阻塞再处理被阻塞 SQL。")
	}
	if strings.Contains(text, "i/o") || strings.Contains(text, "io") || strings.Contains(text, "临时文件") {
		actions = append(actions, "结合 topsql/slowsql 找到产生 I/O 或临时文件的 SQL，再做索引、统计信息或 SQL 改写。")
	}
	return actions
}

func compactEvidenceExcerpt(content string, limit int) string {
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) <= limit {
		return content
	}
	lines := strings.Split(content, "\n")
	var out []string
	used := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len([]rune(line)) + 1
		if used+n > limit {
			break
		}
		out = append(out, line)
		used += n
	}
	if len(out) == 0 {
		r := []rune(content)
		return string(r[:limit]) + "\n... (truncated)"
	}
	return strings.Join(out, "\n") + "\n... (明细已截断，使用对应 slash skill 可查看完整输出)"
}

func firstRegexSubmatch(s, pattern string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func evidenceLineLooksBad(content string, needles ...string) bool {
	for _, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(line)
		matched := false
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(needle)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if strings.Contains(line, "✓") || strings.Contains(strings.ToLower(line), " ok") {
			continue
		}
		return true
	}
	return false
}

func validateForcedEvidenceReport(report string, results []tool.ToolResult) string {
	report = stripANSI(report)
	if strings.TrimSpace(report) == "" {
		return "模型返回空报告"
	}
	if evidenceHasNoBlocking(results) {
		for _, line := range strings.Split(report, "\n") {
			lower := strings.ToLower(line)
			if !(strings.Contains(line, "阻塞") || strings.Contains(lower, "blocking") || strings.Contains(lower, "blocked")) {
				continue
			}
			if hasNegation(line) {
				continue
			}
			if strings.Contains(line, "历史") || strings.Contains(line, "证据") || strings.Contains(line, "来源") {
				continue
			}
			return "证据显示无阻塞链，但报告疑似声称存在阻塞: " + strings.TrimSpace(line)
		}
	}
	if evidenceHasNoBusinessWait(results) {
		for _, line := range strings.Split(report, "\n") {
			lower := strings.ToLower(line)
			waitClaim := strings.Contains(line, "等待") ||
				strings.Contains(lower, "i/o wait") ||
				strings.Contains(lower, "io wait") ||
				strings.Contains(lower, "lock wait")
			if !waitClaim {
				continue
			}
			if hasNegation(line) || strings.Contains(line, "历史") || strings.Contains(line, "无业务等待") ||
				strings.Contains(line, "建议") || strings.Contains(line, "监控") || strings.Contains(line, "常规") ||
				strings.Contains(line, "后续") || strings.Contains(line, "排查") {
				continue
			}
			if strings.Contains(line, "当前") || strings.Contains(line, "在线") || strings.Contains(line, "严重") || strings.Contains(line, "存在") {
				return "证据显示无业务等待，但报告疑似声称存在当前等待问题: " + strings.TrimSpace(line)
			}
		}
	}
	if evidenceHasHealthOK(results) {
		for _, line := range strings.Split(report, "\n") {
			if !(strings.Contains(line, "health") || strings.Contains(line, "健康") || strings.Contains(line, "实例")) {
				continue
			}
			if hasNegation(line) || strings.Contains(line, "OK") || strings.Contains(line, "正常") ||
				strings.Contains(line, "风险") || strings.Contains(line, "可能") || strings.Contains(line, "若") ||
				strings.Contains(line, "建议") || strings.Contains(line, "导致") || strings.Contains(line, "触发") {
				continue
			}
			if (strings.Contains(line, "当前") || strings.Contains(line, "实例") || strings.Contains(line, "健康")) &&
				(strings.Contains(line, "严重") || strings.Contains(line, "故障") || strings.Contains(line, "异常")) {
				return "证据显示 health OK，但报告疑似声称实例健康异常: " + strings.TrimSpace(line)
			}
		}
	}
	return ""
}

func evidenceHasHealthOK(results []tool.ToolResult) bool {
	for _, r := range results {
		if r.Name != "health" || r.Error != "" {
			continue
		}
		content := stripANSI(r.Content)
		lower := strings.ToLower(content)
		if strings.Contains(lower, "overall:") && (strings.Contains(lower, "ok") || strings.Contains(content, "✓")) {
			return true
		}
		if strings.Contains(content, "19 checks passed") {
			return true
		}
	}
	return false
}

func evidenceHasNoBusinessWait(results []tool.ToolResult) bool {
	for _, r := range results {
		if r.Name != "waits" || r.Error != "" {
			continue
		}
		content := stripANSI(r.Content)
		if strings.Contains(content, "不存在业务等待") || strings.Contains(content, "无业务等待") || strings.Contains(content, "仅后台线程活跃") {
			return true
		}
	}
	return false
}

func evidenceHasNoBlocking(results []tool.ToolResult) bool {
	for _, r := range results {
		if r.Name != "blocktree" || r.Error != "" {
			continue
		}
		content := strings.ToLower(stripANSI(r.Content))
		if strings.Contains(content, "当前无阻塞") || strings.Contains(content, "无阻塞链") || strings.Contains(content, "no blocking") {
			return true
		}
	}
	return false
}

func hasNegation(line string) bool {
	lower := strings.ToLower(line)
	for _, token := range []string{"无", "没有", "不存在", "未发现", "不是", "不代表", "无需", "no ", "not ", "none"} {
		if strings.Contains(lower, strings.ToLower(token)) {
			return true
		}
	}
	return false
}

func firstSummaryLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "用户问题:")
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len([]rune(s)) <= 120 {
		return s
	}
	r := []rune(s)
	return string(r[:120]) + "..."
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
//  1. Round is final (no tool calls), OR
//  2. Round only triggers side-effect tools (memory_write/update) — these
//     don't return diagnostic data, so the assistant text IS the deliverable.
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
		return fmt.Sprintf("%s Model 不可用，原因是诊断累计超时，超过 %d 秒总预算；已取消当前模型请求", modelName, diagnosisTimeoutSeconds(e.config.MaxDiagnosisTimeout))
	}

	// Network-level timeout (e.g., http.Client.Timeout)
	if isTimeoutError(err) {
		return fmt.Sprintf("%s Model 不可用，原因是诊断累计超时，超过 %d 秒总预算；已取消当前模型请求", modelName, diagnosisTimeoutSeconds(e.config.MaxDiagnosisTimeout))
	}

	// Connection refused, DNS failure, etc.
	if strings.Contains(err.Error(), "connection refused") {
		return fmt.Sprintf("%s Model 不可用，原因是连接被拒绝，服务未启动或地址错误", modelName)
	}
	if isContextTooLongError(err) {
		return fmt.Sprintf("%s Model 不可用，原因是上下文超过模型窗口；请压缩会话或切换受控证据模式后重试", modelName)
	}

	// Generic fallback
	return fmt.Sprintf("%s Model 不可用，原因是 %s", modelName, err.Error())
}

func diagnosisTimeoutSeconds(d time.Duration) int {
	if d <= 0 {
		return 600
	}
	return int(d.Round(time.Second).Seconds())
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

func isContextTooLongError(err error) bool {
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "exceed_context_size") ||
		strings.Contains(lower, "exceeds the available context size") ||
		strings.Contains(lower, "context size") ||
		strings.Contains(lower, "context length")
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

// passthroughMarkers identify tool results that are already finalized
// markdown reports (sqltune, wdranalyze) and should bypass post-tool LLM
// re-summarization. v1.1.51: smaller models reliably ignored prompt-level
// passthrough directives, so the engine enforces it.
var passthroughMarkers = []string{
	"<!-- WDR_REPORT_BEGIN",
	"<!-- SQLTUNE_REPORT_BEGIN",
}

func containsPassthroughMarker(content string) bool {
	return passthroughMarkerIndex(content) >= 0
}

func passthroughToolContent(name, content string) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return "", false
	}
	if containsPassthroughMarker(content) {
		return stripPassthroughMarker(content), true
	}
	switch name {
	case "sqltune", "wdranalyze":
		return content, true
	default:
		return "", false
	}
}

// stripPassthroughMarker removes the marker comment line so it doesn't
// leak into user-facing output. Keeps everything after the closing -->.
func stripPassthroughMarker(content string) string {
	idx := passthroughMarkerIndex(content)
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

func passthroughMarkerIndex(content string) int {
	idx := -1
	for _, marker := range passthroughMarkers {
		if markerIdx := strings.Index(content, marker); markerIdx >= 0 && (idx == -1 || markerIdx < idx) {
			idx = markerIdx
		}
	}
	return idx
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
