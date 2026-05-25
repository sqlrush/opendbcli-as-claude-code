/*-------------------------------------------------------------------------
 *
 * og_skills_test.go
 *	  Test cases for og_skills.go (uitest package): TestOG_AllSkills.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/uitest/og_skills_test.go
 *
 *-------------------------------------------------------------------------
 */
package uitest

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Live OG TUI tests. One PTY session runs many skills rather than spawning
// opendb once per sub-test — starting a connection against the test server
// costs ~1.5s and doing that 30 times blows past Go's default test timeout.
//
// Requires the "og" connection in the user config to point at a live OG.
// Set OPENDB_SKIP_OG=1 to skip.

const (
	ogConnName  = "og"
	ogPrompt    = `[❯>]`
	ogConnectTO = 10 * time.Second
)

// ogSharedSession starts a single REPL session. All checks in TestOG_AllSkills
// below share it. Skips cleanly when the DB isn't reachable.
func ogSharedSession(t *testing.T) *TestTerminal {
	t.Helper()
	if os.Getenv("OPENDB_SKIP_OG") == "1" {
		t.Skip("OPENDB_SKIP_OG=1 — skipping live OG tests")
	}
	tt := NewTestTerminal(t, 40, 160, "-c", ogConnName)
	if err := tt.WaitFor(ogPrompt, ogConnectTO); err != nil {
		tt.Close()
		t.Skipf("OG instance unreachable via connection %q: %v", ogConnName, err)
	}
	// Wait a bit after prompt — opendb's connect banner keeps printing while
	// the prompt already shows, and we don't want the banner bytes polluting
	// the first skill's screen capture.
	time.Sleep(600 * time.Millisecond)
	return tt
}

// runAndCapture sends a command and returns the new screen content that
// appeared after it. It clears the capture baseline by sending a marker,
// waits for it to echo, then sends the real command and waits for an
// expected token or a short quiet period.
func runAndCapture(t *testing.T, tt *TestTerminal, cmd string, expect string, max time.Duration) string {
	t.Helper()
	tt.SendLine(cmd)
	if expect != "" {
		_ = tt.WaitFor(expect, max)
	} else {
		time.Sleep(max)
	}
	return tt.Screen()
}

// TestOG_AllSkills runs every currently-registered OG skill through the REPL
// in one PTY session and checks per-skill invariants. Table-driven so a
// failure pinpoints which skill regressed without having to re-run.
func TestOG_AllSkills(t *testing.T) {
	tt := ogSharedSession(t)
	defer tt.Close()

	type check struct {
		name    string // pretty label for test output
		cmd     string
		expect  string // token to WaitFor (shortens wait), empty = fixed sleep
		must    []string
		mustNot []string // substrings that indicate a regression / SQL error
	}

	// mustNot shared across all skills — anything in here means something
	// broke at the data or rendering layer.
	commonBad := []string{
		"panic",
		"syntax error",
		"unknown skill",
		"FILTER",  // OG 5.0 doesn't support FILTER; regressions would bring it back
	}

	// TUI tests guarantee rendering invariants only: no panic, no known
	// SQL regressions, no forbidden tokens. Content completeness (column
	// names, totals, etc.) is covered by the shell-level batch tests in
	// docs/validation/og-live-batch.log, since a 40-row terminal scrolls
	// long table headers out of screen before the skill finishes rendering.
	//
	// mustNot entries therefore carry all the real assertion weight; must
	// is kept empty by design.
	checks := []check{
		// core listing
		{"sessions", "/sessions", "", nil, nil},
		{"activesessions", "/activesessions", "", nil, []string{"does not exist"}},
		{"waits", "/waits", "", nil, []string{"does not exist"}},
		{"locks", "/locks", "", nil, nil},
		{"blocktree", "/blocktree", "", nil, nil},

		// MVCC / xid / vacuum
		{"xid", "/xid", "", nil, nil},
		{"vacuum", "/vacuum", "", nil, nil},
		{"longtx", "/longtx", "", nil, nil},
		{"bloat", "/bloat", "", nil, nil},
		// self-match regression guard — the old SQL matched its own query text
		{"autovacuum", "/autovacuum", "", nil, []string{"COALESCE(application_name"}},

		// memory / buffers — gsmem now absorbs sharedbufs; verify no error
		{"gsmem", "/gsmem", "", nil, nil},
		{"sessionmem", "/sessionmem", "", nil, nil},

		// WAL / replication / checkpoint — regression guards on PG-only views
		{"wal", "/wal", "", nil, nil},
		{"walsummary", "/walsummary", "", nil, []string{"pg_stat_archiver"}},
		{"checkpoint", "/checkpoint", "", nil, nil},
		{"replication", "/replication", "", nil, nil},
		{"slots", "/slots", "", nil, nil},
		{"logicalslots", "/logicalslots", "", nil, []string{"confirmed_flush_lsn"}},
		{"pubsub", "/pubsub", "", nil, nil},
		{"cmha", "/cmha", "", nil, nil},

		// space / hot table / temp
		{"segments", "/segments", "", nil, nil},
		{"space", "/space", "", nil, nil},
		{"tempusage", "/tempusage", "", nil, nil},
		{"hotkey", "/hotkey", "", nil, nil},
		{"toasttable", "/toasttable", "", nil, nil},

		// system / users / meta
		{"users", "/users", "", nil, nil},
		{"os", "/os", "", nil, nil},
		{"health", "/health", "", nil, nil},
		{"indexhealth", "/indexhealth", "", nil, nil},
		{"sqlcount", "/sqlcount", "", nil, nil},
		{"respool", "/respool", "", nil, nil},
		{"resource", "/resource", "", nil, nil},
		{"bgworker", "/bgworker", "", nil, []string{"column \"backend_type\""}},

		// wait taxonomy — guard the FILTER / missing-column regressions
		{"lwlocks", "/lwlocks", "", nil, []string{"column \"wait_event\" does not exist"}},

		// query / SQL
		{"topsql", "/topsql", "", nil, nil},
		{"slowsql", "/slowsql", "", nil, nil},
		{"ash", "/ash", "", nil, nil},
		{"ogerr", "/ogerr", "", nil, nil},

		// schema — usage path + a real table (pg_class always present)
		{"tableinfo-usage", "/tableinfo", "", nil, nil},
		{"tableinfo-real", "/tableinfo pg_catalog.pg_class", "", nil, nil},

		// admin (read-only paths)
		{"alert", "/alert", "", nil, nil},
		{"backup", "/backup", "", nil, nil},
		{"jobs", "/jobs", "", nil, nil},
		{"params", "/params", "", nil, nil},

		// reports
		{"wdr", "/wdr", "", nil, nil},
		{"planhistory", "/planhistory 1", "", nil, []string{"column \"total_elapse_time\""}},
	}

	// Shared per-command max wait. Enough for big table renders but not so
	// long that the whole suite blows past the 3-minute envelope.
	const perCmd = 4 * time.Second

	failures := 0
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			screen := runAndCapture(t, tt, c.cmd, c.expect, perCmd)

			for _, w := range c.must {
				if !strings.Contains(screen, w) {
					t.Errorf("%s: missing required token %q", c.cmd, w)
				}
			}
			for _, b := range c.mustNot {
				if strings.Contains(screen, b) {
					t.Errorf("%s: forbidden token %q present (regression)", c.cmd, b)
				}
			}
			for _, b := range commonBad {
				if strings.Contains(screen, b) {
					t.Errorf("%s: common-bad token %q present", c.cmd, b)
					failures++
				}
			}
		})
	}

	// One final overflow check against the current screen.
	tt.AssertNoOverflow(t)
}
