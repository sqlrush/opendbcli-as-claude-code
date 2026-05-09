/*-------------------------------------------------------------------------
 *
 * term.go
 *	  Terminal abstraction — wraps ANSI escape sequences (cursor moves,
 *	  scroll regions, clear lines) and TTY size detection so the rest
 *	  of the UI doesn't sprinkle raw control codes everywhere.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/term.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"io"
)

// term provides thin wrappers around common ANSI terminal operations.
// These replace raw escape sequence writes, eliminating ordering/syntax
// errors and making terminal operations self-documenting.
//
// All functions write to the given io.Writer without flushing — the
// caller is responsible for flushing buffered writers when needed.

// termMoveTo positions the cursor at (row, col) — both 1-based.
func termMoveTo(w io.Writer, row, col int) {
	fmt.Fprintf(w, "\033[%d;%dH", row, col)
}

// termMoveToRow positions the cursor at column 1 of the given row.
func termMoveToRow(w io.Writer, row int) {
	fmt.Fprintf(w, "\033[%d;1H", row)
}

// termClearLine erases the entire current line.
func termClearLine(w io.Writer) {
	fmt.Fprint(w, "\033[2K")
}

// termClearRow moves to the given row and clears it.
func termClearRow(w io.Writer, row int) {
	fmt.Fprintf(w, "\033[%d;1H\033[2K", row)
}

// termWriteAt moves to the given row (col 1) and writes content,
// clearing the line first.
func termWriteAt(w io.Writer, row int, content string) {
	fmt.Fprintf(w, "\033[%d;1H\033[2K%s", row, content)
}

// termHideCursor hides the terminal cursor.
func termHideCursor(w io.Writer) {
	fmt.Fprint(w, "\033[?25l")
}

// termShowCursor shows the terminal cursor.
func termShowCursor(w io.Writer) {
	fmt.Fprint(w, "\033[?25h")
}

// termEnterAltScreen switches to the alternate screen buffer.
func termEnterAltScreen(w io.Writer) {
	fmt.Fprint(w, "\033[?1049h")
}

// termLeaveAltScreen switches back to the main screen buffer.
func termLeaveAltScreen(w io.Writer) {
	fmt.Fprint(w, "\033[?1049l")
}

// termSetScrollRegion sets the scrollable region to rows [top, bottom].
func termSetScrollRegion(w io.Writer, top, bottom int) {
	fmt.Fprintf(w, "\033[%d;%dr", top, bottom)
}

// termResetScrollRegion resets the scroll region to the full screen.
// Note: this also moves the cursor to (1,1) as a side effect.
func termResetScrollRegion(w io.Writer) {
	fmt.Fprint(w, "\033[r")
}

// termClearScreen clears the entire screen and moves cursor to (1,1).
func termClearScreen(w io.Writer) {
	fmt.Fprint(w, "\033[2J\033[H")
}

// termReverseVideo enables reverse video mode for status bars.
func termReverseVideo(w io.Writer) {
	fmt.Fprint(w, "\033[7m")
}

// termResetStyle resets all text attributes.
func termResetStyle(w io.Writer) {
	fmt.Fprint(w, "\033[0m")
}
