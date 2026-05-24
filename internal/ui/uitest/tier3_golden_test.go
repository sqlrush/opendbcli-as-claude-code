/*-------------------------------------------------------------------------
 *
 * tier3_golden_test.go
 *    Tier 3 live PTY golden checks for long-output TUI regressions.
 *
 * Requires OPENDB_GOLDEN_CONN and a prebuilt dbaa/opendb binary. The CI-safe
 * renderer contracts live in package ui tests; this file exercises the real
 * terminal path with midterm.
 *
 *-------------------------------------------------------------------------
 */
package uitest

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

func TestTier3LivePTY_CurrentDiagnosisRendering(t *testing.T) {
	conn := strings.TrimSpace(os.Getenv("OPENDB_GOLDEN_CONN"))
	if conn == "" {
		t.Skip("set OPENDB_GOLDEN_CONN to run live Tier 3 PTY golden checks")
	}
	if os.Getenv("OPENDB_GOLDEN_TUI") != "1" {
		t.Skip("set OPENDB_GOLDEN_TUI=1 to run live Tier 3 PTY golden checks")
	}

	tt := NewTestTerminal(t, 44, 140)
	defer tt.Close()
	if err := tt.WaitFor(`[❯>]`, 20*time.Second); err != nil {
		t.Fatalf("dbaa did not start: %v", err)
	}

	selectConnection(t, tt, conn)

	tt.SendLine("当前数据库存在什么问题")
	if err := tt.WaitFor(`(总结|当前在线问题|建议动作|根因分析)`, 4*time.Minute); err != nil {
		t.Fatalf("diagnosis did not render: %v", err)
	}
	waitForQuiet(t, tt, 1200*time.Millisecond, 8*time.Second)

	rawPlain := stripANSILocal(tt.RawOutput())
	for _, want := range []string{"第1轮:", "第2轮:"} {
		if !strings.Contains(rawPlain, want) {
			t.Fatalf("raw transcript missing progress marker %q\n%s", want, compactTail(rawPlain, 2500))
		}
	}
	for _, bad := range []string{"WDR_REPORT_BEGIN", "generate_wdr_report(integer"} {
		if strings.Contains(rawPlain, bad) {
			t.Fatalf("raw transcript contains forbidden internal token %q\n%s", bad, compactTail(rawPlain, 2500))
		}
	}

	screen := tt.Screen()
	for _, want := range []string{"┃", "┌", "└"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("screen missing rendered table/section token %q\n%s", want, screen)
		}
	}
	if hasRepeatedTailLines(screen, 5) {
		t.Fatalf("screen tail appears duplicated\n%s", screen)
	}
	tt.AssertNoOverflow(t)
}

func selectConnection(t *testing.T, tt *TestTerminal, conn string) {
	t.Helper()
	tt.SendLine("/login")
	if err := tt.WaitFor(regexp.QuoteMeta(conn), 10*time.Second); err != nil {
		t.Fatalf("login picker did not show %q: %v", conn, err)
	}
	idx := pickerIndexFromScreen(tt.Screen(), conn)
	if idx < 0 {
		t.Fatalf("could not locate %q in login picker\n%s", conn, tt.Screen())
	}
	for i := 0; i < idx; i++ {
		tt.SendKey([]byte{0x1b, '[', 'B'})
		time.Sleep(100 * time.Millisecond)
	}
	tt.SendKey([]byte{0x0d})
	if err := tt.WaitFor(`Connected to `+regexp.QuoteMeta(conn), 30*time.Second); err != nil {
		t.Fatalf("login to %q did not complete: %v", conn, err)
	}
}

func pickerIndexFromScreen(screen, conn string) int {
	plain := stripANSILocal(screen)
	idx := -1
	for _, line := range strings.Split(plain, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "▸"))
		if line == "" || strings.Contains(line, "名称") || strings.Contains(line, "────") || strings.Contains(line, "❯") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Login picker rows start with connection name followed by db type.
		if fields[1] != "opengauss" && fields[1] != "gaussdb" && fields[1] != "oracle" && fields[1] != "mysql" && fields[1] != "postgres" {
			continue
		}
		idx++
		if fields[0] == conn {
			return idx
		}
	}
	return -1
}

func waitForQuiet(t *testing.T, tt *TestTerminal, quietFor, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := tt.RawOutput()
	lastChange := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		cur := tt.RawOutput()
		if cur != last {
			last = cur
			lastChange = time.Now()
			continue
		}
		if time.Since(lastChange) >= quietFor {
			return
		}
	}
}

func stripANSILocal(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*(\x07|\x1b\\)`)
	return re.ReplaceAllString(s, "")
}

func compactTail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func hasRepeatedTailLines(screen string, n int) bool {
	var lines []string
	for _, line := range strings.Split(screen, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || runewidth.StringWidth(line) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) < 2*n {
		return false
	}
	for i := 0; i < n; i++ {
		if lines[len(lines)-1-i] != lines[len(lines)-1-n-i] {
			return false
		}
	}
	return true
}
