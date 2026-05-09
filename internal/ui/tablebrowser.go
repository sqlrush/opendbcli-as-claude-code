/*-------------------------------------------------------------------------
 *
 * tablebrowser.go
 *	  Result-set browser — pages through query results larger than the
 *	  screen, with arrow-key navigation and column-width auto-fit.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/tablebrowser.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/format"
)

// tableBrowser provides a scrollable table view with fixed header.
type tableBrowser struct {
	repl   *REPL
	lines  *format.TableLines
	offset int // first visible data row index
	visH   int // how many data rows fit on screen
}

// browseTable enters the interactive table browser.
// It takes over the screen until the user presses ESC or q.
func (r *REPL) browseTable(tl *format.TableLines) {
	if tl == nil || len(tl.Rows) == 0 {
		return
	}

	tb := &tableBrowser{
		repl:  r,
		lines: tl,
	}

	// Header (3 lines) + footer (1) + status bar (1) = 5 fixed rows.
	tb.visH = r.rows - 5
	if tb.visH < 1 {
		tb.visH = 1
	}

	// Switch to alternate screen buffer (preserves main screen content).
	termEnterAltScreen(r.writer)
	termHideCursor(r.writer)

	tb.draw()

	// Event loop — read from REPL's key channel to avoid racing the
	// keyboard goroutine that also reads os.Stdin.
	for {
		ke := <-r.keyCh
		if ke.err != nil {
			break
		}
		buf := ke.buf
		n := ke.n

		exit := false
		for i := 0; i < n; {
			b := buf[i]

			switch {
			case b == 27: // ESC sequence or bare ESC
				if i+2 < n && buf[i+1] == '[' {
					switch buf[i+2] {
					case 'A': // Up
						tb.scrollUp(1)
						i += 3
						continue
					case 'B': // Down
						tb.scrollDown(1)
						i += 3
						continue
					case '5': // PgUp: ESC [ 5 ~
						if i+3 < n && buf[i+3] == '~' {
							tb.scrollUp(tb.visH)
							i += 4
							continue
						}
					case '6': // PgDn: ESC [ 6 ~
						if i+3 < n && buf[i+3] == '~' {
							tb.scrollDown(tb.visH)
							i += 4
							continue
						}
					}
					// Unknown CSI — skip the sequence.
					i += 3
					continue
				}
				// Bare ESC — exit browser.
				exit = true

			case b == 'q', b == 'Q':
				exit = true

			case b == 'k', b == 'K':
				tb.scrollUp(1)

			case b == 'j', b == 'J':
				tb.scrollDown(1)

			case b == 'g': // Home — go to top
				tb.offset = 0
				tb.draw()

			case b == 'G': // End — go to bottom
				max := len(tl.Rows) - tb.visH
				if max < 0 {
					max = 0
				}
				tb.offset = max
				tb.draw()

			case b == 3: // Ctrl+C — exit
				exit = true
			}

			if exit {
				break
			}
			i++
		}
		if exit {
			break
		}
	}
	// Restore main screen buffer (content fully preserved).
	termShowCursor(r.writer)
	termLeaveAltScreen(r.writer)
	// Restore scroll region if we were in scroll mode.
	if r.scrollMode {
		termSetScrollRegion(r.writer, 1, r.maxContentRow())
	}
	// Force-repaint the visible content area from outputBuffer to eliminate
	// any stale separator lines left by the alternate screen restore.
	if r.scrollMode {
		maxRow := r.maxContentRow()
		bufLen := len(r.outputBuffer)
		for row := 1; row <= maxRow; row++ {
			bufIdx := bufLen - maxRow + row - 1
			termClearRow(r.writer, row)
			if bufIdx >= 0 && bufIdx < bufLen {
				fmt.Fprint(r.writer, r.outputBuffer[bufIdx])
			}
		}
	}
	r.drawInputArea()
}

// scrollUp moves the viewport up by n rows.
func (tb *tableBrowser) scrollUp(n int) {
	if tb.offset <= 0 {
		return
	}
	tb.offset -= n
	if tb.offset < 0 {
		tb.offset = 0
	}
	tb.draw()
}

// scrollDown moves the viewport down by n rows.
func (tb *tableBrowser) scrollDown(n int) {
	max := len(tb.lines.Rows) - tb.visH
	if max < 0 {
		max = 0
	}
	if tb.offset >= max {
		return
	}
	tb.offset += n
	if tb.offset > max {
		tb.offset = max
	}
	tb.draw()
}

// draw renders the current viewport.
func (tb *tableBrowser) draw() {
	r := tb.repl
	w := r.writer

	// Row 1-3: fixed header.
	for i, line := range tb.lines.Header {
		termWriteAt(w, i+1, line)
	}

	// Row 4 to 4+visH-1: visible data rows.
	startRow := 4
	end := tb.offset + tb.visH
	if end > len(tb.lines.Rows) {
		end = len(tb.lines.Rows)
	}
	for i := 0; i < tb.visH; i++ {
		row := startRow + i
		dataIdx := tb.offset + i
		termClearRow(w, row)
		if dataIdx < end {
			fmt.Fprint(w, tb.lines.Rows[dataIdx])
		}
	}

	// Footer line (bottom border).
	footerRow := startRow + tb.visH
	termWriteAt(w, footerRow, tb.lines.Footer)

	// Status bar.
	statusRow := footerRow + 1
	showing := end - tb.offset
	total := len(tb.lines.Rows)
	pct := 0
	if total > 0 {
		pct = (tb.offset + showing) * 100 / total
	}
	status := fmt.Sprintf("  行 %d-%d / %d (%d%%)", tb.offset+1, tb.offset+showing, total, pct)
	hint := "  ↑↓ 滚动 │ PgUp/PgDn 翻页 │ g/G 首尾 │ ESC/q 退出"

	// Pad status to fill width, then add hint right-aligned.
	statusW := len(status)
	hintW := len(hint)
	gap := r.cols - statusW - hintW
	if gap < 2 {
		gap = 2
	}

	termMoveToRow(w, statusRow)
	termClearLine(w)
	termReverseVideo(w)
	fmt.Fprintf(w, "%s%s%s", status, strings.Repeat(" ", gap), hint)
	termResetStyle(w)
	r.writer.Flush()
}
