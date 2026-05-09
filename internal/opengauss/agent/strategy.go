/*-------------------------------------------------------------------------
 *
 * strategy.go
 *	  DiagnoseStrategy selects and executes a diagnosis approach based
 *	  on model capability. Small models use guided diagnosis (assist
 *	  mode, max 3 rounds); large models use autonomous diagnosis (auto
 *	  mode, max 10 rounds) with automatic fallback to guided when the
 *	  loop exhausts without a final answer. This is a direct port of
 *	  Oracle's strategy pattern, ensuring OG gets the same graceful
 *	  degradation Oracle has.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/agent/strategy.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"context"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// DiagnoseStrategy selects and executes a diagnosis approach based on
// model capability. Small models use guided diagnosis (assist mode, max 3
// rounds); large models use autonomous diagnosis (auto mode, max 10 rounds)
// with automatic fallback to guided when the loop exhausts without a final
// answer. This is a direct port of Oracle's strategy pattern, ensuring OG
// gets the same graceful degradation Oracle has.
type DiagnoseStrategy interface {
	Name() string
	Diagnose(ctx context.Context, report *sentinel.BurstReport, userInput string) (DiagnoseResult, error)
}

// GuidedStrategy: orchestrated diagnosis for smaller models.
type GuidedStrategy struct {
	diagnoser *Diagnoser
}

func NewGuidedStrategy(diagnoser *Diagnoser) *GuidedStrategy {
	return &GuidedStrategy{diagnoser: diagnoser}
}

func (g *GuidedStrategy) Name() string { return "guided" }

func (g *GuidedStrategy) SetOnRound(fn OnRoundFunc)   { g.diagnoser.SetOnRound(fn) }
func (g *GuidedStrategy) SetOnStream(fn OnStreamFunc) { g.diagnoser.SetOnStream(fn) }

// SetContextStoresFrom propagates context stores from a source diagnoser.
func (g *GuidedStrategy) SetContextStoresFrom(src *Diagnoser) {
	g.diagnoser.sessionStore = src.sessionStore
	g.diagnoser.memoryStore = src.memoryStore
	g.diagnoser.policyLoader = src.policyLoader
	g.diagnoser.sessionID = src.sessionID
}

func (g *GuidedStrategy) Diagnose(ctx context.Context, report *sentinel.BurstReport, userInput string) (DiagnoseResult, error) {
	result, err := g.diagnoser.Diagnose(ctx, ModeAssist, report, userInput)
	result.Strategy = "guided"
	return result, err
}

// AutonomousStrategy: lets the LLM self-direct with up to 10 rounds. If the
// loop exhausts without convergence (MaxTurnsNote present), falls back to
// GuidedStrategy for a focused re-analysis. This is the key reliability win
// over the previous OG code path which would just return whatever partial
// content was at MaxTurns.
type AutonomousStrategy struct {
	diagnoser *Diagnoser
	fallback  *GuidedStrategy
}

func NewAutonomousStrategy(diagnoser *Diagnoser, fallback *GuidedStrategy) *AutonomousStrategy {
	return &AutonomousStrategy{diagnoser: diagnoser, fallback: fallback}
}

func (a *AutonomousStrategy) Name() string { return "autonomous" }

func (a *AutonomousStrategy) SetOnRound(fn OnRoundFunc) {
	a.diagnoser.SetOnRound(fn)
	a.fallback.SetOnRound(fn)
}

func (a *AutonomousStrategy) SetOnStream(fn OnStreamFunc) {
	a.diagnoser.SetOnStream(fn)
	a.fallback.SetOnStream(fn)
}

// SetContextStoresFrom propagates context stores to both diagnosers so
// session/memory/policy work consistently across primary and fallback.
func (a *AutonomousStrategy) SetContextStoresFrom(d *Diagnoser) {
	a.diagnoser.sessionStore = d.sessionStore
	a.diagnoser.memoryStore = d.memoryStore
	a.diagnoser.policyLoader = d.policyLoader
	a.diagnoser.sessionID = d.sessionID
	a.fallback.diagnoser.sessionStore = d.sessionStore
	a.fallback.diagnoser.memoryStore = d.memoryStore
	a.fallback.diagnoser.policyLoader = d.policyLoader
	a.fallback.diagnoser.sessionID = d.sessionID
}

func (a *AutonomousStrategy) Diagnose(ctx context.Context, report *sentinel.BurstReport, userInput string) (DiagnoseResult, error) {
	result, err := a.diagnoser.Diagnose(ctx, ModeAuto, report, userInput)
	if err != nil {
		result.Strategy = "autonomous"
		return result, err
	}

	if !IsMaxTurnsExceeded(result.Analysis) {
		result.Strategy = "autonomous"
		return result, nil
	}

	fallbackResult, fallbackErr := a.fallback.Diagnose(ctx, report, userInput)
	if fallbackErr != nil {
		result.Strategy = "autonomous"
		return result, nil
	}

	fallbackResult.Strategy = "autonomous"
	fallbackResult.FellBack = true
	return fallbackResult, nil
}

// SelectStrategy creates the appropriate strategy based on model capability.
//
// v1.1.17 tier mapping:
//
//	"large"  → AutonomousStrategy w/ GuidedStrategy fallback, strict prompt
//	"medium" → AutonomousStrategy w/ GuidedStrategy fallback, templated prompt
//	"small"  → GuidedStrategy only, templated prompt
//
// Both medium and large use autonomous tool calling (capable enough). Only
// small models get bottlenecked into 3-round guided mode. Prompt variant
// (strict vs templated) is decided by capability via SetCapability and read
// in builder.go.
func SelectStrategy(
	capability ModelCapability,
	provider llm.Provider,
	executor *skill.Executor,
	registry *skill.Registry,
) DiagnoseStrategy {
	guided := NewGuidedStrategy(NewDiagnoser(provider, executor, registry))
	guided.diagnoser.SetCapability(string(capability))

	if capability == CapabilityLarge || capability == CapabilityMedium {
		autonomous := NewDiagnoser(provider, executor, registry)
		autonomous.SetCapability(string(capability))
		return NewAutonomousStrategy(autonomous, guided)
	}
	return guided
}
