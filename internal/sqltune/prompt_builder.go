/*-------------------------------------------------------------------------
 *
 * prompt_builder.go
 *	  PromptBuilder is the per-dialect prompt content provider used by
 *	  the neutral GenericTuner. Lets each dialect inject its own CBO
 *	  knowledge, plan-reading idioms, and hint syntax into the LLM
 *	  system prompt without coupling the orchestrator to any specific
 *	  dialect package.
 *
 *	  Design:
 *	    - GenericTuner assembles the system prompt from 8 sections.
 *	    - 4 sections are universal (output format, taboo phrases,
 *	      principles, dimension diversity requirement).
 *	    - 4 sections are dialect-specific and come from PromptBuilder:
 *	        RoleTag, CBOKnowledge, PlanReading, HintSyntax.
 *	    - Each builder lives in its dialect package (mysql/postgres/
 *	      oracle/gaussdb/opengauss/sqltuner/prompt_builder.go).
 *
 *	  Default behavior — minimal implementations can return short
 *	  paragraphs; richer implementations (og's existing 396-line prompt)
 *	  return the full curated knowledge sections.
 *
 *	  M7 scope: this interface ships; og is NOT migrated to it yet —
 *	  og keeps its existing complex Tuner (with memory/compress/upgrade
 *	  features). MySQL/PG/Oracle/GaussDB get a slim LLM-orchestrated
 *	  tuner via GenericTuner + their PromptBuilder.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/prompt_builder.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import "context"

// PromptBuilder lets each dialect inject its own knowledge sections
// into the system prompt assembled by GenericTuner.
//
// Implementations should return plain text (not markdown frontmatter) —
// GenericTuner wraps each section with its own headers. Returning
// empty strings is OK (the orchestrator skips empty sections); useful
// for dialects where, e.g., a hint syntax cheatsheet doesn't apply.
type PromptBuilder interface {
	// RoleTag identifies the dialect for the role line. Example
	// returns: "OpenGauss 5.0 SQL 调优专家" / "Oracle 19c SQL 调优专家".
	// Drives the very first line of the system prompt.
	RoleTag() string

	// CBOKnowledge returns the dialect's CBO algorithm reference —
	// cost formulas, join algo selection criteria, selectivity model.
	// LLM uses this for cbo_analysis field reasoning. The richer this
	// is, the better the rationale quality.
	CBOKnowledge() string

	// PlanReading returns dialect-specific guidance on interpreting
	// the EXPLAIN output: which operators exist, what their typical
	// failure modes are, what fields to scrutinize. og: "Seq Scan
	// on big table", Oracle: "TABLE ACCESS FULL on big table" etc.
	PlanReading() string

	// HintSyntax returns dialect-specific hint syntax examples for
	// the LLM to use when proposing hint-type candidates. Each
	// dialect's hint syntax differs significantly:
	//   - og/PG: /*+ HASH(t1) */, /*+ LEADING(t1 t2) */
	//   - MySQL: /*+ HASH_JOIN(t1, t2) */ (8.0+), USE INDEX(idx)
	//   - Oracle: /*+ INDEX(t1 idx) */, /*+ LEADING(t1 t2) */
	// Return "" if dialect has no usable hint mechanism.
	HintSyntax() string
}

// LLMCaller is a minimal interface that the neutral GenericTuner
// uses to invoke the LLM. Kept here (not in llm package) so sqltune
// doesn't reverse-import llm — adapter lives in each dialect's
// tuner factory (one-line wrapper from llm.Provider to LLMCaller).
//
// Single method: synchronous chat with a list of messages, returns
// the assistant's reply text. Sufficient for sqltune's Round 1 and
// Round 2 — neither needs streaming.
type LLMCaller interface {
	// Chat sends a single completion request. messages contains the
	// system + user messages. Returns assistant content.
	Chat(ctx context.Context, messages []ChatMessage) (string, error)
}

// ChatMessage is the role+content pair LLMCaller takes. Mirrors
// OpenAI / Anthropic / og's existing llm.Message shape.
type ChatMessage struct {
	Role    string // "system" | "user" | "assistant"
	Content string
}
