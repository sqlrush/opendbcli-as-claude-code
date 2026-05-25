/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Type aliases bridging og's sqltuner to the neutral sqltune engine.
 *
 *	  Original og-specific definitions of these types now live in
 *	  internal/sqltune/types.go. This file retains the og-namespaced
 *	  names as aliases so existing og sqltuner code (4k lines across
 *	  15 files) keeps compiling unchanged during the M1 refactor.
 *
 *	  Aliases — not new types — so the og package and the neutral
 *	  package interoperate without conversion. Future milestones (M1.3
 *	  onward) will progressively move og code to import the neutral
 *	  package directly; at end of M1 this shim file will be deleted.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/types.go
 *
 *-------------------------------------------------------------------------
 */
// Package sqltuner provides complex SQL performance tuning for OpenGauss/GaussDB.
//
// As of M1, the type system and DialectPlanner interface live in
// internal/sqltune; this package is the og-specific implementation.
// During the transition, exported og types are aliases of the neutral
// types (see this file).
package sqltuner

import "github.com/sqlrush/opendb/internal/sqltune"

// ── Type aliases to neutral sqltune package ────────────────────────────

type (
	PlanNode          = sqltune.PlanNode
	PlanInfo          = sqltune.PlanInfo
	SchemaInfo        = sqltune.SchemaInfo
	TableInfo         = sqltune.TableInfo
	IndexInfo         = sqltune.IndexInfo
	ColStat           = sqltune.ColStat
	ForeignKey        = sqltune.ForeignKey
	DialectInfo       = sqltune.DialectInfo
	RuntimeInfo       = sqltune.RuntimeInfo
	WaitEventBucket   = sqltune.WaitEventBucket
	LockEntry         = sqltune.LockEntry
	MemoryEntry       = sqltune.MemoryEntry
	CollectedContext  = sqltune.CollectedContext
	Round1Output      = sqltune.Round1Output
	Candidate         = sqltune.Candidate
	VerifyResult      = sqltune.VerifyResult
	TuneOptions       = sqltune.TuneOptions
	FinalReport       = sqltune.FinalReport
	ReportStats       = sqltune.ReportStats
)
