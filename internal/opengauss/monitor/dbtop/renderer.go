/*-------------------------------------------------------------------------
 *
 * renderer.go
 *	  Render produces exactly 28 lines of the dbtop dashboard. cols is
 *	  the terminal width; intervalSec is the refresh interval for
 *	  display.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/monitor/dbtop/renderer.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import (
	"fmt"
	"math"
	"strings"

	runewidth "github.com/mattn/go-runewidth"
)

func init() {
	// ▇ and ░ have East_Asian_Width=Ambiguous. CJK locale makes go-runewidth
	// treat them as width 2, but terminals render them as width 1.
	runewidth.DefaultCondition.EastAsianWidth = false
}

const (
	totalLines     = 28
	headerLines    = 5  // top + empty + metrics + counts + bottom
	eventsLines    = 8  // top + header + 5 data + bottom
	sessionsLines  = 14 // top + header + 10 data + trunc + bottom
	statusLines    = 1
	maxEvents      = 5
	maxSessionRows = 10
	barWidth       = 8
)

// Render produces exactly 28 lines of the dbtop dashboard.
// cols is the terminal width; intervalSec is the refresh interval for display.
func Render(snap Snapshot, cols int, intervalSec int) []string {
	if cols < 40 {
		cols = 40
	}

	var lines []string
	lines = append(lines, renderHeaderBox(snap, cols)...)
	lines = append(lines, renderEventsBox(snap, cols)...)
	lines = append(lines, renderSessionsBox(snap, cols)...)
	lines = append(lines, renderStatusBar(snap, cols, intervalSec))

	for len(lines) < totalLines {
		lines = append(lines, "")
	}
	return lines[:totalLines]
}

// ── Header Box (5 lines) ──

func renderHeaderBox(snap Snapshot, cols int) []string {
	ts := snap.Timestamp.Format("15:04:05")
	healthStr := colorHealth(snap.Health)

	ver := extractMajorVersion(snap.Version)

	title := fmt.Sprintf("dbtop ── %s ── %s ── %s ── %s ── %s",
		ver, snap.InstanceName, snap.DBRole, healthStr, ts)

	lines := make([]string, 0, headerLines)
	lines = append(lines, boxTop(title, cols))
	lines = append(lines, boxEmpty(cols))
	lines = append(lines, boxLine(renderMetricsLine(snap), cols))
	lines = append(lines, boxLine(renderCountsLine(snap), cols))
	lines = append(lines, boxBottom(cols))
	return lines
}

func extractMajorVersion(version string) string {
	if version == "" {
		return "opengauss"
	}
	// OpenGauss version() returns something like "(openGauss x.y.z ...)"
	// or may contain "openGauss" keyword. Extract major version.
	lower := strings.ToLower(version)
	if idx := strings.Index(lower, "opengauss"); idx >= 0 {
		rest := strings.TrimSpace(version[idx+len("opengauss"):])
		parts := strings.SplitN(rest, ".", 2)
		return "opengauss " + parts[0]
	}
	parts := strings.SplitN(version, ".", 2)
	return "opengauss " + parts[0]
}

func renderMetricsLine(snap Snapshot) string {
	sBufStr := formatSizeMB(snap.SBufSizeMB)
	cacheStr := fmt.Sprintf("%.1f%%", snap.CacheHitPct)
	if snap.CacheHitPct > 0 && snap.CacheHitPct < cacheHitWarn {
		cacheStr = colorWarn(cacheStr)
	}
	walStr := formatWALRate(snap.WALKBs)

	return fmt.Sprintf("SBuf %s  CacheHit %s  WAL %s",
		sBufStr, cacheStr, walStr)
}

func renderCountsLine(snap Snapshot) string {
	anStr := formatComma(int64(snap.ActiveCount))
	if float64(snap.ActiveCount) > float64(anCrit) {
		anStr = colorCrit(anStr)
	} else if float64(snap.ActiveCount) > float64(anWarn) {
		anStr = colorWarn(anStr)
	}

	left := fmt.Sprintf("TPS %s  QPS %s  SN %s  AN %s  Wait %s",
		formatRate(snap.TPS),
		formatRate(snap.QPS),
		formatComma(int64(snap.TotalSessions)),
		anStr,
		formatComma(int64(snap.WaitingCount)))

	return left
}

// ── Events Box (8 lines) ──

func renderEventsBox(snap Snapshot, cols int) []string {
	lines := make([]string, 0, eventsLines)
	lines = append(lines, boxTop("Top Wait Events", cols))

	lines = append(lines, boxLine(formatEventHeader(), cols))

	eventCount := len(snap.Events)
	if eventCount > maxEvents {
		eventCount = maxEvents
	}
	for i := 0; i < maxEvents; i++ {
		if i < eventCount {
			lines = append(lines, boxLine(formatEventRow(snap.Events[i]), cols))
		} else {
			lines = append(lines, boxEmpty(cols))
		}
	}

	lines = append(lines, boxBottom(cols))
	return lines
}

func formatEventHeader() string {
	return fmt.Sprintf("%-20s %-14s %8s",
		"EVENT", "TYPE", "SESSIONS")
}

func formatEventRow(ev WaitEvent) string {
	evName := truncStr(ev.Event, 20)
	evType := truncStr(ev.WaitType, 14)
	return fmt.Sprintf("%-20s %-14s %8d", evName, evType, ev.Sessions)
}

// ── Sessions Box (14 lines) ──

func renderSessionsBox(snap Snapshot, cols int) []string {
	lines := make([]string, 0, sessionsLines)

	title := fmt.Sprintf("Active Sessions (%d)", snap.ActiveCount)
	lines = append(lines, boxTop(title, cols))

	lines = append(lines, boxLine(formatSessionHeader(cols), cols))

	innerW := cols - 4
	if innerW < 36 {
		innerW = 36
	}

	sessCount := len(snap.Sessions)
	if sessCount > maxSessionRows {
		sessCount = maxSessionRows
	}

	shownCount := sessCount

	for i := 0; i < maxSessionRows; i++ {
		if i < sessCount {
			lines = append(lines, boxLine(formatSessionRow(snap.Sessions[i], innerW), cols))
		} else {
			lines = append(lines, boxEmpty(cols))
		}
	}

	// Truncation hint line
	if snap.ActiveCount > shownCount && shownCount > 0 {
		remaining := snap.ActiveCount - shownCount
		hint := fmt.Sprintf("... %d more active sessions", remaining)
		lines = append(lines, boxLine(hint, cols))
	} else {
		lines = append(lines, boxEmpty(cols))
	}

	lines = append(lines, boxBottom(cols))
	return lines
}

func formatSessionHeader(cols int) string {
	return fmt.Sprintf("%-7s %-12s %-20s %7s  %s",
		"PID", "USER", "EVENT", "E/T", "SQL")
}

func formatSessionRow(s SessionRow, innerW int) string {
	usr := truncStr(s.Username, 12)
	event := truncStr(s.Event, 20)
	elapsed := fmt.Sprintf("%5.1fs", s.ElapsedSec)
	elapsed = colorByLevel(elapsed, s.ElapsedSec, etWarn, etCrit)

	// Fixed columns width: 7 + 1 + 12 + 1 + 20 + 1 + 7 + 2 = 51
	fixedW := 51
	sqlW := innerW - fixedW
	if sqlW < 4 {
		sqlW = 4
	}
	sqlText := truncStr(s.SQLText, sqlW)

	return fmt.Sprintf("%-7d %-12s %-20s %7s  %s",
		s.PID, usr, event, elapsed, sqlText)
}

// ── Status Bar (1 line) ──

func renderStatusBar(snap Snapshot, cols int, intervalSec int) string {
	ts := snap.Timestamp.Format("15:04:05")

	var left string
	if snap.Health == Critical {
		left = " " + ansiRed + "CRITICAL" + ansiReset + " | /health for details"
	} else {
		left = fmt.Sprintf(" q quit | refresh: %ds", intervalSec)
	}

	leftW := visibleWidth(left)
	rightW := visibleWidth(ts)
	gap := cols - leftW - rightW
	if gap < 1 {
		gap = 1
	}

	return left + strings.Repeat(" ", gap) + ts
}

// ── Box Drawing Helpers ──

func boxTop(title string, cols int) string {
	prefix := "\u256d\u2500 " + title + " "
	prefixW := visibleWidth(prefix)
	fillW := cols - prefixW - 1
	if fillW < 1 {
		fillW = 1
	}
	return prefix + strings.Repeat("\u2500", fillW) + "\u256e"
}

func boxBottom(cols int) string {
	fillW := cols - 2
	if fillW < 1 {
		fillW = 1
	}
	return "\u2570" + strings.Repeat("\u2500", fillW) + "\u256f"
}

func boxLine(content string, cols int) string {
	contentW := visibleWidth(content)
	innerW := cols - 4
	if innerW < 1 {
		innerW = 1
	}
	padW := innerW - contentW
	if padW < 0 {
		content = truncStr(stripANSI(content), innerW)
		contentW = runewidth.StringWidth(content)
		padW = innerW - contentW
	}
	return "\u2502 " + content + strings.Repeat(" ", padW) + " \u2502"
}

func boxEmpty(cols int) string {
	innerW := cols - 4
	if innerW < 1 {
		innerW = 1
	}
	return "\u2502 " + strings.Repeat(" ", innerW) + " \u2502"
}

// ── ANSI Color Helpers ──

const (
	ansiReset  = "\033[0m"
	ansiOrange = "\033[38;5;208m"
	ansiRed    = "\033[38;5;196m"
	ansiGreen  = "\033[38;5;40m"
)

func colorWarn(s string) string { return ansiOrange + s + ansiReset }
func colorCrit(s string) string { return ansiRed + s + ansiReset }

func colorByLevel(s string, val, warn, crit float64) string {
	switch {
	case val > crit:
		return colorCrit(s)
	case val > warn:
		return colorWarn(s)
	default:
		return s
	}
}

func colorHealth(h HealthLevel) string {
	switch h {
	case Critical:
		return ansiRed + "CRITICAL" + ansiReset
	case Warning:
		return ansiOrange + "WARNING" + ansiReset
	default:
		return ansiGreen + "HEALTHY" + ansiReset
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '\033' && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func visibleWidth(s string) int {
	return runewidth.StringWidth(stripANSI(s))
}

// ── Formatting Helpers ──

func barChart(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(math.Round(pct / 100.0 * float64(width)))
	if filled > width {
		filled = width
	}
	return strings.Repeat("\u2587", filled) + strings.Repeat("\u2591", width-filled)
}

func formatSizeMB(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", mb/1024)
	}
	return fmt.Sprintf("%.0fM", mb)
}

func formatRate(v float64) string {
	if v >= 10000 {
		return fmt.Sprintf("%.1fK", v/1000)
	}
	return formatComma(int64(v))
}

func formatWALRate(kbs float64) string {
	if kbs >= 1048576 {
		return fmt.Sprintf("%.1fG/s", kbs/1048576)
	}
	if kbs >= 1024 {
		return fmt.Sprintf("%.1fM/s", kbs/1024)
	}
	return fmt.Sprintf("%.0fK/s", kbs)
}

func formatComma(n int64) string {
	if n < 0 {
		return "-" + formatComma(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func truncStr(s string, maxW int) string {
	if runewidth.StringWidth(s) <= maxW {
		return s
	}
	if maxW <= 2 {
		var b strings.Builder
		w := 0
		for _, r := range s {
			rw := runewidth.RuneWidth(r)
			if w+rw > maxW {
				break
			}
			b.WriteRune(r)
			w += rw
		}
		return b.String()
	}
	target := maxW - 2
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > target {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	b.WriteString("..")
	return b.String()
}
