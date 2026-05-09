/*-------------------------------------------------------------------------
 *
 * alert_renderer.go
 *	  Alert renderer — prints Sentinel anomaly events (timestamp,
 *	  metric, threshold breach summary) above the prompt without
 *	  scrolling away the user's current input.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/alert_renderer.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
)

// renderAlert writes a non-blocking 2-line alert into the REPL content area.
// Called from the main select loop when sentinel pushes an alert.Event.
// Only states facts (metric + values + trigger reason), no conclusions.
func (r *REPL) renderAlert(evt alert.Event) {
	// Capture alert into ring buffer for /diag and /rule dropdowns.
	if r.alertBuf != nil {
		r.alertBuf.Push(AlertEntry{
			Source:    "Sentinel",
			Summary:  evt.Description,
			Timestamp: time.Now(),
			Index:    -1,
		})
	}

	ts := evt.Timestamp.Format("15:04:05")

	// Line 1: pre-formatted alert description
	line1 := accentStyle.Render(fmt.Sprintf("⚠ [%s] %s", ts, evt.Description))

	// Line 2: blocking chain (if any) + action hint
	line2 := ""
	if evt.BlockerSummary != "" {
		line2 = dimStyle.Render(fmt.Sprintf("  %s | 输入 /llm 查看诊断", evt.BlockerSummary))
	} else {
		line2 = dimStyle.Render("  输入 /llm 查看诊断")
	}

	// Write 2 lines (removed "根因:" — rules only state facts, conclusions come from LLM).
	r.writeOutputLine("")
	r.writeOutputLine(line1)
	r.writeOutputLine(line2)

	// Redraw input area to ensure cursor position is correct.
	r.drawInputArea()
}

// autoStartSentinel starts sentinel after successful login (if configured).
func (r *REPL) autoStartSentinel() {
	if r.sentinelSkill == nil || r.cfg == nil {
		return
	}
	if !r.cfg.Sentinel.AutoStart {
		return
	}
	if r.sentinelSkill.IsRunning() {
		return
	}

	if err := r.sentinelSkill.AutoStart(context.Background()); err == nil {
		r.writeOutputLine(dimStyle.Render("Sentinel 哨兵已自动启动 (轻探针 2 SQL/秒, 7 指标)"))
	}
}

// bufferAlert captures the alert for dropdown but defers visual rendering.
// Called when diagRunning or skillRunning to prevent display corruption.
func (r *REPL) bufferAlert(evt alert.Event) {
	// Always push to alertBuf immediately so /llm and /rule dropdowns stay current.
	if r.alertBuf != nil {
		r.alertBuf.Push(AlertEntry{
			Source:    "Sentinel",
			Summary:  evt.Description,
			Timestamp: time.Now(),
			Index:    -1,
		})
	}
	r.pendingAlerts = append(r.pendingAlerts, evt)
}

// stopSentinel stops sentinel if running. Safe to call when nil or not running.
func (r *REPL) stopSentinel() {
	if r.sentinelSkill != nil && r.sentinelSkill.IsRunning() {
		r.sentinelSkill.StopSentinel()
	}
}
