/*-------------------------------------------------------------------------
 *
 * diagnose.go
 *	  DiagnoseResult holds the output of a diagnosis session.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/diagnose.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/engine"
	"github.com/sqlrush/opendb/internal/engine/bridge"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/engine/policy"
	"github.com/sqlrush/opendb/internal/engine/profile"
	"github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/engine/session"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// DiagnoseResult holds the output of a diagnosis session.
type DiagnoseResult struct {
	Mode       DiagnoseMode
	Strategy   string
	FellBack   bool
	Analysis   string
	Thinking   []RoundInfo
	RoundsUsed int
	TokensUsed llm.Usage
}

// AdapterWrapFn wraps a ProviderAdapter for recording/replay.
type AdapterWrapFn func(provider.ProviderAdapter) provider.ProviderAdapter

// Diagnoser orchestrates the three diagnosis modes using the new Engine.
type Diagnoser struct {
	provider     llm.Provider
	executor     *skill.Executor
	registry     *skill.Registry
	onRound      OnRoundFunc
	onStream     OnStreamFunc
	sessionStore session.SessionStore
	memoryStore  *memory.Store
	policyLoader *policy.Loader
	sessionID    session.SessionID
	adapterWrap  AdapterWrapFn // optional: wraps adapter for recording/replay
	capability   string        // "small" / "large" — drives prompt variant
}

// NewDiagnoser creates a Diagnoser with the given dependencies.
// API unchanged — existing callers (diag_skill.go) don't need modification.
func NewDiagnoser(
	provider llm.Provider,
	executor *skill.Executor,
	registry *skill.Registry,
) *Diagnoser {
	return &Diagnoser{
		provider: provider,
		executor: executor,
		registry: registry,
	}
}

// SetAdapterWrap sets an optional adapter wrapper for LLM recording/replay.
func (d *Diagnoser) SetAdapterWrap(fn AdapterWrapFn) { d.adapterWrap = fn }

// SetOnRound sets the per-round progress callback.
func (d *Diagnoser) SetOnRound(fn OnRoundFunc) { d.onRound = fn }

// SetOnStream sets the streaming callback.
func (d *Diagnoser) SetOnStream(fn OnStreamFunc) { d.onStream = fn }

// SetCapability records the model capability so it gets propagated into
// EngineInput → context builder → universal prompt variant selection.
func (d *Diagnoser) SetCapability(cap string) { d.capability = cap }

// SetContextStores injects the session, memory, and policy stores.
func (d *Diagnoser) SetContextStores(baseDir, instance string) {
	d.sessionStore = session.NewFileSessionStore(baseDir + "/sessions")
	d.memoryStore = memory.NewStore(baseDir + "/memory")
	d.memoryStore.SetActiveInstance(instance)
	d.policyLoader = policy.NewLoader(baseDir + "/policies")
	d.sessionID = session.NewSessionID(instance)
}

// SetContextStoresFrom accepts pre-built stores (called by DiagnoseSkill).
func (d *Diagnoser) SetContextStoresFrom(ss session.SessionStore, ms *memory.Store, pl *policy.Loader, sid session.SessionID) {
	d.sessionStore = ss
	d.memoryStore = ms
	d.policyLoader = pl
	d.sessionID = sid
}

// NewSession resets the session ID.
func (d *Diagnoser) NewSession(instance string) {
	if d.memoryStore != nil {
		d.memoryStore.SetActiveInstance(instance)
	}
	d.sessionID = session.NewSessionID(instance)
}

// Diagnose runs diagnosis in the specified mode using the new Engine.
func (d *Diagnoser) Diagnose(
	ctx context.Context,
	mode DiagnoseMode,
	report *sentinel.BurstReport,
	userInput string,
) (DiagnoseResult, error) {
	if !mode.IsValid() {
		return DiagnoseResult{}, fmt.Errorf("invalid diagnose mode: %q", mode)
	}

	// Bridge: wrap old llm.Provider as new ProviderAdapter
	adapter := bridge.WrapLegacyProvider(d.provider)
	if d.adapterWrap != nil {
		adapter = d.adapterWrap(adapter)
	}

	// Bridge: wrap old skill.Executor as new SkillExecutor
	skillBridge := bridge.NewSkillBridge(d.executor, d.registry)

	// Create Engine with Oracle profile
	opts := []engine.Option{}
	if d.sessionStore != nil {
		opts = append(opts, engine.WithSessionStore(d.sessionStore))
	}
	if d.memoryStore != nil {
		opts = append(opts, engine.WithMemoryStore(d.memoryStore))
	}
	if d.policyLoader != nil {
		opts = append(opts, engine.WithPolicyLoader(d.policyLoader))
	}
	eng := engine.New(adapter, profile.NewProfile("oracle"), skillBridge, skillBridge, opts...)

	// Compress report if available
	var compressed string
	if report != nil {
		compressed = CompressReport(*report)
	}

	// Build engine input
	input := engine.EngineInput{
		UserMessage:      userInput,
		CompressedReport: compressed,
		DatabaseInfo:     engine.DatabaseInfo{Product: "oracle"},
		Mode:             engine.DiagnoseMode(mode),
		SessionID:        d.sessionID,
		Capability:       d.capability,
	}

	// Wire callbacks
	if d.onRound != nil {
		input.OnRound = func(turn int, toolNames []string) {
			d.onRound(RoundInfo{
				Round:   turn,
				Summary: fmt.Sprintf("调用 %s", strings.Join(toolNames, ", ")),
			})
		}
	}
	if d.onStream != nil {
		input.OnStream = d.onStream
	}

	// Run Engine
	result, err := eng.Run(ctx, input)
	if err != nil {
		return DiagnoseResult{Mode: mode}, fmt.Errorf("%s diagnosis failed: %w", mode, err)
	}

	analysis := result.Content
	if result.MaxTurnsHit {
		analysis = analysis + "\n" + MaxTurnsNote
	}

	return DiagnoseResult{
		Mode:       mode,
		Analysis:   analysis,
		RoundsUsed: result.TurnsUsed,
		TokensUsed: llm.Usage{
			InputTokens:  result.TotalUsage.InputTokens,
			OutputTokens: result.TotalUsage.OutputTokens,
		},
	}, nil
}

// condenseThinking truncates thinking content to a preview line.
func condenseThinking(thinking string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 1
	}
	s := strings.ReplaceAll(thinking, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen-1]) + "…"
	}
	return s
}

// DiagnoseOnDemand is a simplified entry for on-demand questions (no report).
func (d *Diagnoser) DiagnoseOnDemand(ctx context.Context, question string) (DiagnoseResult, error) {
	return d.Diagnose(ctx, ModeAuto, nil, question)
}

// FormatDiagnoseResult renders the diagnosis result for CLI display.
func FormatDiagnoseResult(dr DiagnoseResult) string {
	var b strings.Builder

	modeLabel := string(dr.Mode)
	if dr.FellBack {
		modeLabel = "auto→guided"
	} else if dr.Strategy != "" {
		modeLabel = dr.Strategy
	}
	b.WriteString(fmt.Sprintf("%s── AI 诊断 (%s, %d轮) ──%s\n",
		ansiBoldCyan, modeLabel, dr.RoundsUsed, ansiReset))

	if len(dr.Thinking) > 0 {
		b.WriteString(RenderThinkingChain(dr.Thinking))
		b.WriteString("\n")
	}

	if dr.Analysis != "" {
		b.WriteString(RenderLLMContent(dr.Analysis))
	}

	b.WriteString(RenderTokenUsage(llmUsage{
		InputTokens:  dr.TokensUsed.InputTokens,
		OutputTokens: dr.TokensUsed.OutputTokens,
	}))

	return b.String()
}
