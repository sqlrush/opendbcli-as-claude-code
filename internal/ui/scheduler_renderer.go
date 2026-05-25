/*-------------------------------------------------------------------------
 *
 * scheduler_renderer.go
 *	  Scheduler task list renderer — formats the /scheduler ls output
 *	  (name, schedule, last run, status) into an aligned table.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/scheduler_renderer.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/scheduler"
)

// schedulerEventSource provides event channel, auto-start, and stop for scheduler.
// Implemented by admin.SchedulerSkill.
type schedulerEventSource interface {
	EventCh() <-chan scheduler.TaskEvent
	AutoStart() error
	IsRunning() bool
	StopScheduler()
}

// renderSchedulerEvent writes a scheduler task completion event into the REPL.
func (r *REPL) renderSchedulerEvent(ev scheduler.TaskEvent) {
	// Capture warnings and failures into ring buffer for /diag and /rule dropdowns.
	if r.alertBuf != nil {
		if !ev.Success {
			summary := ev.TaskName + ": " + ev.Error
			r.alertBuf.Push(AlertEntry{
				Source:    "Scheduler",
				Summary:  summary,
				Timestamp: time.Now(),
				Index:    -1,
			})
		} else if len(ev.Warnings) > 0 {
			summary := ev.TaskName + ": " + strings.Join(ev.Warnings, "; ")
			r.alertBuf.Push(AlertEntry{
				Source:    "Scheduler",
				Summary:  summary,
				Timestamp: time.Now(),
				Index:    -1,
			})
		}
	}

	ts := ev.StartTime.Format("15:04:05")
	dur := ev.Duration.Round(1e6) // round to ms

	if ev.Success {
		summary := ev.Summary
		if summary == "" {
			summary = "OK"
		}
		line := dimStyle.Render(fmt.Sprintf("  [%s] %s (%s) %s", ts, ev.TaskName, dur, summary))
		r.writeOutputLine(line)
	} else {
		line := accentStyle.Render(fmt.Sprintf("  [%s] %s 失败 (%s)", ts, ev.TaskName, dur))
		r.writeOutputLine(line)
		if ev.Error != "" {
			for _, line := range strings.Split(ev.Error, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					r.writeOutputLine(errorStyle.Render(fmt.Sprintf("    %s", line)))
				}
			}
		}
	}

	r.drawInputArea()
}

// bufferSchedulerEvent captures the event for dropdown but defers visual rendering.
// Called when diagRunning or skillRunning to prevent display corruption.
func (r *REPL) bufferSchedulerEvent(ev scheduler.TaskEvent) {
	// Always push warnings/failures to alertBuf immediately for dropdown freshness.
	if r.alertBuf != nil {
		if !ev.Success {
			r.alertBuf.Push(AlertEntry{
				Source:    "Scheduler",
				Summary:  ev.TaskName + ": " + ev.Error,
				Timestamp: time.Now(),
				Index:    -1,
			})
		} else if len(ev.Warnings) > 0 {
			r.alertBuf.Push(AlertEntry{
				Source:    "Scheduler",
				Summary:  ev.TaskName + ": " + strings.Join(ev.Warnings, "; "),
				Timestamp: time.Now(),
				Index:    -1,
			})
		}
	}
	r.pendingScheds = append(r.pendingScheds, ev)
}

// flushPendingEvents renders all buffered alert/scheduler events that arrived
// during an async operation. Called when diagRunning/skillRunning becomes false.
func (r *REPL) flushPendingEvents() {
	// Render buffered alerts (skip alertBuf push — already captured in bufferAlert).
	for _, evt := range r.pendingAlerts {
		ts := evt.Timestamp.Format("15:04:05")
		line1 := accentStyle.Render(fmt.Sprintf("⚠ [%s] %s", ts, evt.Description))
		line2 := dimStyle.Render("  输入 /llm 查看诊断")
		if evt.BlockerSummary != "" {
			line2 = dimStyle.Render(fmt.Sprintf("  %s | 输入 /llm 查看诊断", evt.BlockerSummary))
		}
		r.writeOutputLine("")
		r.writeOutputLine(line1)
		r.writeOutputLine(line2)
	}
	r.pendingAlerts = r.pendingAlerts[:0]

	// Render buffered scheduler events (skip alertBuf push — already captured).
	for _, ev := range r.pendingScheds {
		ts := ev.StartTime.Format("15:04:05")
		dur := ev.Duration.Round(1e6)
		if ev.Success {
			summary := ev.Summary
			if summary == "" {
				summary = "OK"
			}
			r.writeOutputLine(dimStyle.Render(fmt.Sprintf("  [%s] %s (%s) %s", ts, ev.TaskName, dur, summary)))
		} else {
			r.writeOutputLine(accentStyle.Render(fmt.Sprintf("  [%s] %s 失败 (%s)", ts, ev.TaskName, dur)))
			if ev.Error != "" {
				for _, line := range strings.Split(ev.Error, "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						r.writeOutputLine(errorStyle.Render(fmt.Sprintf("    %s", line)))
					}
				}
			}
		}
	}
	r.pendingScheds = r.pendingScheds[:0]
}

// drainPendingEvents non-blocking reads any queued Sentinel/Scheduler events
// from their channels into the pending buffers. Called after browseTable or
// picker exits to prevent queued events from rendering before drawInputArea.
func (r *REPL) drainPendingEvents() {
	// Drain sentinel alerts.
	if r.sentinelSkill != nil {
		alertCh := r.sentinelSkill.AlertCh()
		for alertCh != nil {
			select {
			case a := <-alertCh:
				r.bufferAlert(a)
			default:
				alertCh = nil
			}
		}
	}
	// Drain scheduler events.
	if r.schedulerSkill != nil {
		schedCh := r.schedulerSkill.EventCh()
		for schedCh != nil {
			select {
			case ev := <-schedCh:
				r.bufferSchedulerEvent(ev)
			default:
				schedCh = nil
			}
		}
	}
}

// flushAfterBlock drains queued events, renders them, and redraws the input area.
// Called after browseTable or picker exits — any events that arrived while the
// main loop was blocked get rendered cleanly below existing content.
func (r *REPL) flushAfterBlock() {
	r.drainPendingEvents()
	if len(r.pendingAlerts) > 0 || len(r.pendingScheds) > 0 {
		r.flushPendingEvents()
	}
	r.drawInputArea()
}

// autoStartScheduler starts scheduler after successful login (if configured).
func (r *REPL) autoStartScheduler() {
	if r.schedulerSkill == nil || r.cfg == nil {
		return
	}
	if !r.cfg.Scheduler.AutoStart {
		return
	}
	if r.schedulerSkill.IsRunning() {
		return
	}

	if err := r.schedulerSkill.AutoStart(); err == nil {
		r.writeOutputLine(dimStyle.Render("Scheduler 巡检引擎已自动启动"))
	}
}

// stopScheduler stops scheduler if running.
func (r *REPL) stopScheduler() {
	if r.schedulerSkill != nil && r.schedulerSkill.IsRunning() {
		r.schedulerSkill.StopScheduler()
	}
}
