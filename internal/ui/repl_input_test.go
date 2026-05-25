/*-------------------------------------------------------------------------
 *
 * repl_input_test.go
 *	  Test cases for repl_input.go (ui package):
 *	  TestHandleKeyInput_InsertASCII, TestHandleKeyInput_InsertUTF8,
 *	  TestHandleKeyInput_InsertAtMiddle.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_input_test.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

// ── Helpers ──────────────────────────────────────────────────

// newInputTestREPL builds a minimal REPL suitable for input-logic tests.
// It writes terminal output to a throwaway buffer so drawInputArea does
// not panic.
func newInputTestREPL() *REPL {
	return &REPL{
		writer:     bufio.NewWriter(&bytes.Buffer{}),
		rows:       30,
		cols:       80,
		historyIdx: -1,
		compIdx:    -1,
		registry:   skill.NewRegistry(),
		alertBuf:   newAlertBuffer(),
	}
}

// setInput is a convenience to populate inputBuf + cursorPos at the end.
func setInput(r *REPL, s string) {
	r.inputBuf = []rune(s)
	r.cursorPos = len(r.inputBuf)
}

// inputString returns the current inputBuf as a string.
func inputString(r *REPL) string {
	return string(r.inputBuf)
}

// ── Character Insertion ──────────────────────────────────────

func TestHandleKeyInput_InsertASCII(t *testing.T) {
	r := newInputTestREPL()

	r.handleKeyInput([]byte{'h'}, 1)
	r.handleKeyInput([]byte{'i'}, 1)

	if got := inputString(r); got != "hi" {
		t.Errorf("inputBuf = %q, want %q", got, "hi")
	}
	if r.cursorPos != 2 {
		t.Errorf("cursorPos = %d, want 2", r.cursorPos)
	}
}

func TestHandleKeyInput_InsertUTF8(t *testing.T) {
	r := newInputTestREPL()

	// "你好" is 6 bytes in UTF-8.
	data := []byte("你好")
	r.handleKeyInput(data, len(data))

	if got := inputString(r); got != "你好" {
		t.Errorf("inputBuf = %q, want %q", got, "你好")
	}
	if r.cursorPos != 2 {
		t.Errorf("cursorPos = %d, want 2 (2 runes)", r.cursorPos)
	}
}

func TestHandleKeyInput_InsertAtMiddle(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "ac")
	r.cursorPos = 1 // cursor after 'a'

	r.handleKeyInput([]byte{'b'}, 1)

	if got := inputString(r); got != "abc" {
		t.Errorf("inputBuf = %q, want %q", got, "abc")
	}
	if r.cursorPos != 2 {
		t.Errorf("cursorPos = %d, want 2", r.cursorPos)
	}
}

// ── Backspace ────────────────────────────────────────────────

func TestHandleKeyInput_Backspace(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")

	r.handleKeyInput([]byte{127}, 1) // backspace

	if got := inputString(r); got != "ab" {
		t.Errorf("inputBuf = %q, want %q", got, "ab")
	}
	if r.cursorPos != 2 {
		t.Errorf("cursorPos = %d, want 2", r.cursorPos)
	}
}

func TestHandleKeyInput_BackspaceAtStart(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")
	r.cursorPos = 0

	r.handleKeyInput([]byte{127}, 1) // backspace at position 0

	if got := inputString(r); got != "abc" {
		t.Errorf("inputBuf should be unchanged, got %q", got)
	}
	if r.cursorPos != 0 {
		t.Errorf("cursorPos = %d, want 0", r.cursorPos)
	}
}

func TestHandleKeyInput_BackspaceMiddle(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abcd")
	r.cursorPos = 2 // cursor after 'b'

	r.handleKeyInput([]byte{127}, 1) // delete 'b'

	if got := inputString(r); got != "acd" {
		t.Errorf("inputBuf = %q, want %q", got, "acd")
	}
	if r.cursorPos != 1 {
		t.Errorf("cursorPos = %d, want 1", r.cursorPos)
	}
}

// Backspace on a completed slash command (e.g. "/health") should
// delete back to "/" in one shot.
func TestHandleKeyInput_BackspaceSlashCommand(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/health")

	r.handleKeyInput([]byte{127}, 1)

	if got := inputString(r); got != "/" {
		t.Errorf("inputBuf = %q, want %q (single command word deletion)", got, "/")
	}
	if r.cursorPos != 1 {
		t.Errorf("cursorPos = %d, want 1", r.cursorPos)
	}
}

func TestHandleKeyInput_BackspaceSlashCommandWithArg(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/login mydb")

	r.handleKeyInput([]byte{127}, 1)

	// Two words: "/login mydb" — backspace should delete 'b', not the whole thing.
	if got := inputString(r); got != "/login myd" {
		t.Errorf("inputBuf = %q, want %q", got, "/login myd")
	}
}

// ── Delete (forward) ─────────────────────────────────────────

func TestHandleKeyInput_Delete(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abcd")
	r.cursorPos = 1 // cursor after 'a'

	// ESC [ 3 ~
	r.handleKeyInput([]byte{27, '[', '3', '~'}, 4)

	if got := inputString(r); got != "acd" {
		t.Errorf("inputBuf = %q, want %q", got, "acd")
	}
	if r.cursorPos != 1 {
		t.Errorf("cursorPos = %d, want 1 (unchanged)", r.cursorPos)
	}
}

func TestHandleKeyInput_DeleteAtEnd(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")
	// cursor at end — delete should be a no-op.

	r.handleKeyInput([]byte{27, '[', '3', '~'}, 4)

	if got := inputString(r); got != "abc" {
		t.Errorf("inputBuf should be unchanged, got %q", got)
	}
}

// ── Cursor Movement ──────────────────────────────────────────

func TestHandleKeyInput_ArrowLeft(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")

	r.handleKeyInput([]byte{27, '[', 'D'}, 3) // left

	if r.cursorPos != 2 {
		t.Errorf("cursorPos = %d, want 2", r.cursorPos)
	}
}

func TestHandleKeyInput_ArrowLeftAtStart(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")
	r.cursorPos = 0

	r.handleKeyInput([]byte{27, '[', 'D'}, 3) // left at 0

	if r.cursorPos != 0 {
		t.Errorf("cursorPos = %d, want 0 (clamped)", r.cursorPos)
	}
}

func TestHandleKeyInput_ArrowRight(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")
	r.cursorPos = 1

	r.handleKeyInput([]byte{27, '[', 'C'}, 3) // right

	if r.cursorPos != 2 {
		t.Errorf("cursorPos = %d, want 2", r.cursorPos)
	}
}

func TestHandleKeyInput_ArrowRightAtEnd(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")

	r.handleKeyInput([]byte{27, '[', 'C'}, 3) // right at end

	if r.cursorPos != 3 {
		t.Errorf("cursorPos = %d, want 3 (clamped)", r.cursorPos)
	}
}

func TestHandleKeyInput_Home(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")

	r.handleKeyInput([]byte{27, '[', 'H'}, 3) // Home

	if r.cursorPos != 0 {
		t.Errorf("cursorPos = %d, want 0", r.cursorPos)
	}
}

func TestHandleKeyInput_End(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "abc")
	r.cursorPos = 0

	r.handleKeyInput([]byte{27, '[', 'F'}, 3) // End

	if r.cursorPos != 3 {
		t.Errorf("cursorPos = %d, want 3", r.cursorPos)
	}
}

// ── Ctrl+A / Ctrl+E ──────────────────────────────────────────

func TestHandleKeyInput_CtrlA(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "hello world")

	r.handleKeyInput([]byte{1}, 1) // Ctrl+A

	if r.cursorPos != 0 {
		t.Errorf("Ctrl+A: cursorPos = %d, want 0", r.cursorPos)
	}
}

func TestHandleKeyInput_CtrlE(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "hello world")
	r.cursorPos = 3

	r.handleKeyInput([]byte{5}, 1) // Ctrl+E

	if r.cursorPos != 11 {
		t.Errorf("Ctrl+E: cursorPos = %d, want 11", r.cursorPos)
	}
}

// ── Ctrl+U (kill line) ───────────────────────────────────────

func TestHandleKeyInput_CtrlU(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "hello world")

	r.handleKeyInput([]byte{21}, 1) // Ctrl+U

	if got := inputString(r); got != "" {
		t.Errorf("Ctrl+U: inputBuf = %q, want empty", got)
	}
	if r.cursorPos != 0 {
		t.Errorf("Ctrl+U: cursorPos = %d, want 0", r.cursorPos)
	}
}

// ── Ctrl+W (delete word backward) ────────────────────────────

func TestHandleKeyInput_CtrlW(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "hello world")

	r.handleKeyInput([]byte{23}, 1) // Ctrl+W

	if got := inputString(r); got != "hello " {
		t.Errorf("Ctrl+W: inputBuf = %q, want %q", got, "hello ")
	}
	if r.cursorPos != 6 {
		t.Errorf("Ctrl+W: cursorPos = %d, want 6", r.cursorPos)
	}
}

func TestHandleKeyInput_CtrlW_TrailingSpaces(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "hello   ")

	r.handleKeyInput([]byte{23}, 1) // Ctrl+W

	// Should skip trailing spaces, then delete "hello".
	if got := inputString(r); got != "" {
		t.Errorf("Ctrl+W with trailing spaces: inputBuf = %q, want empty", got)
	}
}

func TestHandleKeyInput_CtrlW_SingleWord(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "hello")

	r.handleKeyInput([]byte{23}, 1) // Ctrl+W

	if got := inputString(r); got != "" {
		t.Errorf("Ctrl+W single word: inputBuf = %q, want empty", got)
	}
}

func TestHandleKeyInput_CtrlW_Empty(t *testing.T) {
	r := newInputTestREPL()

	r.handleKeyInput([]byte{23}, 1) // Ctrl+W on empty

	if got := inputString(r); got != "" {
		t.Errorf("Ctrl+W empty: inputBuf = %q, want empty", got)
	}
}

// ── Ctrl+C / Ctrl+D (exit) ───────────────────────────────────

func TestHandleKeyInput_CtrlC_Exit(t *testing.T) {
	r := newInputTestREPL()

	r.handleKeyInput([]byte{3}, 1) // Ctrl+C

	if !r.exitRequested {
		t.Error("Ctrl+C should set exitRequested = true")
	}
}

func TestHandleKeyInput_CtrlD_EmptyInput(t *testing.T) {
	r := newInputTestREPL()

	r.handleKeyInput([]byte{4}, 1) // Ctrl+D on empty input

	if !r.exitRequested {
		t.Error("Ctrl+D on empty input should set exitRequested = true")
	}
}

func TestHandleKeyInput_CtrlD_NonEmptyInput(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "hello")

	r.handleKeyInput([]byte{4}, 1) // Ctrl+D on non-empty input

	if r.exitRequested {
		t.Error("Ctrl+D on non-empty input should NOT exit")
	}
}

// ── Ctrl+C in SQL Mode ───────────────────────────────────────

func TestHandleKeyInput_CtrlC_SQLMode(t *testing.T) {
	r := newInputTestREPL()
	r.sqlMode = true
	r.sqlBuffer = []string{"SELECT 1"}

	r.handleKeyInput([]byte{3}, 1) // Ctrl+C

	if r.sqlMode {
		t.Error("Ctrl+C in SQL mode should exit sqlMode")
	}
	if r.sqlBuffer != nil {
		t.Error("Ctrl+C in SQL mode should clear sqlBuffer")
	}
	if r.exitRequested {
		t.Error("Ctrl+C in SQL mode should NOT exit the REPL")
	}
}

// ── Tab Completion ───────────────────────────────────────────

func TestHandleKeyInput_Tab_CompletesCommand(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/he")
	r.completions = []string{"health", "help"}
	r.compIdx = 0

	r.handleKeyInput([]byte{9}, 1) // Tab

	if got := inputString(r); got != "/health " {
		t.Errorf("Tab completion: inputBuf = %q, want %q", got, "/health ")
	}
	if r.completions != nil {
		t.Error("completions should be nil after Tab")
	}
	if r.compIdx != -1 {
		t.Errorf("compIdx = %d, want -1 after Tab", r.compIdx)
	}
}

func TestHandleKeyInput_Tab_NoCompletions(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "hello")

	r.handleKeyInput([]byte{9}, 1) // Tab with no completions

	// Should be a no-op.
	if got := inputString(r); got != "hello" {
		t.Errorf("Tab no completions: inputBuf = %q, want %q", got, "hello")
	}
}

func TestHandleKeyInput_Tab_ArgCompletion(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/config se")
	r.completions = []string{"sentinel"}
	r.compIdx = 0
	r.compIsArg = true

	r.handleKeyInput([]byte{9}, 1) // Tab

	if got := inputString(r); got != "/config sentinel" {
		t.Errorf("Tab arg completion: inputBuf = %q, want %q", got, "/config sentinel")
	}
	if r.compIsArg {
		t.Error("compIsArg should be false after Tab")
	}
}

// ── ESC (dismiss completions / dropdown) ─────────────────────

func TestHandleKeyInput_ESC_ResetsCompIdx(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/s")
	r.completions = []string{"status", "sessions"}
	r.compIdx = 0

	r.handleKeyInput([]byte{27}, 1) // ESC (single byte, no '[')

	// ESC resets compIdx to -1 (deselects the highlighted completion).
	// Note: drawInputArea re-populates completions via updateCompletions when
	// input starts with "/", so completions may be non-nil after ESC.
	if r.compIdx != -1 {
		t.Errorf("ESC: compIdx = %d, want -1", r.compIdx)
	}
}

func TestHandleKeyInput_ESC_DismissesDropdown(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/login")
	r.dropdown = NewDropdown(KindLogin, []DropdownItem{
		{Label: "db1", Value: "db1"},
	})

	r.handleKeyInput([]byte{27}, 1) // ESC

	if r.dropdown != nil {
		t.Error("ESC should dismiss dropdown")
	}
}

// ── History Navigation ───────────────────────────────────────

func TestHandleKeyInput_ArrowUp_History(t *testing.T) {
	r := newInputTestREPL()
	r.connInfo = &ConnectionInfo{Name: "test"} // connected -> full history
	r.history = []string{"SELECT 1", "SELECT 2", "/status"}

	r.handleKeyInput([]byte{27, '[', 'A'}, 3) // Up

	if got := inputString(r); got != "/status" {
		t.Errorf("ArrowUp: inputBuf = %q, want %q", got, "/status")
	}
	if r.historyIdx != 2 {
		t.Errorf("historyIdx = %d, want 2", r.historyIdx)
	}
}

func TestHandleKeyInput_ArrowDown_History(t *testing.T) {
	r := newInputTestREPL()
	r.connInfo = &ConnectionInfo{Name: "test"}
	r.history = []string{"SELECT 1", "SELECT 2"}
	r.historyIdx = 0

	r.handleKeyInput([]byte{27, '[', 'B'}, 3) // Down

	if got := inputString(r); got != "SELECT 2" {
		t.Errorf("ArrowDown: inputBuf = %q, want %q", got, "SELECT 2")
	}
	if r.historyIdx != 1 {
		t.Errorf("historyIdx = %d, want 1", r.historyIdx)
	}
}

func TestHandleKeyInput_ArrowDown_PastEnd(t *testing.T) {
	r := newInputTestREPL()
	r.connInfo = &ConnectionInfo{Name: "test"}
	r.history = []string{"SELECT 1"}
	r.historyIdx = 0

	r.handleKeyInput([]byte{27, '[', 'B'}, 3) // Down past end

	if got := inputString(r); got != "" {
		t.Errorf("ArrowDown past end: inputBuf = %q, want empty", got)
	}
	if r.historyIdx != -1 {
		t.Errorf("historyIdx = %d, want -1", r.historyIdx)
	}
}

// ── Completion Navigation (Up/Down) ──────────────────────────

func TestHandleKeyInput_ArrowUp_Completions(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/s")
	r.completions = []string{"sessions", "sga", "status"}
	r.compIdx = 1

	r.handleKeyInput([]byte{27, '[', 'A'}, 3) // Up

	if r.compIdx != 0 {
		t.Errorf("compIdx = %d, want 0", r.compIdx)
	}
}

func TestHandleKeyInput_ArrowUp_CompletionsWrap(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/s")
	r.completions = []string{"sessions", "sga", "status"}
	r.compIdx = 0

	r.handleKeyInput([]byte{27, '[', 'A'}, 3) // Up wraps to last

	if r.compIdx != 2 {
		t.Errorf("compIdx = %d, want 2 (wrap)", r.compIdx)
	}
}

func TestHandleKeyInput_ArrowDown_Completions(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/s")
	r.completions = []string{"sessions", "sga", "status"}
	r.compIdx = 1

	r.handleKeyInput([]byte{27, '[', 'B'}, 3) // Down

	if r.compIdx != 2 {
		t.Errorf("compIdx = %d, want 2", r.compIdx)
	}
}

func TestHandleKeyInput_ArrowDown_CompletionsWrap(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/s")
	r.completions = []string{"sessions", "sga", "status"}
	r.compIdx = 2

	r.handleKeyInput([]byte{27, '[', 'B'}, 3) // Down wraps to first

	if r.compIdx != 0 {
		t.Errorf("compIdx = %d, want 0 (wrap)", r.compIdx)
	}
}

// ── filteredHistory ──────────────────────────────────────────

func TestFilteredHistory_Connected(t *testing.T) {
	r := newInputTestREPL()
	r.connInfo = &ConnectionInfo{Name: "db1"}
	r.history = []string{"SELECT 1", "/status", "/login db1"}

	got := r.filteredHistory()
	if len(got) != 3 {
		t.Errorf("connected: filteredHistory len = %d, want 3", len(got))
	}
}

func TestFilteredHistory_Disconnected(t *testing.T) {
	r := newInputTestREPL()
	r.history = []string{"SELECT 1", "/status", "/login db1", "/help", "/login db2", "/exit"}

	got := r.filteredHistory()

	// Only pre-login commands: /login db1, /help, /login db2, /exit.
	// Deduplicated by full text: "/login db1" and "/login db2" are distinct.
	// Order: chronological (oldest first).
	wantLen := 4
	if len(got) != wantLen {
		t.Errorf("disconnected: filteredHistory len = %d, want %d, got %v", len(got), wantLen, got)
	}
}

func TestFilteredHistory_Disconnected_Dedup(t *testing.T) {
	r := newInputTestREPL()
	r.history = []string{"/login db1", "/help", "/login db1"}

	got := r.filteredHistory()

	// "/login db1" appears twice — dedup keeps most recent (index 2).
	if len(got) != 2 {
		t.Fatalf("disconnected dedup: len = %d, want 2, got %v", len(got), got)
	}
	// Chronological order: /help then /login db1.
	if got[0] != "/help" {
		t.Errorf("got[0] = %q, want %q", got[0], "/help")
	}
	if got[1] != "/login db1" {
		t.Errorf("got[1] = %q, want %q", got[1], "/login db1")
	}
}

func TestFilteredHistory_Disconnected_Empty(t *testing.T) {
	r := newInputTestREPL()
	r.history = []string{"SELECT 1", "explain plan"}

	got := r.filteredHistory()
	if len(got) != 0 {
		t.Errorf("disconnected no commands: filteredHistory len = %d, want 0", len(got))
	}
}

// ── isPickerCommand ──────────────────────────────────────────

func TestIsPickerCommand(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"/login", true},
		{"/model", true},
		{"/m", true},
		{"/llm", true},
		{"/rule", true},
		{"/status", false},
		{"/login db1", false},
		{"login", false},
		{"", false},
		{"/login ", true}, // trailing space — TrimSpace handles it
	}
	for _, tc := range cases {
		if got := isPickerCommand(tc.input); got != tc.want {
			t.Errorf("isPickerCommand(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ── inputPosition ────────────────────────────────────────────

func TestInputPosition_NonScroll(t *testing.T) {
	r := newInputTestREPL()
	r.contentRow = 10
	r.scrollMode = false

	top, prompt, bot := r.inputPosition()

	if top != 10 {
		t.Errorf("topSep = %d, want 10", top)
	}
	if prompt != 11 {
		t.Errorf("promptRow = %d, want 11", prompt)
	}
	if bot != 12 {
		t.Errorf("botSep = %d, want 12", bot)
	}
}

func TestInputPosition_ScrollMode(t *testing.T) {
	r := newInputTestREPL()
	r.rows = 30
	r.scrollMode = true

	top, prompt, bot := r.inputPosition()

	if top != 28 {
		t.Errorf("topSep = %d, want 28", top)
	}
	if prompt != 29 {
		t.Errorf("promptRow = %d, want 29", prompt)
	}
	if bot != 30 {
		t.Errorf("botSep = %d, want 30", bot)
	}
}

// ── maxContentRow ────────────────────────────────────────────

func TestMaxContentRow(t *testing.T) {
	r := newInputTestREPL()
	r.rows = 24

	if got := r.maxContentRow(); got != 21 {
		t.Errorf("maxContentRow() = %d, want 21 (24 - 3)", got)
	}
}

// ── sortStrings ──────────────────────────────────────────────

func TestSortStrings(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"single", []string{"a"}, []string{"a"}},
		{"sorted", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"reversed", []string{"c", "b", "a"}, []string{"a", "b", "c"}},
		{"mixed", []string{"delta", "alpha", "charlie", "bravo"}, []string{"alpha", "bravo", "charlie", "delta"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Make a copy to avoid mutating test data.
			s := make([]string, len(tc.in))
			copy(s, tc.in)
			sortStrings(s)
			for i, v := range s {
				if v != tc.want[i] {
					t.Errorf("sortStrings[%d] = %q, want %q", i, v, tc.want[i])
				}
			}
		})
	}
}

// ── adjustScroll (picker helper) ─────────────────────────────

func TestAdjustScroll(t *testing.T) {
	cases := []struct {
		name       string
		scrollOff  int
		sel        int
		maxVisible int
		total      int
		wantOff    int
	}{
		{"within window", 0, 3, 6, 10, 0},
		{"below window", 0, 7, 6, 10, 2},
		{"above window", 5, 2, 6, 10, 2},
		{"wrap to start", 4, 0, 6, 10, 0},
		{"wrap to end", 0, 9, 6, 10, 4},
		{"small list", 0, 2, 6, 3, 0},
		{"negative clamp", -1, 0, 6, 10, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			off := tc.scrollOff
			adjustScroll(&off, tc.sel, tc.maxVisible, tc.total)
			if off != tc.wantOff {
				t.Errorf("adjustScroll(off=%d, sel=%d, max=%d, total=%d) = %d, want %d",
					tc.scrollOff, tc.sel, tc.maxVisible, tc.total, off, tc.wantOff)
			}
		})
	}
}

// ── pickerFromStrings / pickerFromLabelValue ──────────────────

func TestPickerFromStrings(t *testing.T) {
	items := pickerFromStrings([]string{"alpha", "beta"})
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	if items[0].Label != "alpha" || items[0].Value != "alpha" {
		t.Errorf("items[0] = %+v, want {alpha, alpha}", items[0])
	}
	if items[1].Label != "beta" || items[1].Value != "beta" {
		t.Errorf("items[1] = %+v, want {beta, beta}", items[1])
	}
}

func TestPickerFromStrings_Empty(t *testing.T) {
	items := pickerFromStrings(nil)
	if len(items) != 0 {
		t.Errorf("len = %d, want 0", len(items))
	}
}

func TestPickerFromLabelValue(t *testing.T) {
	items := pickerFromLabelValue(
		[]string{"Alpha", "Beta"},
		[]string{"a", "b"},
	)
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	if items[0].Label != "Alpha" || items[0].Value != "a" {
		t.Errorf("items[0] = %+v", items[0])
	}
}

func TestPickerFromLabelValue_UnequalLengths(t *testing.T) {
	items := pickerFromLabelValue(
		[]string{"A", "B", "C"},
		[]string{"x", "y"},
	)
	if len(items) != 2 {
		t.Errorf("len = %d, want 2 (min of 3, 2)", len(items))
	}
}

// ── officialDBTypeName ───────────────────────────────────────

func TestOfficialDBTypeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"oracle", "Oracle"},
		{"Oracle", "Oracle"},
		{"", "Oracle"},
		{"mysql", "MySQL"},
		{"MySQL", "MySQL"},
		{"postgres", "PostgreSQL"},
		{"postgresql", "PostgreSQL"},
		{"opengauss", "openGauss"},
		{"unknowndb", "unknowndb"},
	}
	for _, tc := range cases {
		if got := officialDBTypeName(tc.in); got != tc.want {
			t.Errorf("officialDBTypeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ── buildCompletionGhost ─────────────────────────────────────

func TestBuildCompletionGhost_NoCompletions(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/s")

	if got := r.buildCompletionGhost(); got != "" {
		t.Errorf("no completions: ghost = %q, want empty", got)
	}
}

func TestBuildCompletionGhost_CommandGhost(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/st")
	r.completions = []string{"status"}
	r.compIdx = 0

	got := r.buildCompletionGhost()
	if got != "atus" {
		t.Errorf("command ghost = %q, want %q", got, "atus")
	}
}

func TestBuildCompletionGhost_ArgGhost(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/config se")
	r.completions = []string{"sentinel"}
	r.compIdx = 0
	r.compIsArg = true

	got := r.buildCompletionGhost()
	if got != "ntinel" {
		t.Errorf("arg ghost = %q, want %q", got, "ntinel")
	}
}

func TestBuildCompletionGhost_ExactMatch_Picker(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/login")
	r.completions = []string{"login"}
	r.compIdx = 0

	got := r.buildCompletionGhost()
	if got != " ↵" {
		t.Errorf("exact match picker ghost = %q, want %q", got, " ↵")
	}
}

func TestBuildCompletionGhost_ExactMatch_NonPicker(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/status")
	r.completions = []string{"status"}
	r.compIdx = 0

	got := r.buildCompletionGhost()
	if got != "" {
		t.Errorf("exact match non-picker ghost = %q, want empty", got)
	}
}

func TestBuildCompletionGhost_NonSlashInput(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "SELECT")
	r.completions = []string{"status"}
	r.compIdx = 0

	got := r.buildCompletionGhost()
	if got != "" {
		t.Errorf("non-slash ghost = %q, want empty", got)
	}
}

// ── buildCompletionInput ─────────────────────────────────────

func TestBuildCompletionInput_Command(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/s")
	r.completions = []string{"status", "sessions"}

	got := string(r.buildCompletionInput(0))
	if got != "/status" {
		t.Errorf("buildCompletionInput(0) = %q, want %q", got, "/status")
	}

	got = string(r.buildCompletionInput(1))
	if got != "/sessions" {
		t.Errorf("buildCompletionInput(1) = %q, want %q", got, "/sessions")
	}
}

func TestBuildCompletionInput_Arg(t *testing.T) {
	r := newInputTestREPL()
	setInput(r, "/config se")
	r.completions = []string{"sentinel"}
	r.compIsArg = true

	got := string(r.buildCompletionInput(0))
	if got != "/config sentinel" {
		t.Errorf("buildCompletionInput arg = %q, want %q", got, "/config sentinel")
	}
}

// ── buildPrompt ──────────────────────────────────────────────

func TestBuildPrompt_NoConnection(t *testing.T) {
	r := newInputTestREPL()

	got := r.buildPrompt()
	// Should contain the prompt character.
	if got == "" {
		t.Error("buildPrompt() should not be empty")
	}
	// Should NOT contain connection info (check stripped text).
	stripped := stripAnsi(got)
	if containsSubstr(stripped, "(") {
		t.Error("buildPrompt() should not contain connection info when disconnected")
	}
}

func TestBuildPrompt_WithConnection(t *testing.T) {
	r := newInputTestREPL()
	r.connInfo = &ConnectionInfo{Name: "prod-db"}

	got := r.buildPrompt()
	stripped := stripAnsi(got)
	if !containsSubstr(stripped, "prod-db") {
		t.Errorf("buildPrompt() stripped = %q, should contain %q", stripped, "prod-db")
	}
}

func TestBuildPrompt_WithQueue(t *testing.T) {
	r := newInputTestREPL()
	r.cmdQueue = []string{"cmd1", "cmd2"}

	got := r.buildPrompt()
	stripped := stripAnsi(got)
	if !containsSubstr(stripped, "2") {
		t.Errorf("buildPrompt() stripped = %q, should indicate 2 queued commands", stripped)
	}
}

// containsSubstr checks if s contains sub.
func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── Multi-byte input in single handleKeyInput call ───────────

func TestHandleKeyInput_MultipleBytesInOneCall(t *testing.T) {
	r := newInputTestREPL()

	// Simulate typing "abc" in a single read buffer.
	r.handleKeyInput([]byte{'a', 'b', 'c'}, 3)

	if got := inputString(r); got != "abc" {
		t.Errorf("inputBuf = %q, want %q", got, "abc")
	}
	if r.cursorPos != 3 {
		t.Errorf("cursorPos = %d, want 3", r.cursorPos)
	}
}

// ── Dropdown navigation via arrow keys ───────────────────────

func TestHandleKeyInput_ArrowUp_Dropdown_NoPanic(t *testing.T) {
	// When a dropdown is active, Arrow Up should call dropdown.MoveUp
	// and drawInputArea. drawInputArea may clear the dropdown (if the
	// context dropdown is not applicable), so we just verify no panic.
	r := newInputTestREPL()
	setInput(r, "/login")
	r.dropdown = NewDropdown(KindLogin, []DropdownItem{
		{Label: "db1", Value: "db1"},
		{Label: "db2", Value: "db2"},
		{Label: "db3", Value: "db3"},
	})

	// Should not panic.
	r.handleKeyInput([]byte{27, '[', 'A'}, 3)

	// History should NOT have been consulted (dropdown takes priority).
	if r.historyIdx != -1 {
		t.Errorf("historyIdx = %d, want -1 (dropdown should prevent history nav)", r.historyIdx)
	}
}

func TestHandleKeyInput_ArrowDown_Dropdown_NoPanic(t *testing.T) {
	// Same as ArrowUp: verify no panic when dropdown is active.
	r := newInputTestREPL()
	setInput(r, "/login")
	r.dropdown = NewDropdown(KindLogin, []DropdownItem{
		{Label: "db1", Value: "db1"},
		{Label: "db2", Value: "db2"},
	})
	r.dropdown.SelectedIdx = 0

	// Should not panic.
	r.handleKeyInput([]byte{27, '[', 'B'}, 3)

	// History should NOT have been consulted.
	if r.historyIdx != -1 {
		t.Errorf("historyIdx = %d, want -1 (dropdown should prevent history nav)", r.historyIdx)
	}
}
