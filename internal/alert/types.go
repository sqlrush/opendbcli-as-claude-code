/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Package alert defines database-agnostic alert types for the shell
 *	  layer (UI/REPL). Product-specific sentinel implementations convert
 *	  their alerts to these types.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/alert/types.go
 *
 *-------------------------------------------------------------------------
 */
// Package alert defines database-agnostic alert types for the shell layer (UI/REPL).
// Product-specific sentinel implementations convert their alerts to these types.
package alert

import "time"

// Event is pushed to the REPL when any product's sentinel detects an anomaly.
// All fields are pre-formatted by the product — the shell layer only displays them.
type Event struct {
	Timestamp   time.Time `json:"timestamp"`
	Description string    `json:"description"`  // pre-formatted: "活跃会话Active Sessions 2→15个 (3σ阈值8.3)"
	CauseName   string    `json:"cause_name"`   // display name: "锁等待阻塞", "SQL并发冲高"
	DurationSec float64   `json:"duration_sec"`

	// BlockerSummary is a pre-formatted blocker chain description (empty if none).
	// e.g. "SID 142(根阻塞者) → 5个等待"
	BlockerSummary string `json:"blocker_summary,omitempty"`

	// Report is the product-specific detailed report (opaque to shell layer).
	// Cast to concrete type (e.g. *sentinel.BurstReport) in product-specific code.
	Report any `json:"-"`
}
