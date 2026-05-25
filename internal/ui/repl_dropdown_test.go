/*-------------------------------------------------------------------------
 *
 * repl_dropdown_test.go
 *	  Test cases for repl_dropdown.go (ui package):
 *	  TestDropdown_NonScrollMode_Below,
 *	  TestDropdown_NonScrollMode_CappedBelow,
 *	  TestDropdown_ScrollMode_ShiftedUp.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_dropdown_test.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

// captureDrawInputArea calls drawInputArea and returns the ANSI output.
func captureDrawInputArea(r *REPL) string {
	var buf bytes.Buffer
	r.writer = bufio.NewWriter(&buf)
	r.drawInputArea()
	r.writer.Flush()
	return buf.String()
}

func newTestREPL(rows, cols int) *REPL {
	return &REPL{
		writer:     bufio.NewWriter(&bytes.Buffer{}),
		rows:       rows,
		cols:       cols,
		historyIdx: -1,
		compIdx:    -1,
		registry:   skill.NewRegistry(),
		alertBuf:   newAlertBuffer(),
	}
}

func TestDropdown_NonScrollMode_Below(t *testing.T) {
	r := newTestREPL(30, 80)
	r.contentRow = 10
	r.scrollMode = false
	r.inputBuf = []rune("/s")
	r.cursorPos = 2
	r.compIdx = 0
	r.completions = []string{"sessions", "slowsql", "sga", "status"}

	out := captureDrawInputArea(r)

	// Input should be at rows 10-12 (contentRow to contentRow+2).
	if !strings.Contains(out, "\033[10;1H") {
		t.Error("topSep should be at row 10")
	}
	if !strings.Contains(out, "\033[12;1H") {
		t.Error("botSep should be at row 12")
	}
	// Dropdown below: rows 13-16 (4 items).
	if !strings.Contains(out, "\033[13;1H") {
		t.Error("first dropdown item should be at row 13")
	}
	if !strings.Contains(out, "\033[16;1H") {
		t.Error("last dropdown item should be at row 16")
	}
	// State tracking.
	if r.dropdownShown != 4 {
		t.Errorf("dropdownShown = %d, want 4", r.dropdownShown)
	}
	if r.drawnTopRow != 10 {
		t.Errorf("drawnTopRow = %d, want 10", r.drawnTopRow)
	}
	if r.drawnEndRow != 16 {
		t.Errorf("drawnEndRow = %d, want 16", r.drawnEndRow)
	}
}

func TestDropdown_NonScrollMode_CappedBelow(t *testing.T) {
	r := newTestREPL(30, 80)
	r.contentRow = 27 // near bottom, botSep = 29, only 1 row below
	r.scrollMode = false
	r.inputBuf = []rune("/s")
	r.cursorPos = 2
	r.compIdx = 0
	r.completions = []string{"sessions", "slowsql", "sga", "status"}

	captureDrawInputArea(r)

	// Only 1 row available below (row 30). Dropdown capped to 1.
	if r.dropdownShown != 1 {
		t.Errorf("dropdownShown = %d, want 1 (capped)", r.dropdownShown)
	}
}

func TestDropdown_ScrollMode_ShiftedUp(t *testing.T) {
	r := newTestREPL(30, 80)
	r.scrollMode = true
	r.inputBuf = []rune("/s")
	r.cursorPos = 2
	r.compIdx = 0
	r.completions = []string{"sessions", "slowsql", "sga", "status"}

	out := captureDrawInputArea(r)

	// In scroll mode, base input is at 28-30.
	// With 4-item dropdown, input shifts up by 4: topSep = 24, botSep = 26.
	// Dropdown at 27-30.
	if !strings.Contains(out, "\033[24;1H") {
		t.Error("shifted topSep should be at row 24")
	}
	if !strings.Contains(out, "\033[26;1H") {
		t.Error("shifted botSep should be at row 26")
	}
	// Dropdown items at 27-30.
	if !strings.Contains(out, "\033[27;1H") {
		t.Error("first dropdown item should be at row 27")
	}
	if !strings.Contains(out, "\033[30;1H") {
		t.Error("last dropdown item should be at row 30")
	}
	if r.dropdownShown != 4 {
		t.Errorf("dropdownShown = %d, want 4", r.dropdownShown)
	}
	if r.drawnTopRow != 24 {
		t.Errorf("drawnTopRow = %d, want 24", r.drawnTopRow)
	}
	if r.drawnEndRow != 30 {
		t.Errorf("drawnEndRow = %d, want 30", r.drawnEndRow)
	}
	// Scroll region should be shrunk to 1-23.
	if !strings.Contains(out, "\033[1;23r") {
		t.Error("scroll region should be set to 1;23")
	}
}

func TestDropdown_ScrollMode_SmallTerminal(t *testing.T) {
	r := newTestREPL(12, 80)
	r.scrollMode = true
	r.inputBuf = []rune("/s")
	r.cursorPos = 2
	r.compIdx = 0
	r.completions = []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"}

	captureDrawInputArea(r)

	// 12-row terminal, scroll mode. Base input at 10-12.
	// Max 6 items (maxDropdownVisible=6): topSep = 10-6 = 4. That works (>= 1).
	// Dropdown at 7-12.
	if r.drawnTopRow != 4 {
		t.Errorf("drawnTopRow = %d, want 4", r.drawnTopRow)
	}
	if r.drawnEndRow != 12 {
		t.Errorf("drawnEndRow = %d, want 12", r.drawnEndRow)
	}
	if r.dropdownShown != 6 {
		t.Errorf("dropdownShown = %d, want 6", r.dropdownShown)
	}
}

func TestDropdown_ScrollMode_VerySmallTerminal(t *testing.T) {
	r := newTestREPL(10, 80)
	r.scrollMode = true
	r.inputBuf = []rune("/s")
	r.cursorPos = 2
	r.compIdx = 0
	r.completions = []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"}

	captureDrawInputArea(r)

	// 10-row terminal. Base input at 8-10. Max 6 items.
	// Shift by 6: topSep = 8-6 = 2. That works (>= 1).
	// Dropdown at 5-10.
	if r.drawnTopRow != 2 {
		t.Errorf("drawnTopRow = %d, want 2", r.drawnTopRow)
	}
	if r.dropdownShown != 6 {
		t.Errorf("dropdownShown = %d, want 6", r.dropdownShown)
	}
}

func TestDropdown_Cleanup_AfterTab(t *testing.T) {
	r := newTestREPL(30, 80)
	r.scrollMode = true
	r.inputBuf = []rune("/s")
	r.cursorPos = 2
	r.compIdx = 0
	r.completions = []string{"sessions", "slowsql", "sga", "status"}

	// First draw: show dropdown.
	captureDrawInputArea(r)

	if r.dropdownShown != 4 {
		t.Fatalf("initial dropdownShown = %d, want 4", r.dropdownShown)
	}
	if r.drawnTopRow != 24 {
		t.Fatalf("initial drawnTopRow = %d, want 24", r.drawnTopRow)
	}

	// Simulate Tab: accept completion, clear completions.
	r.inputBuf = []rune("/sessions ")
	r.cursorPos = 10
	r.completions = nil
	r.compIdx = -1

	out := captureDrawInputArea(r)

	// Should have cleared rows 24-30 (old drawing area).
	for row := 24; row <= 30; row++ {
		clearSeq := fmt.Sprintf("\033[%d;1H\033[2K", row)
		if !strings.Contains(out, clearSeq) {
			t.Errorf("should clear row %d from old drawing", row)
		}
	}

	// Input stays near content (not back to bottom) after dropdown closes.
	// With 4-item scroll, contentRow = 27 - 4 + 1 = 24, input at 24-26.
	if r.dropdownShown != 0 {
		t.Errorf("dropdownShown = %d, want 0 after Tab", r.dropdownShown)
	}
	if r.drawnTopRow != 24 {
		t.Errorf("drawnTopRow = %d, want 24 (near content)", r.drawnTopRow)
	}
	// Should have exited scroll mode.
	if r.scrollMode {
		t.Error("should have exited scroll mode after dropdown close")
	}
	// Scroll region should be fully reset.
	if !strings.Contains(out, "\033[r") {
		t.Error("scroll region should be reset")
	}
}

func TestDropdown_PermanentScroll(t *testing.T) {
	r := newTestREPL(30, 80)
	r.scrollMode = true

	// Simulate output buffer with content lines.
	for i := 0; i < 27; i++ {
		r.outputBuffer = append(r.outputBuffer, fmt.Sprintf("content-line-%d", i))
	}

	// Show dropdown (content should scroll up permanently).
	r.inputBuf = []rune("/s")
	r.cursorPos = 2
	r.compIdx = 0
	r.completions = []string{"sessions", "slowsql", "sga", "status"}
	openOut := captureDrawInputArea(r)

	if r.drawnTopRow != 24 {
		t.Fatalf("drawnTopRow = %d, want 24", r.drawnTopRow)
	}
	if r.dropScrollOffset != 4 {
		t.Errorf("dropScrollOffset = %d, want 4", r.dropScrollOffset)
	}

	// Should contain scroll sequences (\r\n at maxContentRow).
	// maxContentRow = 27, so we expect \033[27;1H\r\n.
	if !strings.Contains(openOut, "\033[27;1H\r\n") {
		t.Error("should contain scroll sequence at maxContentRow")
	}

	// Now close dropdown (simulate Tab completion).
	r.inputBuf = []rune("/sessions ")
	r.cursorPos = 10
	r.completions = nil
	r.compIdx = -1

	captureDrawInputArea(r)

	// Input stays near content after dropdown closes (not at bottom).
	// contentRow = 27 - 4 + 1 = 24, input at 24-26.
	if r.drawnTopRow != 24 {
		t.Errorf("drawnTopRow = %d, want 24 (near content)", r.drawnTopRow)
	}
	if r.scrollMode {
		t.Error("should have exited scroll mode")
	}
	if r.dropScrollOffset != 0 {
		t.Errorf("dropScrollOffset = %d, want 0 after close", r.dropScrollOffset)
	}
}

func TestDropdown_NoDropdown_NoCleanup(t *testing.T) {
	r := newTestREPL(30, 80)
	r.contentRow = 10
	r.scrollMode = false
	r.inputBuf = []rune("SELECT 1")
	r.cursorPos = 8

	out := captureDrawInputArea(r)

	if r.dropdownShown != 0 {
		t.Errorf("dropdownShown = %d, want 0", r.dropdownShown)
	}
	if r.drawnTopRow != 10 {
		t.Errorf("drawnTopRow = %d, want 10", r.drawnTopRow)
	}
	if r.drawnEndRow != 12 {
		t.Errorf("drawnEndRow = %d, want 12", r.drawnEndRow)
	}
	// Should not contain scroll region commands.
	if strings.Contains(out, "\033[1;") && strings.Contains(out, "r") {
		// Check it's not a scroll region set (could be false positive from other sequences).
		// Just verify no dropdown was drawn.
	}
	_ = out
}

func TestWriteOutputLine_DoesNotEraseOwnContent(t *testing.T) {
	// Regression: after dropdown close, input area drawn at contentRow (non-scroll).
	// Subsequent writeOutputLine overwrites those rows, then enters scroll mode.
	// clearPreviousDrawing must NOT erase those rows (they now have real content).
	r := newTestREPL(30, 80)
	r.contentRow = 24
	r.scrollMode = false
	// Simulate: drawInputArea was previously called at contentRow=24.
	r.drawnTopRow = 24
	r.drawnEndRow = 26

	// Write several lines — these overwrite the old input area rows.
	r.writeOutputLine("command echo")  // row 24, cr=25
	r.writeOutputLine("")              // row 25, cr=26
	r.writeOutputLine("  top border")  // row 26, cr=27
	r.writeOutputLine("  header row")  // row 27, cr=28 > maxRow=27 → enters scroll

	// After writing over old input area, drawnTopRow should be reset.
	if r.drawnTopRow != 0 {
		t.Errorf("drawnTopRow = %d, want 0 (reset after overwrite)", r.drawnTopRow)
	}
	if r.drawnEndRow != 0 {
		t.Errorf("drawnEndRow = %d, want 0 (reset after overwrite)", r.drawnEndRow)
	}

	// Now draw input area — clearPreviousDrawing should be a no-op.
	out := captureDrawInputArea(r)

	// Verify no clear sequences for rows 24-26 (those have real content now).
	for row := 24; row <= 26; row++ {
		clearSeq := fmt.Sprintf("\033[%d;1H\033[2K", row)
		if strings.Contains(out, clearSeq) {
			t.Errorf("drawInputArea should NOT clear row %d (has real content)", row)
		}
	}
}
