/*-------------------------------------------------------------------------
 *
 * welcome_test.go
 *	  Test cases for welcome.go (ui package): TestWelcomeLineWidths,
 *	  TestWelcomeLineWidths_EastAsian.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/welcome_test.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"testing"

	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/config"
)

// TestWelcomeLineWidths verifies every line in the welcome page has exactly
// the expected visible width (terminal columns). A mismatch means the right
// border wraps to the next line, causing the "贴图错误" rendering bug.
func TestWelcomeLineWidths(t *testing.T) {
	for _, cols := range []int{80, 100, 120, 140} {
		t.Run(fmt.Sprintf("cols=%d", cols), func(t *testing.T) {
			r := &REPL{
				cols:    cols,
				rows:    30,
				connMgr: stubConnMgr(t),
			}
			lines := r.buildWelcomeLines()
			for i, line := range lines {
				vw := visibleWidth(line)
				if vw != cols {
					plain := stripAnsi(line)
					t.Errorf("line %d: visibleWidth=%d, want %d\n  plain: %q", i, vw, cols, plain)
				}
			}
		})
	}
}

// TestWelcomeLineWidths_EastAsian tests with CJK terminal width assumptions.
// On CJK terminals, ambiguous-width chars (·, ▗, ▄, etc.) may render as 2 wide.
// This test uses runewidth.StringWidth (same as displayWidth) to verify.
func TestWelcomeLineWidths_EastAsian(t *testing.T) {
	// This test verifies the calculation is internally consistent.
	// It does NOT test terminal rendering (which depends on the actual terminal).
	r := &REPL{
		cols:    120,
		rows:    30,
		connMgr: stubConnMgr(t),
	}
	lines := r.buildWelcomeLines()
	t.Logf("Welcome page: %d lines for %d cols", len(lines), r.cols)
	for i, line := range lines {
		vw := visibleWidth(line)
		plain := stripAnsi(line)
		t.Logf("  line %02d: vw=%3d  plain_len=%3d  %s", i, vw, len(plain), truncStr(plain, 60))
		if vw != r.cols {
			t.Errorf("line %d: visibleWidth=%d, want %d", i, vw, r.cols)
		}
	}
}

func truncStr(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max-1]) + "…"
	}
	return s
}

// stubConnMgr creates a minimal connection manager with test connections.
func stubConnMgr(t *testing.T) *connection.Manager {
	t.Helper()
	cfg := &config.Config{
		ConnectionsDir: t.TempDir(),
	}
	m, err := connection.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Add test connections to simulate the user's setup.
	_ = m.SaveConnection(config.Connection{
		Name:    "oratest",
		Host:    "127.0.0.1",
		Port:    1521,
		Service: "orcl",
	}, "")
	_ = m.SaveConnection(config.Connection{
		Name:    "orcl-test",
		Host:    "127.0.0.1",
		Port:    1521,
		Service: "orcl",
	}, "")
	return m
}
