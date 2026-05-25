/*-------------------------------------------------------------------------
 *
 * renderer.go
 *	  Render produces exactly 28 lines of the MySQL dbtop dashboard.
 *	  cols is the terminal width; intervalSec is the refresh interval
 *	  for display.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/monitor/dbtop/renderer.go
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
	headerLines    = 5  // top + empty + BP line + counts line + bottom
	eventsLines    = 8  // top + header + 5 data + bottom
	sessionsLines  = 14 // top + header + 10 data + trunc + bottom
	statusLines    = 1
	maxEvents      = 5
	maxSessionRows = 10
	barWidth       = 8
)

// Render produces exactly 28 lines of the MySQL dbtop dashboard.
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
		return "mysql"
	}
	// Take first part before '-' or first 3 version segments
	parts := strings.SplitN(version, "-", 2)
	return "mysql " + parts[0]
}

func renderMetricsLine(snap Snapshot) string {
	bpPct := float64(0)
	if snap.BPMaxMB > 0 {
		bpPct = snap.BPUsedMB / snap.BPMaxMB * 100
	}
	bpBar := barChart(bpPct, barWidth)
	bpUsed := formatSize(snap.BPUsedMB)
	bpMax := formatSize(snap.BPMaxMB)

	var dbPctStr, wtrPctStr string
	if snap.HasDelta {
		dbBar := barChart(snap.DBPercent, barWidth)
		dbVal := fmt.Sprintf("%4.1f", snap.DBPercent)
		dbPctStr = fmt.Sprintf("db%% %s %s",
			colorByLevel(dbBar, snap.DBPercent, dbPctWarn, dbPctCrit),
			colorByLevel(dbVal, snap.DBPercent, dbPctWarn, dbPctCrit))

		wtrBar := barChart(snap.WTRPercent, barWidth)
		wtrVal := fmt.Sprintf("%4.1f", snap.WTRPercent)
		wtrPctStr = fmt.Sprintf("WTR%% %s %s",
			colorByLevel(wtrBar, snap.WTRPercent, wtrWarn, wtrCrit),
			colorByLevel(wtrVal, snap.WTRPercent, wtrWarn, wtrCrit))
	} else {
		dbPctStr = "db% --"
		wtrPctStr = "WTR% --"
	}

	return fmt.Sprintf("BP %s %s/%s  %s  %s",
		bpBar, bpUsed, bpMax, dbPctStr, wtrPctStr)
}

func renderCountsLine(snap Snapshot) string {
	anStr := formatComma(int64(snap.ActiveCount))
	anStr = colorByLevel(anStr, float64(snap.ActiveCount), float64(anWarn), float64(anCrit))

	sessLeft := fmt.Sprintf("Session %s  Active %s  ActiveCPU %s  ActiveIO %s  Idle %s",
		formatComma(int64(snap.TotalSessions)),
		anStr,
		formatComma(int64(snap.ActiveCPU)),
		formatComma(int64(snap.ActiveIO)),
		formatComma(int64(snap.IdleCount)))

	var throughput string
	if snap.HasDelta {
		throughput = fmt.Sprintf("TPS %s  QPS %s  Redo %s",
			formatRate(snap.TPS), formatRate(snap.QPS), formatRedoRate(snap.RedoKBs))
	} else {
		throughput = "TPS --  QPS --  Redo --"
	}

	return sessLeft + "  \u2502  " + throughput
}

// ── Events Box (8 lines) ──

const eventSepCol = 57

func renderEventsBox(snap Snapshot, cols int) []string {
	lines := make([]string, 0, eventsLines)
	lines = append(lines, boxTop("Top Wait Events", cols))

	lines = append(lines, eventBoxLine(formatEventHeader(), cols))

	eventCount := len(snap.Events)
	if eventCount > maxEvents {
		eventCount = maxEvents
	}
	for i := 0; i < maxEvents; i++ {
		if i < eventCount {
			lines = append(lines, eventBoxLine(formatEventRow(snap.Events[i]), cols))
		} else {
			lines = append(lines, eventBoxEmpty(cols))
		}
	}

	lines = append(lines, boxBottom(cols))
	return lines
}

// eventBoxLine renders a line with a fixed separator at eventSepCol.
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

	// Truncate left side if it exceeds separator column to keep │ aligned.
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

	return "│ " + left + strings.Repeat(" ", leftPad) + " │ " + right + strings.Repeat(" ", rightPad) + " │"
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
	return "│ " + strings.Repeat(" ", eventSepCol) + " │ " + strings.Repeat(" ", rightW) + " │"
}

func formatEventHeader() string {
	left := fmt.Sprintf("%-24s %7s %9s  %-6s %-6s",
		"EVENT", "Dcnt", "Dtime(ms)", "DPCT", "Delta")
	right := fmt.Sprintf("%11s %9s  %-6s %-6s",
		"Ccnt", "Ctime(s)", "CPCT", "Cumul")
	return left + "\x00" + right
}

func formatEventRow(ev WaitEvent) string {
	evName := truncStr(ev.Event, 24)

	dBar := barChart(ev.DPCT, 6)
	dPctStr := fmt.Sprintf("%5.1f%%", ev.DPCT)
	coloredDBar := colorByLevel(dBar, ev.DPCT, 30.0, 50.0)
	coloredDPct := colorByLevel(dPctStr, ev.DPCT, 30.0, 50.0)
	left := fmt.Sprintf("%-24s %7s %9.1f  %s %s",
		evName,
		formatComma(ev.DCount),
		ev.DTimeMs,
		coloredDBar,
		coloredDPct)

	cBar := barChart(ev.PCT, 6)
	cPctStr := fmt.Sprintf("%5.1f%%", ev.PCT)
	coloredCBar := colorByLevel(cBar, ev.PCT, 30.0, 50.0)
	coloredCPct := colorByLevel(cPctStr, ev.PCT, 30.0, 50.0)
	right := fmt.Sprintf("%11s %9.1f  %s %s",
		formatComma(ev.Count),
		ev.TimeSec,
		coloredCBar,
		coloredCPct)

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
		hint := fmt.Sprintf("... %d more active sessions not shown", remaining)
		lines = append(lines, boxLine(hint, cols))
	} else {
		lines = append(lines, boxEmpty(cols))
	}

	lines = append(lines, boxBottom(cols))
	return lines
}

func formatSessionHeader(cols int) string {
	return fmt.Sprintf("%-5s %-10s %-13s %-20s %-10s %5s  %s",
		"SID", "USR", "SQLID", "EVENT", "CLASS", "E/T", "SQL")
}

func formatSessionRow(s SessionRow, innerW int) string {
	usr := truncStr(s.Username, 10)
	sqlID := truncStr(s.SQLID, 13)
	if sqlID == "" {
		sqlID = "-"
	}
	event := truncStr(s.Event, 20)
	wclass := truncStr(s.WaitClass, 10)
	elapsed := fmt.Sprintf("%4.1fs", s.ElapsedSec)
	elapsed = colorByLevel(elapsed, s.ElapsedSec, etWarn, etCrit)

	fixedW := 70 // 5+1+10+1+13+1+20+1+10+1+5+2 = 70
	sqlW := innerW - fixedW
	if sqlW < 4 {
		sqlW = 4
	}
	sqlText := truncStr(s.SQLText, sqlW)

	return fmt.Sprintf("%-5d %-10s %-13s %-20s %-10s %5s  %s",
		s.SID, usr, sqlID, event, wclass, elapsed, sqlText)
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
	prefix := "╭─ " + title + " "
	prefixW := visibleWidth(prefix)
	fillW := cols - prefixW - 1
	if fillW < 1 {
		fillW = 1
	}
	return prefix + strings.Repeat("─", fillW) + "╮"
}

func boxBottom(cols int) string {
	fillW := cols - 2
	if fillW < 1 {
		fillW = 1
	}
	return "╰" + strings.Repeat("─", fillW) + "╯"
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
	return "│ " + content + strings.Repeat(" ", padW) + " │"
}

func boxEmpty(cols int) string {
	innerW := cols - 4
	if innerW < 1 {
		innerW = 1
	}
	return "│ " + strings.Repeat(" ", innerW) + " │"
}

// ── ANSI Color Helpers ──

const (
	ansiReset  = "\033[0m"
	ansiOrange = "\033[38;5;208m"
	ansiRed    = "\033[38;5;196m"
	ansiGreen  = "\033[38;5;40m"
)

func colorByLevel(s string, val, warn, crit float64) string {
	switch {
	case val > crit:
		return ansiRed + s + ansiReset
	case val > warn:
		return ansiOrange + s + ansiReset
	default:
		return s
	}
}

func colorHealth(h HealthLevel) string {
	switch h {
	case Critical:
		return ansiRed + "● CRITICAL" + ansiReset
	case Warning:
		return ansiOrange + "● WARNING" + ansiReset
	default:
		return ansiGreen + "● HEALTHY" + ansiReset
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
	return strings.Repeat("▇", filled) + strings.Repeat("░", width-filled)
}

func formatSize(mb float64) string {
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

func formatRedoRate(kbs float64) string {
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
