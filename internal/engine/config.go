/*-------------------------------------------------------------------------
 *
 * config.go
 *	  EngineConfig holds configuration for the Engine.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/config.go
 *
 *-------------------------------------------------------------------------
 */
package engine

import (
	"time"

	"github.com/sqlrush/opendb/internal/engine/session"
)

// EngineConfig holds configuration for the Engine.
type EngineConfig struct {
	DefaultMaxTurns     int
	DefaultMaxTokens    int
	ThinkingLevel       string // "low" / "medium" / "high"
	StreamFinalOnly     bool
	EnablePreExecution  bool
	EnableCompression   bool
	MaxOutputRecoveries int
	MaxDiagnosisTimeout time.Duration
}

// DefaultConfig returns sensible defaults for the Engine.
func DefaultConfig() EngineConfig {
	return EngineConfig{
		DefaultMaxTurns:     20,
		DefaultMaxTokens:    80000,
		ThinkingLevel:       "high",
		StreamFinalOnly:     false,
		EnablePreExecution:  true,
		EnableCompression:   true,
		MaxOutputRecoveries: 2,
		MaxDiagnosisTimeout: 10 * time.Minute, // v1.1.19: 5→10min for thinking models (Kimi K2.6 / DeepSeek V4)
	}
}

// DiagnoseMode represents the three diagnosis modes.
type DiagnoseMode string

const (
	ModePlaybook DiagnoseMode = "playbook"
	ModeAssist   DiagnoseMode = "assist"
	ModeAuto     DiagnoseMode = "auto"
)

// IsValid returns true if the mode is recognized.
func (m DiagnoseMode) IsValid() bool {
	switch m {
	case ModePlaybook, ModeAssist, ModeAuto:
		return true
	default:
		return false
	}
}

// EngineInput is the input to Engine.Run().
type EngineInput struct {
	UserMessage      string
	CompressedReport string
	DatabaseInfo     DatabaseInfo
	Metadata         map[string]string
	Mode             DiagnoseMode
	MaxTurns         int
	OnRound          func(turn int, toolNames []string)
	OnStream         func(delta string)
	SessionID        session.SessionID // resume an existing session
	SessionPolicies  string            // session-level policy text
	// ForceInitialToolCalls are executed before the first LLM round. They are
	// persisted as assistant tool_calls + tool messages so the model must base
	// the answer on real evidence even when it would otherwise produce a bare
	// final response.
	ForceInitialToolCalls []ToolCall
	// RequireToolEvidence prevents the first no-tool LLM response from being
	// accepted as final when no tool has run yet.
	RequireToolEvidence bool
	// ForceInitialEvidenceSummary returns a deterministic evidence report after
	// ForceInitialToolCalls finish. It is intended for prompt-mode / smaller
	// models on broad "current database status" questions where another
	// free-form agent loop can consume the full diagnosis timeout.
	ForceInitialEvidenceSummary bool
	// ForceInitialEvidenceLLM runs ForceInitialToolCalls, compacts the evidence,
	// then asks the LLM for a single controlled final report. This is reserved
	// for model families that need DBAA to provide the expert structure (for
	// example local Qwen deployments). If the LLM fails or contradicts hard
	// evidence, Engine falls back to deterministic expert rendering.
	ForceInitialEvidenceLLM bool

	// Capability ("small" / "large", inferred from active model) — drives
	// universal prompt variant selection. v1.1.16+: small/medium models get
	// a templated prompt with concrete fill-in patterns; large models keep
	// the strict v1.1.15 prompt with abstract completion criteria.
	Capability string
}

// DatabaseInfo describes the connected database.
type DatabaseInfo struct {
	Product  string // oracle / mysql / postgres / opengauss
	Version  string
	Instance string
	Host     string
}

// EngineResult is the output of Engine.Run().
type EngineResult struct {
	Content      string
	Thinking     string
	TotalUsage   Usage
	TurnsUsed    int
	MaxTurnsHit  bool
	ToolsInvoked []string
	Errors       []TurnError
}

// TurnError records an error that occurred during a specific turn.
type TurnError struct {
	Turn  int
	Tool  string
	Error string
}
