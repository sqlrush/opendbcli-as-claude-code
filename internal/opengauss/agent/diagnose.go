/*-------------------------------------------------------------------------
 *
 * diagnose.go
 *	  SelectDiagnoseMode is kept for backward compat with callers that
 *	  still need a single mode; new code should use SelectStrategy for
 *	  fallback support.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/agent/diagnose.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/engine"
	"github.com/sqlrush/opendb/internal/engine/bridge"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/engine/policy"
	"github.com/sqlrush/opendb/internal/engine/profile"
	engprovider "github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/engine/session"
	engtool "github.com/sqlrush/opendb/internal/engine/tool"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

type DiagnoseResult struct {
	Mode       DiagnoseMode
	Strategy   string // "guided" / "autonomous"
	FellBack   bool   // true when AutonomousStrategy fell back to GuidedStrategy
	Analysis   string
	RoundsUsed int
	TokensUsed llm.Usage
}

type OnProgressFunc = func(phase, message string, elapsed time.Duration, result *skill.Result, err error)
type DiagProgressFunc = OnProgressFunc

type Diagnoser struct {
	provider     llm.Provider
	executor     *skill.Executor
	registry     *skill.Registry
	onProgress   OnProgressFunc
	onRound      OnRoundFunc
	onStream     OnStreamFunc
	sessionStore session.SessionStore
	memoryStore  *memory.Store
	policyLoader *policy.Loader
	sessionID    session.SessionID
	capability   string // "small" / "large" — drives prompt variant selection
	toolMode     string // "" / "native" / "prompt" — v1.2.0 PromptToolAdapter selector
}

func NewDiagnoser(provider llm.Provider, executor *skill.Executor, registry *skill.Registry) *Diagnoser {
	return &Diagnoser{provider: provider, executor: executor, registry: registry}
}

func (d *Diagnoser) SetOnProgress(fn OnProgressFunc) { d.onProgress = fn }
func (d *Diagnoser) SetOnRound(fn OnRoundFunc)       { d.onRound = fn }
func (d *Diagnoser) SetOnStream(fn OnStreamFunc)     { d.onStream = fn }

// SetCapability records the model capability so it gets propagated into
// EngineInput → context builder → universal prompt variant selection.
func (d *Diagnoser) SetCapability(cap string) { d.capability = cap }

// SetToolMode records the active model's tool_mode for v1.2.0
// PromptToolAdapter selection. "" / "native" → use the provider's native
// FC API; "prompt" → wrap with PromptModeBuilder.
func (d *Diagnoser) SetToolMode(mode string) { d.toolMode = mode }

// promptBuilderOptions returns the bridge.WrapOption list to apply when
// constructing the provider adapter. For native mode this is empty (no-op
// NativeFCBuilder is the default). For prompt mode, builds a fully-wired
// PromptModeBuilder with SceneBasedFilter + tool serializer + known tool
// names for fuzzy correction.
func (d *Diagnoser) promptBuilderOptions(userInput string) []bridge.WrapOption {
	if d.toolMode != "prompt" {
		return nil
	}
	allToolNames := make([]string, 0)
	if d.registry != nil {
		for _, s := range d.registry.All() {
			allToolNames = append(allToolNames, s.Name())
		}
	}
	filter := engtool.NewSceneBasedFilter(engtool.DefaultScenes(), engtool.DefaultAlwaysAvailable())
	builder := engprovider.NewPromptModeBuilder(
		allToolNames,
		engprovider.WithToolFilter(filter.Filter),
		engprovider.WithToolSerializer(engtool.SerializeToolsCompact),
	)
	// Initial turn context. Engine doesn't currently re-set this between
	// rounds; v1.2.1 will plumb LastToolCalls through OnRound.
	builder.SetTurnContext(engprovider.FilterContext{
		UserMessage: userInput,
		Database:    "opengauss",
	})
	return []bridge.WrapOption{bridge.WithPromptBuilder(builder)}
}

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

func (d *Diagnoser) Diagnose(ctx context.Context, mode DiagnoseMode, report *sentinel.BurstReport, userInput string) (DiagnoseResult, error) {
	if !mode.IsValid() {
		return DiagnoseResult{}, fmt.Errorf("invalid diagnose mode: %q", mode)
	}
	var compressed string
	if report != nil {
		compressed = CompressReport(*report)
	}
	return d.runEngine(ctx, mode, userInput, compressed)
}

func (d *Diagnoser) DiagnoseOnDemand(ctx context.Context, userInput string) (DiagnoseResult, error) {
	return d.runEngine(ctx, ModeAuto, userInput, "")
}

func (d *Diagnoser) runEngine(ctx context.Context, mode DiagnoseMode, userInput, compressed string) (DiagnoseResult, error) {
	adapter := bridge.WrapLegacyProvider(d.provider, d.promptBuilderOptions(userInput)...)
	skillBridge := bridge.NewSkillBridge(d.executor, d.registry)
	opts := []engine.Option{}
	if d.sessionStore != nil {
		opts = append(opts, engine.WithSessionStore(d.sessionStore))
	}
	if d.memoryStore != nil {
		opts = append(opts, engine.WithMemoryStore(d.memoryStore))
		// Seed PROFILE.md with an OG-specific template on first diagnosis
		// so the LLM has concrete fields to fill (MOT / CM / WDR /
		// archive mode / capacity profile). Without this, PROFILE.md
		// doesn't exist until the LLM decides to call memory_update —
		// which might never happen on short queries.
		if !d.memoryStore.ProfileExists() {
			_ = d.memoryStore.WriteProfile(memory.ProfileTemplate(d.sessionID.Instance(), "opengauss"))
		}
	}
	if d.policyLoader != nil {
		opts = append(opts, engine.WithPolicyLoader(d.policyLoader))
	}
	eng := engine.New(adapter, profile.NewProfile("opengauss"), skillBridge, skillBridge, opts...)

	input := engine.EngineInput{
		UserMessage: userInput, CompressedReport: compressed,
		DatabaseInfo: engine.DatabaseInfo{Product: "opengauss"},
		Mode:         engine.DiagnoseMode(mode),
		SessionID:    d.sessionID,
		Capability:   d.capability,
	}
	if d.onRound != nil {
		input.OnRound = func(turn int, toolNames []string) {
			d.onRound(RoundInfo{Round: turn, Summary: fmt.Sprintf("调用 %s", strings.Join(toolNames, ", "))})
		}
	}
	if d.onStream != nil {
		input.OnStream = d.onStream
	}

	result, err := eng.Run(ctx, input)
	if err != nil {
		return DiagnoseResult{Mode: mode}, fmt.Errorf("%s diagnosis failed: %w", mode, err)
	}
	analysis := result.Content
	if result.MaxTurnsHit {
		analysis = analysis + "\n" + MaxTurnsNote
	}
	return DiagnoseResult{
		Mode: mode, Analysis: analysis, RoundsUsed: result.TurnsUsed,
		TokensUsed: llm.Usage{InputTokens: result.TotalUsage.InputTokens, OutputTokens: result.TotalUsage.OutputTokens},
	}, nil
}

func FormatDiagnoseResult(dr DiagnoseResult) string {
	var b strings.Builder
	modeLabel := string(dr.Mode)
	if dr.FellBack {
		modeLabel = "auto→guided"
	} else if dr.Strategy != "" {
		modeLabel = dr.Strategy
	}
	b.WriteString(fmt.Sprintf("── AI 诊断 (%s, %d轮) ──\n", modeLabel, dr.RoundsUsed))
	if dr.Analysis != "" {
		b.WriteString(dr.Analysis)
		b.WriteString("\n")
	}
	return b.String()
}

type RouteAnalysis struct {
	Input               string          `json:"input"`
	Intent              string          `json:"intent"`
	Mode                string          `json:"mode"`
	Skill               string          `json:"skill,omitempty"`
	Params              map[string]any  `json:"params,omitempty"`
	UseLLM              bool            `json:"llm_used"`
	Confidence          float64         `json:"confidence,omitempty"`
	Reason              string          `json:"reason"`
	RouteKind           string          `json:"route_kind"`
	ForceInitialTools   bool            `json:"force_initial_tools"`
	RequireToolEvidence bool            `json:"require_tool_evidence"`
	ManagedEvidenceLLM  bool            `json:"managed_evidence_llm"`
	ForcedTools         []RouteToolCall `json:"forced_tools,omitempty"`
	ExpectedFlow        []string        `json:"expected_flow"`
}

type RouteToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func AnalyzeRoute(userInput, capability, toolMode, modelName string) RouteAnalysis {
	return RouteAnalysis{
		Input:     userInput,
		Intent:    "free_llm",
		Mode:      "llm",
		UseLLM:    true,
		Reason:    "main branch uses the LLM agent path; no pre-model forced evidence route is configured",
		RouteKind: "model_decides_tools",
		ExpectedFlow: []string{
			"进入 LLM 诊断流程",
			"把可用工具交给模型",
			"由模型决定是否发起 tool_calls",
			"工具返回后由模型继续分析或输出最终回答",
		},
	}
}

type ModelCapability string

const (
	CapabilitySmall  ModelCapability = "small"
	CapabilityMedium ModelCapability = "medium"
	CapabilityLarge  ModelCapability = "large"
)

// IsValid returns true if the capability is a recognized value.
func (c ModelCapability) IsValid() bool {
	return c == CapabilitySmall || c == CapabilityLarge
}

// SelectDiagnoseMode is kept for backward compat with callers that still need
// a single mode; new code should use SelectStrategy for fallback support.
func SelectDiagnoseMode(capability string) DiagnoseMode {
	if ModelCapability(capability) == CapabilityLarge {
		return ModeAuto
	}
	return ModePlaybook
}
