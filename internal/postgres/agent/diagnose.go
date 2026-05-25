/*-------------------------------------------------------------------------
 *
 * diagnose.go
 *	  diagnose — DiagProgressFunc, DiagnoseResult, ... plus helpers
 *	  (FormatDiagnoseResult, NewDiagnoser, ...) used by the agent
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/agent/diagnose.go
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
	"github.com/sqlrush/opendb/internal/engine/session"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/postgres/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

type DiagnoseResult struct {
	Mode       DiagnoseMode
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
}

func NewDiagnoser(provider llm.Provider, executor *skill.Executor, registry *skill.Registry) *Diagnoser {
	return &Diagnoser{provider: provider, executor: executor, registry: registry}
}

func (d *Diagnoser) SetOnProgress(fn OnProgressFunc) { d.onProgress = fn }
func (d *Diagnoser) SetOnRound(fn OnRoundFunc)       { d.onRound = fn }
func (d *Diagnoser) SetOnStream(fn OnStreamFunc)      { d.onStream = fn }

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

func (d *Diagnoser) emitProgress(phase, message string, elapsed time.Duration, result *skill.Result, err error) {
	if d.onProgress != nil {
		d.onProgress(phase, message, elapsed, result, err)
	}
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
	adapter := bridge.WrapLegacyProvider(d.provider)
	skillBridge := bridge.NewSkillBridge(d.executor, d.registry)
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
	eng := engine.New(adapter, profile.NewProfile("postgres"), skillBridge, skillBridge, opts...)

	input := engine.EngineInput{
		UserMessage: userInput, CompressedReport: compressed,
		DatabaseInfo: engine.DatabaseInfo{Product: "postgres"},
		Mode:         engine.DiagnoseMode(mode),
		SessionID:    d.sessionID,
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
	return DiagnoseResult{
		Mode: mode, Analysis: result.Content, RoundsUsed: result.TurnsUsed,
		TokensUsed: llm.Usage{InputTokens: result.TotalUsage.InputTokens, OutputTokens: result.TotalUsage.OutputTokens},
	}, nil
}

func FormatDiagnoseResult(dr DiagnoseResult) string {
	var b strings.Builder
	modeLabel := string(dr.Mode)
	if modeLabel == "" {
		modeLabel = "playbook"
	}
	b.WriteString(fmt.Sprintf("\033[1;36m── AI 诊断 (%s, %d轮) ──\033[0m\n", modeLabel, dr.RoundsUsed))
	if dr.Analysis != "" {
		b.WriteString(dr.Analysis)
		b.WriteString("\n")
	}
	return b.String()
}

type ModelCapability string

const (
	CapabilitySmall ModelCapability = "small"
	CapabilityLarge ModelCapability = "large"
)

func SelectDiagnoseMode(capability string) DiagnoseMode {
	if ModelCapability(capability) == CapabilityLarge {
		return ModeAuto
	}
	return ModePlaybook
}
