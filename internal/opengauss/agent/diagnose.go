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
	modelName    string // active /model entry name; used for model-family routing only
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

// SetModelName records the active /model entry name. This is intentionally
// separate from provider.Name(): multiple local/cloud models share the same
// OpenAI-compatible provider but need different reliability paths.
func (d *Diagnoser) SetModelName(name string) { d.modelName = name }

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
		Metadata: map[string]string{
			"active_model": d.modelName,
			"tool_mode":    d.toolMode,
		},
	}
	// SQL_ID tuning is a product-level deterministic route, not a model
	// selection problem. Small prompt-mode models routinely invent example
	// SQL after sqlfetch; pass the numeric ID to sqltune and let sqltune's
	// own resolver fetch the real statement.
	if sqlID, ok := shouldForceSQLTune(userInput); ok {
		input.ForceInitialToolCalls = sqlTuneToolCalls(sqlID)
		input.RequireToolEvidence = true
	} else if shouldForceCurrentDBEvidence(userInput) {
		input.ForceInitialToolCalls = currentDBEvidenceToolCalls()
		input.RequireToolEvidence = true
		input.ForceInitialEvidenceLLM = shouldUseCurrentDBManagedEvidenceLLM(d.capability, d.toolMode, d.modelName)
		input.Metadata["expert_report"] = "currentdb"
	}
	if d.onRound != nil {
		input.OnRound = func(turn int, toolNames []string) {
			d.onRound(RoundInfo{Round: turn, Summary: formatEngineRoundSummary(toolNames)})
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

func formatEngineRoundSummary(toolNames []string) string {
	if len(toolNames) == 1 && toolNames[0] == "__llm_evidence_report__" {
		return "基于已采集证据生成诊断报告"
	}
	if len(toolNames) == 0 {
		return "继续分析"
	}
	return fmt.Sprintf("调用 %s", strings.Join(toolNames, ", "))
}

func shouldForceCurrentDBEvidence(userInput string) bool {
	lower := strings.ToLower(userInput)
	triggers := []string{
		"当前数据库", "数据库当前", "有哪些问题", "有没有问题", "存在什么问题",
		"当前状态", "健康", "异常", "性能问题", "数据库慢", "响应慢",
		"连接数", "慢查询", "锁等待", "阻塞", "等待事件", "cpu", "内存", "i/o", "io等待", "io wait",
		"database status", "current database", "health", "performance issue",
	}
	for _, trigger := range triggers {
		if strings.Contains(lower, strings.ToLower(trigger)) {
			return true
		}
	}
	return false
}

func shouldUseCurrentDBFastSummary(capability, toolMode string) bool {
	capability = strings.ToLower(strings.TrimSpace(capability))
	toolMode = strings.ToLower(strings.TrimSpace(toolMode))
	if toolMode == "prompt" {
		return true
	}
	return capability == "" || capability == string(CapabilitySmall) || capability == string(CapabilityMedium)
}

func shouldUseCurrentDBManagedEvidenceLLM(capability, toolMode, modelName string) bool {
	capability = strings.ToLower(strings.TrimSpace(capability))
	toolMode = strings.ToLower(strings.TrimSpace(toolMode))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if isManagedQwenModel(modelName) {
		return true
	}
	if modelName == "" {
		return toolMode == "prompt" || capability == string(CapabilitySmall)
	}
	return false
}

func isManagedQwenModel(modelName string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	for _, marker := range []string{"qwen", "qwq"} {
		if strings.Contains(modelName, marker) {
			return true
		}
	}
	return false
}

func shouldForceSQLTune(userInput string) (string, bool) {
	text := strings.TrimSpace(userInput)
	if text == "" || startsWithSQLKeyword(text) {
		return "", false
	}
	lower := strings.ToLower(text)
	if !hasSQLTuneIntent(lower) {
		return "", false
	}
	if hasSQLIDLabel(lower) {
		if id, ok := firstDigitRun(text, 1, 25); ok {
			return id, true
		}
		return "", false
	}
	runs := digitRuns(text, 6, 25)
	if len(runs) == 1 {
		return runs[0], true
	}
	return "", false
}

func sqlTuneToolCalls(sqlID string) []engprovider.ToolCall {
	return []engprovider.ToolCall{{
		ID:        "forced_sqltune_0_sqltune",
		Name:      "sqltune",
		Arguments: fmt.Sprintf(`{"args":"%s","mode":"quick"}`, sqlID),
	}}
}

func hasSQLTuneIntent(lower string) bool {
	for _, kw := range []string{
		"优化", "调优", "如何改", "怎么改", "执行计划", "慢", "性能",
		"tune", "tuning", "optimize", "explain", "plan",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func hasSQLIDLabel(lower string) bool {
	for _, label := range []string{"sql_id", "sql id", "sqlid"} {
		if strings.Contains(lower, label) {
			return true
		}
	}
	return false
}

func startsWithSQLKeyword(s string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "select", "with", "insert", "update", "delete", "merge", "explain", "create", "alter", "drop", "truncate", "do", "call":
		return true
	default:
		return false
	}
}

func firstDigitRun(s string, minLen, maxLen int) (string, bool) {
	runs := digitRuns(s, minLen, maxLen)
	if len(runs) == 0 {
		return "", false
	}
	return runs[0], true
}

func digitRuns(s string, minLen, maxLen int) []string {
	var runs []string
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		if n := end - start; n >= minLen && n <= maxLen {
			runs = append(runs, s[start:end])
		}
		start = -1
	}
	for i, r := range s {
		if r >= '0' && r <= '9' {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
	}
	flush(len(s))
	return runs
}

func currentDBEvidenceToolCalls() []engprovider.ToolCall {
	names := []string{"health", "activesessions", "waits", "topsql", "slowsql", "blocktree"}
	calls := make([]engprovider.ToolCall, 0, len(names))
	for i, name := range names {
		args := "{}"
		switch name {
		case "topsql":
			args = `{"args":"el"}`
		case "slowsql":
			args = `{"args":"1000"}`
		}
		calls = append(calls, engprovider.ToolCall{
			ID:        fmt.Sprintf("forced_current_%d_%s", i, name),
			Name:      name,
			Arguments: args,
		})
	}
	return calls
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
