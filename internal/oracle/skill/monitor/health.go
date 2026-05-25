/*-------------------------------------------------------------------------
 *
 * health.go
 *	  HealthCheckItem is the structured representation of a single
 *	  health check result.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/health.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/odberr"
	"github.com/sqlrush/opendb/internal/skill"
)

// HealthCheckItem is the structured representation of a single health check result.
type HealthCheckItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "OK", "WARN", "FAIL"
	Message string `json:"message"`
}

// HealthReport is the structured output of the health skill for LLM consumption.
type HealthReport struct {
	InstanceName string            `json:"instance_name"`
	Version      string            `json:"version"`
	Checks       []HealthCheckItem `json:"checks"`
	OKCount      int               `json:"ok_count"`
	WarnCount    int               `json:"warn_count"`
	FailCount    int               `json:"fail_count"`
}

// checkStatus represents the severity level of a health check result.
type checkStatus uint8

const (
	statusOK   checkStatus = iota
	statusWarn
	statusFail
)

// statusLabel returns the display label for a check status with ANSI colors.
func (s checkStatus) statusLabel() string {
	switch s {
	case statusOK:
		return "\033[32mOK\033[0m"
	case statusWarn:
		return "\033[33mWARN\033[0m"
	case statusFail:
		return "\033[1;31mFAIL\033[0m"
	default:
		return "????"
	}
}

// statusString returns a plain status string for structured data and table rendering.
func (s checkStatus) statusString() string {
	switch s {
	case statusOK:
		return "OK"
	case statusWarn:
		return "WARN"
	case statusFail:
		return "FAIL"
	default:
		return "UNKNOWN"
	}
}

// checkResult holds the outcome of a single health check.
type checkResult struct {
	name    string
	status  checkStatus
	message string
}

// HealthSkill aggregates multiple health checks into a comprehensive report.
type HealthSkill struct {
	driver db.Driver
}

// NewHealthSkill creates a HealthSkill backed by the given driver.
func NewHealthSkill(driver db.Driver) *HealthSkill {
	return &HealthSkill{driver: driver}
}

func (h *HealthSkill) Name() string                        { return "health" }
func (h *HealthSkill) Description() string                 { return "Comprehensive database health check" }
func (h *HealthSkill) SecurityLevel() skill.SecurityLevel   { return skill.LevelReadOnly }

func (h *HealthSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "health",
		Description: "Comprehensive database health check",
	}
}

func (h *HealthSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "health",
		Usage:   "/health",
	}
}

func (h *HealthSkill) Validate(_ skill.Params) error { return nil }

func (h *HealthSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	info := h.driver.ServerInfo()

	// Run alert log check async with a short timeout — it can be slow
	// and should not block the entire health report.
	alertCh := make(chan checkResult, 1)
	odberr.SafeGo(odberr.ErrSkillExec, func() {
		alertCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		alertCh <- h.checkAlertLog(alertCtx)
	})

	checks := []checkResult{
		// Group 1: 基础
		h.checkInstance(ctx, info),
		h.checkDatabaseRole(ctx),
		h.checkArchiveMode(ctx),
		// Group 2: 空间
		h.checkTablespace(ctx),
		h.checkTempSpace(ctx),
		h.checkUndoSpace(ctx),
		h.checkFRA(ctx),
		h.checkASM(ctx),
		// Group 3: 会话/性能
		h.checkConnections(ctx),
		h.checkActiveSessions(ctx),
		h.checkWaitEvents(ctx),
		h.checkSlowSQL(ctx),
		h.checkBufferHit(ctx),
		h.checkLibraryHit(ctx),
		// Group 4: 内存
		h.checkPGA(ctx),
		h.checkSGA(ctx),
		h.checkSharedPoolFree(ctx),
		// Group 5: 日志
		h.checkRedoSwitch(ctx),
	}

	// Collect alert result — use whatever finished, or skip if timed out.
	select {
	case r := <-alertCh:
		checks = append(checks, r)
	default:
		checks = append(checks, checkResult{name: "告警日志", status: statusOK, message: "查询超时，已跳过"})
	}

	// Group 6: 维护/安全
	checks = append(checks,
		h.checkBackup(ctx),
		h.checkStaleStats(ctx),
		h.checkInvalidObjects(ctx),
		h.checkResourceLimit(ctx),
		h.checkPasswordExpiry(ctx),
	)

	rendered := formatReport(info, checks)

	items := make([]HealthCheckItem, len(checks))
	var okCount, warnCount, failCount int
	for i, c := range checks {
		items[i] = HealthCheckItem{
			Name:    c.name,
			Status:  c.status.statusString(),
			Message: c.message,
		}
		switch c.status {
		case statusOK:
			okCount++
		case statusWarn:
			warnCount++
		case statusFail:
			failCount++
		}
	}

	hr := HealthReport{
		InstanceName: info.InstanceName,
		Version:      info.Version,
		Checks:       items,
		OKCount:      okCount,
		WarnCount:    warnCount,
		FailCount:    failCount,
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &hr,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d OK, %d WARN, %d FAIL", okCount, warnCount, failCount),
	}, nil
}

// suggestionMap maps check names to relevant /commands.
var suggestionMap = map[string]string{
	"等待事件":      "/waits",
	"慢SQL":      "/slowsql",
	"表空间":       "/space",
	"临时表空间":     "/space",
	"Undo表空间":   "/space",
	"PGA使用率":    "/pga",
	"SGA概览":     "/sga",
	"Shared Pool": "/sga",
	"Redo切换":    "/redo",
	"备份":        "/backup",
	"告警日志":      "/alert",
}

// Panel layout constants for 2-column check display.
const (
	healthNameW = 13 // display width for check name column
	healthColW  = 34 // display width per column (name + gap + msg + gap + icon)
	healthGap   = 3  // gap between two columns
)

// formatReport builds the health report as a grouped panel with sections.
func formatReport(info db.ServerInfo, checks []checkResult) string {
	var okCount, warnCount, failCount int
	for _, c := range checks {
		switch c.status {
		case statusOK:
			okCount++
		case statusWarn:
			warnCount++
		case statusFail:
			failCount++
		}
	}

	// Overall status.
	var overallLabel, overallIcon, overallDetail string
	switch {
	case failCount > 0:
		overallIcon = format.StatusIcon("FAIL")
		overallLabel = format.StatusLabel("FAIL")
		overallDetail = fmt.Sprintf("(%d issues found)", warnCount+failCount)
	case warnCount > 0:
		overallIcon = format.StatusIcon("WARN")
		overallLabel = format.StatusLabel("WARN")
		overallDetail = fmt.Sprintf("(%d issues found)", warnCount)
	default:
		overallIcon = format.StatusIcon("OK")
		overallLabel = format.StatusLabel("OK")
		overallDetail = fmt.Sprintf("(%d checks passed)", okCount)
	}

	// Group definitions by check index range.
	groups := []struct {
		name  string
		start int
		end   int
	}{
		{"基础", 0, 3},
		{"空间", 3, 8},
		{"会话/性能", 8, 14},
		{"内存", 14, 17},
		{"日志", 17, 19},
		{"维护/安全", 19, 24},
	}

	sections := []format.PanelSection{
		{Lines: []string{
			fmt.Sprintf(" Overall: %s %s  %s", overallIcon, overallLabel, overallDetail),
		}},
	}

	for _, g := range groups {
		end := g.end
		if end > len(checks) {
			end = len(checks)
		}
		if g.start >= end {
			continue
		}
		lines := formatCheckPairs(checks[g.start:end])
		sections = append(sections, format.PanelSection{
			Header: g.name,
			Lines:  lines,
		})
	}

	// Alerts section for non-OK checks.
	var alerts []string
	for _, c := range checks {
		if c.status == statusOK {
			continue
		}
		icon := format.StatusIcon(c.status.statusString())
		alerts = append(alerts, fmt.Sprintf(" %s %s: %s", icon, c.name, c.message))
	}
	if len(alerts) > 0 {
		suggestions := buildSuggestions(checks)
		if len(suggestions) > 0 {
			maxTipW := healthColW*2 + healthGap + 1 // match 2-column content width
			alerts = append(alerts, wrapTipLines(suggestions, maxTipW)...)
		}
		sections = append(sections, format.PanelSection{
			Header: "Alerts",
			Lines:  alerts,
		})
	}

	title := fmt.Sprintf("Database Health — %s", info.InstanceName)
	return format.Panel(title, sections)
}

// formatCheckPairs renders check items in 2-column paired layout.
func formatCheckPairs(checks []checkResult) []string {
	msgW := healthColW - healthNameW - 3
	var lines []string
	for i := 0; i < len(checks); i += 2 {
		left := formatCheckCol(checks[i], msgW)
		if i+1 < len(checks) {
			right := formatCheckCol(checks[i+1], msgW)
			lines = append(lines, " "+format.PadRight(left, healthColW)+strings.Repeat(" ", healthGap)+right)
		} else {
			lines = append(lines, " "+left)
		}
	}
	return lines
}

// formatCheckCol renders a single check item: name(padded) + message + icon.
func formatCheckCol(c checkResult, msgW int) string {
	name := format.PadRight(c.name, healthNameW)
	msg := format.TruncDisplayWidth(c.message, msgW)
	icon := statusIcon(c.status)
	return name + " " + format.PadRight(msg, msgW) + " " + icon
}

// statusIcon returns a colored single-character status icon.
func statusIcon(s checkStatus) string {
	switch s {
	case statusOK:
		return "\033[32m✓\033[0m"
	case statusWarn:
		return "\033[33m⚠\033[0m"
	case statusFail:
		return "\033[1;31m✗\033[0m"
	default:
		return " "
	}
}

// buildSuggestions returns de-duplicated command suggestions for non-OK checks.
func buildSuggestions(checks []checkResult) []string {
	seen := make(map[string]bool)
	var suggestions []string

	for _, c := range checks {
		if c.status == statusOK {
			continue
		}
		cmd, ok := suggestionMap[c.name]
		if !ok {
			continue
		}
		if seen[cmd] {
			continue
		}
		seen[cmd] = true
		suggestions = append(suggestions, cmd+" 查看"+c.name)
	}
	return suggestions
}

// wrapTipLines breaks suggestion tips into multiple lines to fit within maxW display width.
func wrapTipLines(suggestions []string, maxW int) []string {
	prefix := " Tip: "
	contPrefix := "      " // align continuation with first tip after "Tip: "
	prefixW := format.DisplayWidth(prefix)
	contPrefixW := format.DisplayWidth(contPrefix)

	var lines []string
	cur := prefix
	curW := prefixW

	for i, s := range suggestions {
		seg := s
		if i > 0 {
			seg = "; " + s
		}
		segW := format.DisplayWidth(seg)

		if curW+segW > maxW && cur != prefix {
			lines = append(lines, cur)
			cur = contPrefix + s
			curW = contPrefixW + format.DisplayWidth(s)
		} else {
			cur += seg
			curW += segW
		}
	}
	if cur != prefix && cur != contPrefix {
		lines = append(lines, cur)
	}
	return lines
}

// toFloat64 converts a value to float64, handling common numeric types.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}
