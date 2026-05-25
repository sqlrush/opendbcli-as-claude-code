/*-------------------------------------------------------------------------
 *
 * engine.go
 *	  Neutral entry point for /sqltune. Lets skills construct a per-
 *	  dialect tuner without importing the dialect package directly.
 *
 *	  Per-dialect packages register a TunerFactory in init() alongside
 *	  their DialectPlanner factory. The skill layer then dispatches:
 *
 *	      engine, err := sqltune.BuildTuner(sqltune.DialectOpenGauss,
 *	          driver, provider, memStore)
 *	      report, err := engine.Tune(ctx, opts)
 *
 *	  As of M1, only og registers. M2-M5 milestones add MySQL / PG /
 *	  GaussDB / Oracle factories. The skill layer (/sqltune) becomes
 *	  dialect-free at the call site.
 *
 *	  Why a separate TunerFactory instead of building from DialectPlanner
 *	  alone: the Tuner needs LLM + memory wiring on top of the planner,
 *	  and the orchestration (Round 1 / verify / Round 2) currently lives
 *	  in the dialect package. M5 (EquivVerifier generalization) and
 *	  beyond may pull the orchestration into this package; until then,
 *	  the factory indirection keeps the seam clean.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/engine.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import (
	"context"
	"fmt"
)

// TunerEngine is implemented by per-dialect Tuner types. Identical
// signature across dialects so /sqltune skill code is dialect-free.
type TunerEngine interface {
	Tune(ctx context.Context, opts TuneOptions) (*FinalReport, error)
}

// TunerFactory builds a TunerEngine bound to a specific dialect.
// deps.Driver is the concrete db.Driver for that dialect — each
// implementation type-asserts it inside the factory.
type TunerFactory func(deps TunerDeps) (TunerEngine, error)

// TunerDeps bundles cross-cutting deps the tuner orchestrator needs.
// Kept here so each dialect's TunerFactory has a stable signature
// regardless of which LLM provider / memory backend is in use.
type TunerDeps struct {
	Driver   any // dialect's db.Driver; planner type-asserts
	Provider any // llm.Provider; tuner type-asserts (kept any to avoid pulling llm pkg into sqltune)
	MemStore any // *memory.Store; nil OK (memory features degrade gracefully)
}

var tunerRegistry = map[DialectKind]TunerFactory{}

// RegisterTuner makes a tuner factory available via BuildTuner.
// Called from per-dialect package init().
func RegisterTuner(k DialectKind, f TunerFactory) { tunerRegistry[k] = f }

// LookupTuner returns the registered tuner factory for k, or nil.
func LookupTuner(k DialectKind) TunerFactory { return tunerRegistry[k] }

// BuildTuner is the skill-layer entry: pick a dialect, get a tuner.
// Returns a typed error when no factory has been registered for kind
// (typically a missing build tag — e.g. "opengauss" tag stripped).
func BuildTuner(kind DialectKind, deps TunerDeps) (TunerEngine, error) {
	f := tunerRegistry[kind]
	if f == nil {
		return nil, fmt.Errorf("sqltune: no tuner registered for dialect %q (missing build tag?)", kind)
	}
	return f(deps)
}
