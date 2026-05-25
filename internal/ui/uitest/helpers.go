/*-------------------------------------------------------------------------
 *
 * helpers.go
 *	  Package uitest provides a TestTerminal helper that launches opendb
 *	  in a PTY and uses an in-memory terminal emulator (midterm) to
 *	  capture the rendered screen — exactly what a human would see.
 *	  This enables golden-file comparison and precise coordinate-level
 *	  assertions for detecting misalignment, overflow, and rendering
 *	  regressions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/uitest/helpers.go
 *
 *-------------------------------------------------------------------------
 */
// Package uitest provides a TestTerminal helper that launches opendb in a PTY
// and uses an in-memory terminal emulator (midterm) to capture the rendered
// screen — exactly what a human would see. This enables golden-file comparison
// and precise coordinate-level assertions for detecting misalignment, overflow,
// and rendering regressions.
package uitest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"
	"github.com/creack/pty"
	"github.com/mattn/go-runewidth"
	"github.com/vito/midterm"
)

func init() {
	// Match opendb's setting: ambiguous-width chars (box-drawing, etc.) = width 1
	runewidth.DefaultCondition.EastAsianWidth = false
}

// TestTerminal wraps a PTY-launched process and an in-memory terminal emulator.
// All output from the process is fed into midterm, which processes ANSI escape
// sequences (scroll regions, alternate screen, colors, cursor movement) and
// maintains a virtual screen buffer — the same image a human would see.
type TestTerminal struct {
	ptmx *os.File
	term *midterm.Terminal
	cmd  *exec.Cmd
	rows int
	cols int

	mu     sync.Mutex
	closed bool
	done   chan struct{} // closed when PTY read goroutine exits
	raw    bytes.Buffer  // full raw PTY transcript for progress/dedupe assertions

	waitOnce sync.Once
	waitErr  error
}

// NewTestTerminal starts the opendb binary in a PTY of the given size.
// Extra args are appended to the opendb command (e.g. "-c", "oratest").
// The binary must be built before running tests (go build -o opendb .).
func NewTestTerminal(t *testing.T, rows, cols int, args ...string) *TestTerminal {
	t.Helper()

	binPath := findBinary(t)

	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("COLUMNS=%d", cols),
		fmt.Sprintf("LINES=%d", rows),
		"TERM=xterm-256color",
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		t.Fatalf("pty.StartWithSize: %v", err)
	}

	term := midterm.NewTerminal(rows, cols)
	term.Raw = true // PTY output already includes \r

	// Critical: opendb sends terminal queries (CSI 6n cursor position,
	// OSC 11 background color). midterm processes these and generates
	// responses — we pipe them back to the PTY so opendb doesn't hang.
	term.ForwardResponses = ptmx

	tt := &TestTerminal{
		ptmx: ptmx,
		term: term,
		cmd:  cmd,
		rows: rows,
		cols: cols,
		done: make(chan struct{}),
	}

	// Bridge PTY output → midterm (goroutine, blocks until PTY closes).
	// Writes are serialized through tt.mu to avoid data races with Screen()/CellAt()/RowText().
	go func() {
		defer close(tt.done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				tt.mu.Lock()
				tt.raw.Write(buf[:n])
				term.Write(buf[:n]) //nolint:errcheck
				tt.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	return tt
}

// SendLine writes a line of text followed by Enter to the PTY.
func (tt *TestTerminal) SendLine(s string) {
	tt.ptmx.Write([]byte(s + "\n")) //nolint:errcheck
}

// SendKey writes raw bytes to the PTY (for Ctrl+C, ESC, arrow keys, etc.).
//
// Common keys:
//
//	Ctrl+C: []byte{0x03}
//	Ctrl+D: []byte{0x04}
//	ESC:    []byte{0x1b}
//	Up:     []byte{0x1b, '[', 'A'}
//	Down:   []byte{0x1b, '[', 'B'}
//	Tab:    []byte{0x09}
func (tt *TestTerminal) SendKey(key []byte) {
	tt.ptmx.Write(key) //nolint:errcheck
}

// WaitFor polls the terminal screen until the given regex matches, or the
// timeout expires. It checks the entire screen content.
func (tt *TestTerminal) WaitFor(pattern string, timeout time.Duration) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		screen := tt.Screen()
		if re.MatchString(screen) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %q after %v\n--- screen ---\n%s", pattern, timeout, tt.Screen())
}

// RawOutput returns the full PTY byte stream captured so far. Unlike Screen,
// this includes content that has scrolled off-screen and is useful for
// asserting progress messages and duplicate tail rendering regressions.
func (tt *TestTerminal) RawOutput() string {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	return tt.raw.String()
}

// Screen returns the full terminal screen as a string (rows joined by \n).
// This is the "what a human sees" representation — ANSI sequences are already
// processed by midterm and the result is plain text with correct positioning.
func (tt *TestTerminal) Screen() string {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	lines := make([]string, tt.rows)
	for row := 0; row < tt.rows && row < len(tt.term.Content); row++ {
		lines[row] = strings.TrimRight(string(tt.term.Content[row]), " ")
	}
	return strings.Join(lines, "\n")
}

// CellAt returns the rune at the given (row, col) position.
// Returns ' ' if out of bounds.
func (tt *TestTerminal) CellAt(row, col int) rune {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	if row < 0 || row >= len(tt.term.Content) {
		return ' '
	}
	rowContent := tt.term.Content[row]
	if col < 0 || col >= len(rowContent) {
		return ' '
	}
	return rowContent[col]
}

// RowText returns the text content of a specific row (0-indexed), trimmed of
// trailing spaces.
func (tt *TestTerminal) RowText(row int) string {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	if row < 0 || row >= len(tt.term.Content) {
		return ""
	}
	return strings.TrimRight(string(tt.term.Content[row]), " ")
}

// Resize changes the terminal size and sends SIGWINCH to the process.
func (tt *TestTerminal) Resize(rows, cols int) {
	tt.mu.Lock()
	tt.rows = rows
	tt.cols = cols
	tt.term.Resize(rows, cols)
	tt.mu.Unlock()
	pty.Setsize(tt.ptmx, &pty.Winsize{ //nolint:errcheck
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

// RequireGolden compares the current screen against a golden file at
// testdata/<TestName>.golden. Run with -update to regenerate golden files.
//
// The screen is passed through normalizeGolden before comparison to elide
// volatile content (e.g. version numbers in the welcome header), so golden
// files stay stable across version bumps.
func (tt *TestTerminal) RequireGolden(t *testing.T) {
	t.Helper()
	golden.RequireEqual(t, normalizeGolden(tt.Screen()))
}

// welcomeHeaderRe matches the welcome banner top border that embeds the
// version string, e.g. "╭─── OpenDB v1.1.04 ────...───╮".
var welcomeHeaderRe = regexp.MustCompile(`╭─+ OpenDB v\d+\.\d+\.\d+ ─+╮`)

// normalizeGolden rewrites volatile content in the rendered screen so golden
// files do not drift on cosmetic changes (version bumps, etc.).
//
// Currently normalizes the welcome header (which bakes in the version number)
// to a pure-border line of the same rune width, preserving downstream row
// alignment while discarding the version string.
func normalizeGolden(s string) string {
	return welcomeHeaderRe.ReplaceAllStringFunc(s, func(match string) string {
		inner := len([]rune(match)) - 2 // minus the two corner runes
		return "╭" + strings.Repeat("─", inner) + "╮"
	})
}

// waitForCmd ensures cmd.Wait() is called exactly once, preventing data races
// when multiple goroutines attempt to wait on the same process.
func (tt *TestTerminal) waitForCmd() error {
	tt.waitOnce.Do(func() { tt.waitErr = tt.cmd.Wait() })
	return tt.waitErr
}

// Close kills the process and cleans up resources.
func (tt *TestTerminal) Close() {
	tt.mu.Lock()
	if tt.closed {
		tt.mu.Unlock()
		return
	}
	tt.closed = true
	tt.mu.Unlock()

	// Send Ctrl+C then kill
	tt.ptmx.Write([]byte{0x03}) //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	if tt.cmd.Process != nil {
		tt.cmd.Process.Kill() //nolint:errcheck
	}
	tt.ptmx.Close() //nolint:errcheck

	// Wait for PTY read goroutine to finish
	select {
	case <-tt.done:
	case <-time.After(3 * time.Second):
	}

	tt.waitForCmd() //nolint:errcheck
}

// AssertNoOverflow checks that no visible row exceeds the terminal width.
func (tt *TestTerminal) AssertNoOverflow(t *testing.T) {
	t.Helper()
	for row := 0; row < tt.rows; row++ {
		line := tt.RowText(row)
		w := runewidth.StringWidth(line)
		if w > tt.cols {
			t.Errorf("row %d overflows: width=%d > %d, content=%q", row, w, tt.cols, line)
		}
	}
}

// AssertContains checks that the screen contains the given substring.
func (tt *TestTerminal) AssertContains(t *testing.T, substr string) {
	t.Helper()
	screen := tt.Screen()
	if !strings.Contains(screen, substr) {
		t.Errorf("screen does not contain %q\n--- screen ---\n%s", substr, screen)
	}
}

// AssertNotContains checks that the screen does NOT contain the given substring.
func (tt *TestTerminal) AssertNotContains(t *testing.T, substr string) {
	t.Helper()
	screen := tt.Screen()
	if strings.Contains(screen, substr) {
		t.Errorf("screen unexpectedly contains %q", substr)
	}
}

// ProcessExited returns true if the opendb process has already exited.
func (tt *TestTerminal) ProcessExited() bool {
	if tt.cmd.ProcessState != nil {
		return tt.cmd.ProcessState.Exited()
	}
	// Try a non-blocking wait
	done := make(chan error, 1)
	go func() { done <- tt.waitForCmd() }()
	select {
	case <-done:
		return true
	case <-time.After(200 * time.Millisecond):
		return false
	}
}

// findBinary locates the opendb binary. It checks:
// 1. OPENDB_BIN env var
// 2. ./opendb in the repo root
// 3. opendb in PATH
func findBinary(t *testing.T) string {
	t.Helper()

	if bin := os.Getenv("OPENDB_BIN"); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}

	// Try repo root (assumes tests run from repo root or internal/ui/uitest/)
	candidates := []string{"./opendb", "../../../opendb", "opendb"}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}

	t.Skip("opendb binary not found — run 'go build -o opendb .' first")
	return ""
}
