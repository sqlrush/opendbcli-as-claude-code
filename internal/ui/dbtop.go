/*-------------------------------------------------------------------------
 *
 * dbtop.go
 *	  /dbtop — top(1)-style live view of database load (active sessions,
 *	  Top SQL, blockers) refreshed every second. Reuses the same probe
 *	  skills as Sentinel.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/dbtop.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/skill"
	"golang.org/x/term"
)

// runDbtop enters the real-time monitoring mode.
// First frame written via writeOutputLine (handles scrolling).
// Subsequent frames overwrite in-place via ANSI cursor positioning.
// Blocks until user presses q/ESC/Ctrl+C.
func (r *REPL) runDbtop(src skill.DbtopRefreshSource) {
	intervalSec := src.DbtopInterval()
	if intervalSec < 1 {
		intervalSec = 1
	}

	loop := src.NewDbtopLoop()
	interval := time.Duration(intervalSec) * time.Second

	// First frame.
	lines := loop.RenderFrame(context.Background(), r.cols, intervalSec)

	// Write first frame via writeOutputLine to handle scroll positioning.
	// Record the row where the FIRST line of the frame lands.
	firstLineRow := r.contentRow
	for _, line := range lines {
		r.writeOutputLine(line)
	}

	// startRow = where the first line was actually written.
	// This is the anchor for all subsequent in-place refreshes.
	startRow := firstLineRow
	if r.scrollMode {
		maxRow := r.maxContentRow()
		startRow = maxRow - len(lines) + 1
		if startRow < 1 {
			startRow = 1
		}
	}

	// Hide cursor during refresh.
	termHideCursor(r.writer)

	// Refresh loop with keyboard handling.
	// Reuse REPL's keyCh to avoid dual stdin readers (which causes key leaks).
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Sentinel alert channel — nil if sentinel not running (select ignores nil channels).
	var alertCh <-chan alert.Event
	if r.sentinelSkill != nil {
		alertCh = r.sentinelSkill.AlertCh()
	}

	// alertLine holds the latest sentinel alert text to overlay on dbtop.
	alertLine := ""

	exitDbtop := false
	for !exitDbtop {
		select {
		case ke := <-r.keyCh:
			if ke.err != nil {
				exitDbtop = true
				break
			}
			for i := 0; i < ke.n; i++ {
				switch ke.buf[i] {
				case 'q', 'Q', 3: // q, Q, Ctrl+C
					exitDbtop = true
				case 27: // ESC
					if i+1 < ke.n && ke.buf[i+1] == '[' {
						i += 2 // skip CSI sequence
						continue
					}
					exitDbtop = true
				}
			}
		case <-ticker.C:
			lines = loop.RenderFrame(context.Background(), r.cols, intervalSec)
			for i, line := range lines {
				termWriteAt(r.writer, startRow+i, line)
			}
			// Re-render alert overlay if present.
			if alertLine != "" {
				termWriteAt(r.writer, startRow+len(lines), alertLine)
			}
			r.writer.Flush()
		case alert := <-alertCh:
			alertLine = formatDbtopAlert(alert, r.cols)
			termWriteAt(r.writer, startRow+len(lines), alertLine)
			r.writer.Flush()
		case <-r.sigwinchCh:
			// Terminal resized during dbtop — update dimensions and redraw.
			newCols, newRows, err := term.GetSize(int(os.Stdin.Fd()))
			if err == nil && (newCols != r.cols || newRows != r.rows) {
				r.cols = newCols
				r.rows = newRows
				termClearScreen(r.writer)
				lines = loop.RenderFrame(context.Background(), r.cols, intervalSec)
				startRow = 1
				for i, line := range lines {
					termWriteAt(r.writer, startRow+i, line)
				}
				if alertLine != "" {
					termWriteAt(r.writer, startRow+len(lines), alertLine)
				}
				r.writer.Flush()
			}
		}
	}

	termShowCursor(r.writer)
	// Clear alert line and any stale content below it.
	alertShown := alertLine != ""
	if alertShown {
		alertRow := startRow + len(lines)
		termClearRow(r.writer, alertRow)
		// Clear a few extra rows below the alert that may have stale content
		// from previous renders (e.g., old prompt fragments).
		for extra := alertRow + 1; extra <= alertRow+2 && extra <= r.rows; extra++ {
			termClearRow(r.writer, extra)
		}
	}
	// Last frame stays in place as history.
	if !r.scrollMode {
		r.contentRow = startRow + len(lines)
		// Skip past the alert row so the prompt doesn't overlap it.
		if alertShown {
			r.contentRow++
		}
	}
}

// formatDbtopAlert formats a sentinel alert as a single compact line for dbtop overlay.
func formatDbtopAlert(evt alert.Event, cols int) string {
	ts := evt.Timestamp.Format("15:04:05")
	line := fmt.Sprintf("\u26a0 [%s] %s | /llm \u67e5\u770b\u8bca\u65ad", ts, evt.Description)

	// Truncate if wider than terminal.
	if displayWidth(line) > cols {
		line = truncateToWidth(line, cols-1)
	}
	return accentStyle.Render(line)
}
