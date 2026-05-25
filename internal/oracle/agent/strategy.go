/*-------------------------------------------------------------------------
 *
 * strategy.go
 *	  ModelCapability describes the reasoning ability of the configured
 *	  LLM.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/strategy.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"context"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// ModelCapability describes the reasoning ability of the configured LLM.
type ModelCapability string

const (
	// CapabilitySmall indicates a small model (≤9B) that benefits from
	// OpenDB-orchestrated, guided diagnosis with limited rounds.
	CapabilitySmall ModelCapability = "small"

	// CapabilityMedium indicates a mid-tier cloud model (GLM/DeepSeek/Qwen
	// /Kimi) — capable of autonomous tool calling but needs the templated
	// universal prompt variant. Added in v1.1.17.
	CapabilityMedium ModelCapability = "medium"

	// CapabilityLarge indicates a frontier model (Opus/Sonnet/GPT-5/Gemini-Pro)
	// capable of internalizing the strict universal prompt's abstract rules.
	CapabilityLarge ModelCapability = "large"
)

// IsValid returns true if the capability is a recognized value.
func (c ModelCapability) IsValid() bool {
	return c == CapabilitySmall || c == CapabilityMedium || c == CapabilityLarge
}

// DiagnoseStrategy selects and executes a diagnosis approach based on
// model capability. Small models use guided diagnosis (OpenDB orchestrates),
// large models use autonomous diagnosis (LLM self-directs).
type DiagnoseStrategy interface {
	// Name returns the strategy identifier ("guided" or "autonomous").
	Name() string
	// Diagnose runs the strategy's diagnosis logic.
	Diagnose(ctx context.Context, report *sentinel.BurstReport, userInput string) (DiagnoseResult, error)
}

// GuidedStrategy uses OpenDB-orchestrated diagnosis for smaller models.
// The LLM operates in assist mode (max 3 rounds, read-only tools) with
// OpenDB compressing the report and providing rule-based remediation.
type GuidedStrategy struct {
	diagnoser *Diagnoser
}

// NewGuidedStrategy creates a GuidedStrategy backed by the given Diagnoser.
func NewGuidedStrategy(diagnoser *Diagnoser) *GuidedStrategy {
	return &GuidedStrategy{diagnoser: diagnoser}
}

func (g *GuidedStrategy) Name() string { return "guided" }

// SetOnRound sets the per-round callback on the underlying diagnoser.
func (g *GuidedStrategy) SetOnRound(fn OnRoundFunc) { g.diagnoser.SetOnRound(fn) }

// SetOnStream sets the streaming callback on the underlying diagnoser.
func (g *GuidedStrategy) SetOnStream(fn OnStreamFunc) { g.diagnoser.SetOnStream(fn) }

func (g *GuidedStrategy) Diagnose(ctx context.Context, report *sentinel.BurstReport, userInput string) (DiagnoseResult, error) {
	result, err := g.diagnoser.Diagnose(ctx, ModeAssist, report, userInput)
	result.Strategy = "guided"
	return result, err
}

// AutonomousStrategy lets the LLM self-direct investigation with up to
// 10 rounds and full tool access. If the loop exhausts all rounds without
// convergence, it falls back to GuidedStrategy for a focused re-analysis.
type AutonomousStrategy struct {
	diagnoser *Diagnoser
	fallback  *GuidedStrategy
}

// NewAutonomousStrategy creates an AutonomousStrategy with the given fallback.
func NewAutonomousStrategy(diagnoser *Diagnoser, fallback *GuidedStrategy) *AutonomousStrategy {
	return &AutonomousStrategy{diagnoser: diagnoser, fallback: fallback}
}

func (a *AutonomousStrategy) Name() string { return "autonomous" }

// SetOnRound sets the per-round callback on the underlying diagnoser.
func (a *AutonomousStrategy) SetOnRound(fn OnRoundFunc) {
	a.diagnoser.SetOnRound(fn)
	a.fallback.SetOnRound(fn)
}

// SetOnStream sets the streaming callback on the underlying diagnosers.
func (a *AutonomousStrategy) SetOnStream(fn OnStreamFunc) {
	a.diagnoser.SetOnStream(fn)
	a.fallback.SetOnStream(fn)
}

func (a *AutonomousStrategy) Diagnose(ctx context.Context, report *sentinel.BurstReport, userInput string) (DiagnoseResult, error) {
	result, err := a.diagnoser.Diagnose(ctx, ModeAuto, report, userInput)
	if err != nil {
		result.Strategy = "autonomous"
		return result, err
	}

	// If the loop converged (LLM gave a final answer within rounds), return.
	if !IsMaxTurnsExceeded(result.Analysis) {
		result.Strategy = "autonomous"
		return result, nil
	}

	// Loop exhausted — fall back to guided strategy.
	fallbackResult, fallbackErr := a.fallback.Diagnose(ctx, report, userInput)
	if fallbackErr != nil {
		// Fallback also failed — return the original autonomous result.
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
