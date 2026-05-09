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
 *	  internal/postgres/monitor/dbtop/renderer.go
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
	lines = append(lines, renderEventsBox(snap, cols, intervalSec)...)
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
		return "postgresql"
	}
	parts := strings.SplitN(version, ".", 2)
	return "postgresql " + parts[0]
}

func renderMetricsLine(snap Snapshot) string {
	// SBuf: just show the value (it's a config setting, not runtime usage)
	sBufStr := formatSizeMB(snap.SBufSizeMB)

	// CacheHit with bar and color
	cacheBar := barChart(snap.CacheHitPct, barWidth)
	cacheStr := fmt.Sprintf("%.1f%%", snap.CacheHitPct)
	if snap.CacheHitPct > 0 && snap.CacheHitPct < cacheHitWarn {
		cacheBar = colorWarn(cacheBar)
		cacheStr = colorWarn(cacheStr)
	}

	// db% and WTR% (matching Oracle layout)
	var dbPctStr, wtrPctStr string
	if snap.HasDelta {
		// Scale db%: 10 active sessions = 100% bar fill
		dbBarPct := math.Min(snap.DBTimePct*10, 100)
		dbBar := barChart(dbBarPct, barWidth)
		dbVal := fmt.Sprintf("%4.1f", snap.DBTimePct)
		dbPctStr = fmt.Sprintf("db%% %s %s",
			colorByLevel(dbBar, snap.DBTimePct, dbPctWarn, dbPctCrit),
			colorByLevel(dbVal, snap.DBTimePct, dbPctWarn, dbPctCrit))

		wtrBar := barChart(snap.WaitRatio, barWidth)
		wtrVal := fmt.Sprintf("%4.1f", snap.WaitRatio)
		wtrPctStr = fmt.Sprintf("WTR%% %s %s",
			colorByLevel(wtrBar, snap.WaitRatio, wtrWarn, wtrCrit),
			colorByLevel(wtrVal, snap.WaitRatio, wtrWarn, wtrCrit))
	} else {
		dbPctStr = "db% --"
		wtrPctStr = "WTR% --"
	}

	return fmt.Sprintf("SBuf %s  CacheHit %s %s  %s  %s",
		sBufStr, cacheBar, cacheStr, dbPctStr, wtrPctStr)
}

func renderCountsLine(snap Snapshot) string {
	// Left: session counts matching Oracle layout (Session Active ActiveCPU ActiveIO Idle)
	activeStr := formatComma(int64(snap.ActiveCount))
	activeStr = colorByLevel(activeStr, float64(snap.ActiveCount), anWarn, anCrit)

	left := fmt.Sprintf("Session %s  Active %s  ActiveCPU %s  ActiveIO %s  Idle %s",
		formatComma(int64(snap.TotalSessions)),
		activeStr,
		formatComma(int64(snap.ActiveCPUCount)),
		formatComma(int64(snap.ActiveIOCount)),
		formatComma(int64(snap.IdleCount)))

	// Right: throughput
	var right string
	if snap.HasDelta {
		right = fmt.Sprintf("TPS %s  QPS %s  WAL %s",
			formatRate(snap.TPS),
			formatRate(snap.QPS),
			formatWALRate(snap.WALKBs))
	} else {
		right = "TPS --  QPS --  WAL --"
	}

	return left + "  │  " + right
}

// ── Events Box (8 lines) ──
//
// Dual-column layout matching Oracle: Delta (left) │ Cumulative (right)
// Left:  EVENT(24) + sp(1) + Dwait(7) + sp(1) + Dtime(9) + sp(2) + bar(6) + sp(1) + pct(6) = 57
// Right: Cwait(11) + sp(1) + Ctime(9) + sp(2) + bar(6) + sp(1) + pct(6) = 36
const eventSepCol = 57

func renderEventsBox(snap Snapshot, cols int, intervalSec int) []string {
	lines := make([]string, 0, eventsLines)
	lines = append(lines, boxTop("Top Wait Events", cols))
	lines = append(lines, eventBoxLine(formatEventHeader(), cols))

	eventCount := len(snap.Events)
	if eventCount > maxEvents {
		eventCount = maxEvents
	}
	for i := 0; i < maxEvents; i++ {
		if i < eventCount {
			lines = append(lines, eventBoxLine(formatEventRow(snap.Events[i], intervalSec), cols))
		} else {
			lines = append(lines, eventBoxEmpty(cols))
		}
	}

	lines = append(lines, boxBottom(cols))
	return lines
}

func eventBoxLine(content string, cols int) string {
	innerW := cols - 4
	if innerW < 1 {
		innerW = 1
	}
	parts := strings.SplitN(content, "\x00", 2)
	left := parts[0]
	right := ""
	if len(parts) > 1 {
		right = parts[1]
	}
	leftW := visibleWidth(left)
	if leftW > eventSepCol {
		left = truncStr(stripANSI(left), eventSepCol-1) + " "
		leftW = visibleWidth(left)
	}
	leftPad := eventSepCol - leftW
	if leftPad < 0 {
		leftPad = 0
	}
	rightW := visibleWidth(right)
	usedW := eventSepCol + 3 + rightW
	rightPad := innerW - usedW
	if rightPad < 0 {
		rightPad = 0
	}
	return "\u2502 " + left + strings.Repeat(" ", leftPad) + " \u2502 " + right + strings.Repeat(" ", rightPad) + " \u2502"
}

func eventBoxEmpty(cols int) string {
	innerW := cols - 4
	if innerW < 1 {
		innerW = 1
	}
	rightW := innerW - eventSepCol - 3
	if rightW < 0 {
		rightW = 0
	}
	return "\u2502 " + strings.Repeat(" ", eventSepCol) + " \u2502 " + strings.Repeat(" ", rightW) + " \u2502"
}

func formatEventHeader() string {
	left := fmt.Sprintf("%-24s %7s %9s  %-6s %-6s",
		"EVENT", "Dwait", "Dtime(ms)", "DPCT", "Delta")
	right := fmt.Sprintf("%11s %9s  %-6s %-6s",
		"Cwait", "Ctime(s)", "CPCT", "Cumul")
	return left + "\x00" + right
}

func formatEventRow(ev WaitEvent, intervalSec int) string {
	evName := truncStr(ev.Event, 24)

	// Estimate time from session counts × interval (ASH-style sampling).
	dTimeMs := float64(ev.Sessions) * float64(intervalSec) * 1000
	cTimeSec := float64(ev.CumulSessions) * float64(intervalSec)

	// Delta section (matching Oracle layout)
	dBar := barChart(ev.Percentage, 6)
	dPctStr := fmt.Sprintf("%5.1f%%", ev.Percentage)
	coloredDBar := colorByLevel(dBar, ev.Percentage, evtPctWarn, evtPctCrit)
	coloredDPct := colorByLevel(dPctStr, ev.Percentage, evtPctWarn, evtPctCrit)
	left := fmt.Sprintf("%-24s %7s %9.1f  %s %s",
		evName, formatComma(int64(ev.Sessions)), dTimeMs, coloredDBar, coloredDPct)

	// Cumulative section (matching Oracle layout)
	cBar := barChart(ev.CumulPct, 6)
	cPctStr := fmt.Sprintf("%5.1f%%", ev.CumulPct)
	coloredCBar := colorByLevel(cBar, ev.CumulPct, evtPctWarn, evtPctCrit)
	coloredCPct := colorByLevel(cPctStr, ev.CumulPct, evtPctWarn, evtPctCrit)
	right := fmt.Sprintf("%11s %9.1f  %s %s",
		formatComma(ev.CumulSessions), cTimeSec, coloredCBar, coloredCPct)

	return left + "\x00" + right
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
		hint := fmt.Sprintf("... \u8fd8\u6709 %d \u4e2a\u6d3b\u8dc3\u4f1a\u8bdd\u672a\u663e\u793a", remaining)
		lines = append(lines, boxLine(hint, cols))
	} else {
		lines = append(lines, boxEmpty(cols))
	}

	lines = append(lines, boxBottom(cols))
	return lines
}

func formatSessionHeader(cols int) string {
	// Match Oracle layout: SID/PID, USR, SQLID/QUERYID, EVENT, CLASS/TYPE, E/T, SQL
	return fmt.Sprintf("%-5s %-10s %-13s %-20s %-10s %5s  %s",
		"PID", "USR", "QUERYID", "EVENT", "TYPE", "E/T", "SQL")
}

func formatSessionRow(s SessionRow, innerW int) string {
	usr := truncStr(s.Username, 10)
	qid := truncStr(s.QueryID, 13)
	if qid == "" || qid == "0" {
		qid = "-"
	}
	event := truncStr(s.Event, 20)
	waitType := truncStr(s.WaitType, 10)
	elapsed := fmt.Sprintf("%4.1fs", s.ElapsedSec)
	elapsed = colorByLevel(elapsed, s.ElapsedSec, etWarn, etCrit)

	// Fixed columns width: 5 + 1 + 10 + 1 + 13 + 1 + 20 + 1 + 10 + 1 + 5 + 2 = 70
	fixedW := 70
	sqlW := innerW - fixedW
	if sqlW < 4 {
		sqlW = 4
	}
	sqlText := truncStr(s.SQLText, sqlW)

	return fmt.Sprintf("%-5d %-10s %-13s %-20s %-10s %5s  %s",
		s.PID, usr, qid, event, waitType, elapsed, sqlText)
}

// ── Status Bar (1 line) ──

func renderStatusBar(snap Snapshot, cols int, intervalSec int) string {
	ts := snap.Timestamp.Format("15:04:05")

	var left string
	switch snap.Health {
	case Critical:
		left = " " + ansiRed + "\u25cf CRITICAL" + ansiReset + " \u2502 \u8f93\u5165 /health \u67e5\u770b\u8be6\u7ec6\u8bca\u65ad"
	case Warning:
		left = " " + ansiOrange + "\u25cf WARNING" + ansiReset + " \u2502 \u8f93\u5165 /health \u67e5\u770b\u8be6\u7ec6\u8bca\u65ad"
	default:
		left = fmt.Sprintf(" q \u9000\u51fa \u2502 \u5237\u65b0: %ds", intervalSec)
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
		return ansiRed + "\u25cf CRITICAL" + ansiReset
	case Warning:
		return ansiOrange + "\u25cf WARNING" + ansiReset
	default:
		return ansiGreen + "\u25cf HEALTHY" + ansiReset
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
