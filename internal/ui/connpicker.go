/*-------------------------------------------------------------------------
 *
 * connpicker.go
 *	  Interactive connection picker — TUI list used by /conn and the
 *	  boot path when no -c flag is given. Shared with /model picker
 *	  via the Picker primitive (see picker.go).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/connpicker.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/sqlrush/opendb/internal/config"
)

// connPicker is an interactive connection selector using alternate screen buffer.
type connPicker struct {
	repl     *REPL
	conns    []config.Connection
	filtered []config.Connection
	selected int
	filter   string
	result   string // selected connection name, empty on cancel
}

// pickConnection opens an interactive connection list for the user to select.
// Returns the selected connection name, or empty string on cancel/ESC.
func (r *REPL) pickConnection() string {
	conns := r.connMgr.ListConnections()
	if len(conns) == 0 {
		return ""
	}

	cp := &connPicker{
		repl:     r,
		conns:    conns,
		filtered: conns,
		selected: 0,
	}

	// Enter alternate screen buffer.
	fmt.Fprint(r.writer, "\033[?1049h") // enter alternate screen
	fmt.Fprint(r.writer, "\033[?25l")   // hide cursor

	cp.draw()

	// Event loop.
	buf := make([]byte, 16)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			break
		}

		for i := 0; i < n; {
			b := buf[i]

			switch {
			case b == 27: // ESC or arrow sequence
				if i+2 < n && buf[i+1] == '[' {
					switch buf[i+2] {
					case 'A': // Up
						cp.moveUp()
						i += 3
						continue
					case 'B': // Down
						cp.moveDown()
						i += 3
						continue
					}
					i += 3
					continue
				}
				// Bare ESC — cancel.
				goto done

			case b == 13 || b == 10: // Enter — select
				if len(cp.filtered) > 0 && cp.selected < len(cp.filtered) {
					cp.result = cp.filtered[cp.selected].Name
				}
				goto done

			case b == 3: // Ctrl+C — cancel
				goto done

			case b == 127 || b == 8: // Backspace — remove filter char
				if len(cp.filter) > 0 {
					cp.filter = cp.filter[:len(cp.filter)-1]
					cp.applyFilter()
					cp.draw()
				}

			case b >= 32 && b < 127: // Printable — type-ahead filter
				cp.filter += string(b)
				cp.applyFilter()
				cp.draw()
			}

			i++
		}
	}

done:
	// Restore main screen.
	fmt.Fprint(r.writer, "\033[?25h")   // show cursor
	fmt.Fprint(r.writer, "\033[?1049l") // leave alternate screen
	if r.scrollMode {
		fmt.Fprintf(r.writer, "\033[1;%dr", r.maxContentRow())
	}
	// Don't drawInputArea here — main loop's blockingUI handler will.

	return cp.result
}

// applyFilter filters connections by name prefix (case-insensitive).
func (cp *connPicker) applyFilter() {
	if cp.filter == "" {
		cp.filtered = cp.conns
		cp.selected = 0
		return
	}

	lower := strings.ToLower(cp.filter)
	filtered := make([]config.Connection, 0)
	for _, c := range cp.conns {
		if strings.Contains(strings.ToLower(c.Name), lower) {
			filtered = append(filtered, c)
		}
	}
	cp.filtered = filtered
	cp.selected = 0
}

// moveUp moves selection up by one.
func (cp *connPicker) moveUp() {
	if cp.selected > 0 {
		cp.selected--
		cp.draw()
	}
}

// moveDown moves selection down by one.
func (cp *connPicker) moveDown() {
	if cp.selected < len(cp.filtered)-1 {
		cp.selected++
		cp.draw()
	}
}

// draw renders the connection list.
func (cp *connPicker) draw() {
	r := cp.repl
	w := r.writer

	// Clear screen.
	fmt.Fprint(w, "\033[2J\033[H")

	// Title.
	title := "  选择连接"
	if cp.filter != "" {
		title += fmt.Sprintf("  (过滤: %s)", cp.filter)
	}
	fmt.Fprintf(w, "\033[1;1H\033[1m%s\033[0m\n", title)
	fmt.Fprintf(w, "\033[2;1H%s\n", strings.Repeat("─", r.cols))

	// Visible area.
	maxVisible := r.rows - 5 // title(1) + separator(1) + footer(2) + status(1)
	if maxVisible < 1 {
		maxVisible = 1
	}

	// Calculate scroll offset for long lists.
	offset := 0
	if cp.selected >= maxVisible {
		offset = cp.selected - maxVisible + 1
	}

	// Render connections.
	recentSet := make(map[string]bool)
	for _, name := range r.connMgr.RecentConnections() {
		recentSet[name] = true
	}
	currentName := r.connMgr.CurrentName()

	for i := offset; i < len(cp.filtered) && i-offset < maxVisible; i++ {
		c := cp.filtered[i]
		row := 3 + (i - offset)

		marker := "  "
		if c.Name == currentName {
			marker = "* "
		} else if recentSet[c.Name] {
			marker = "~ "
		}

		service := c.Service
		if service == "" {
			service = c.SID
		}
		line := fmt.Sprintf("  %s%-20s  %s@%s:%d/%s",
			marker, c.Name, c.User, c.Host, c.Port, service)

		if i == cp.selected {
			// Highlight selected row.
			fmt.Fprintf(w, "\033[%d;1H\033[7m▸ %s\033[0m", row, line)
		} else {
			fmt.Fprintf(w, "\033[%d;1H  %s", row, line)
		}
	}

	if len(cp.filtered) == 0 {
		fmt.Fprintf(w, "\033[3;1H  (无匹配连接)")
	}

	// Footer.
	footerRow := r.rows - 1
	fmt.Fprintf(w, "\033[%d;1H%s", footerRow, strings.Repeat("─", r.cols))

	statusRow := r.rows
	status := "  ↑↓ 选择  Enter 确认  Esc 取消"
	if len(cp.filtered) > 0 {
		status += fmt.Sprintf("  │  %d/%d", cp.selected+1, len(cp.filtered))
	}
	fmt.Fprintf(w, "\033[%d;1H\033[7m%-*s\033[0m", statusRow, r.cols, status)
}
