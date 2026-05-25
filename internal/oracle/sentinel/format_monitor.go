/*-------------------------------------------------------------------------
 *
 * format_monitor.go
 *	  FormatRuleDiagnosisWidth renders a BurstReport using per-scenario
 *	  blocks. The box right border aligns to termWidth. No 120-column
 *	  cap.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/format_monitor.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"fmt"
	"strings"
)

// ──────────────────────────────────────────────────────────────────
// Per-scenario block configuration
// ──────────────────────────────────────────────────────────────────

// blockConfig defines which data blocks to render for a given trigger metric.
// Based on alert-templates.md: each scenario shows only relevant blocks.
type blockConfig struct {
	waitEvents    bool // B: wait events top 5
	topSQL        bool // C: top SQL
	sqlText       bool // D: SQL text
	blockingChain bool // E: blocking chains
	keyMetrics    bool // F: key metric summaries
	spaceDetail   bool // G: space/capacity details
	paramDetail   bool // H: related DB parameters
}

// scenarioBlocks maps each trigger metric to its rendering config.
var scenarioBlocks = map[MetricName]blockConfig{
	// ── Session/Load ──
	MetricActive:              {waitEvents: true, topSQL: true, sqlText: true, keyMetrics: true},
	MetricCPU:                 {waitEvents: true, topSQL: true, sqlText: true, keyMetrics: true},
	MetricIO:                  {waitEvents: true, topSQL: true, sqlText: true, keyMetrics: true},
	MetricLock:                {blockingChain: true, topSQL: true, sqlText: true, keyMetrics: true},
	MetricLongSQL:             {topSQL: true, sqlText: true, keyMetrics: true},
	MetricTotalSessions:       {keyMetrics: true},
	MetricPQSessions:          {topSQL: true, sqlText: true, keyMetrics: true},
	MetricSessionCreationRate: {keyMetrics: true},
	MetricBackgroundWait:      {waitEvents: true, keyMetrics: true},
	MetricOSLoad:              {keyMetrics: true},

	// ── Throughput ──
	MetricRedoRate:         {topSQL: true, sqlText: true, keyMetrics: true},
	MetricHardParse:        {topSQL: true, sqlText: true, keyMetrics: true},
	MetricPhysicalReadRate: {waitEvents: true, topSQL: true, sqlText: true, keyMetrics: true},
	MetricLogicalReadRate:  {topSQL: true, sqlText: true, keyMetrics: true},
	MetricCommitRate:       {keyMetrics: true},

	// ── Wait/Latency ──
	MetricLogFileSyncUs:    {waitEvents: true, keyMetrics: true, paramDetail: true},
	MetricDbFileSeqReadUs:  {waitEvents: true, topSQL: true, sqlText: true, keyMetrics: true},
	MetricDbFileScatReadUs: {waitEvents: true, topSQL: true, sqlText: true, keyMetrics: true},
	MetricEnqueueWaitMs:    {blockingChain: true, topSQL: true, keyMetrics: true},
	MetricLatchFreeRate:    {waitEvents: true, keyMetrics: true},
	MetricNetworkRoundTrip: {keyMetrics: true},

	// ── Memory/Cache ──
	MetricBufferCacheHit:    {topSQL: true, sqlText: true, keyMetrics: true, paramDetail: true},
	MetricLibraryCacheHit:   {keyMetrics: true, paramDetail: true},
	MetricPGAUsedPct:        {keyMetrics: true, paramDetail: true},
	MetricSharedPoolFreePct: {keyMetrics: true, paramDetail: true},

	// ── Storage ──
	MetricTablespaceUsedPct: {keyMetrics: true, spaceDetail: true},
	MetricTempUsedPct:       {keyMetrics: true, spaceDetail: true},
	MetricUndoUsedPct:       {keyMetrics: true, spaceDetail: true},
	MetricFRAUsedPct:        {keyMetrics: true, spaceDetail: true},
	MetricASMUsedPct:        {keyMetrics: true, spaceDetail: true},

	// ── Redo/Archive ──
	MetricLogSwitchRate:    {topSQL: true, sqlText: true, keyMetrics: true, paramDetail: true},
	MetricArchiveLagSec:    {keyMetrics: true},
	MetricStandbyApplyLag:  {keyMetrics: true},
	MetricRedoLogSpaceWait: {keyMetrics: true, paramDetail: true},

	// ── Lock/Concurrency ──
	MetricBlockingChains:   {blockingChain: true, keyMetrics: true},
	MetricEnqueueDeadlocks: {blockingChain: true, keyMetrics: true},
	MetricMutexWait:        {waitEvents: true, keyMetrics: true},
	MetricRowLockWaitMs:    {blockingChain: true, keyMetrics: true},

	// ── SQL Performance ──
	MetricTopSQLDrift:     {topSQL: true, sqlText: true},
	MetricFullScanRate:    {topSQL: true, sqlText: true},
	MetricPlanChangeCount: {topSQL: true, sqlText: true},
	MetricSQLThrottle:     {topSQL: true, keyMetrics: true, paramDetail: true},

	// ── System/Pattern ──
	MetricInstanceStatus:        {},
	MetricDataGuardStatus:       {},
	MetricAlertLogErrors:        {},
	MetricJobFailureRate:        {},
	MetricResourceLimitPct:      {keyMetrics: true},
	MetricCheckpointNotComplete: {keyMetrics: true, paramDetail: true},
}

// blocksForMetric returns the block config for a trigger metric.
// Falls back to showing all blocks for unknown metrics.
func blocksForMetric(metric MetricName) blockConfig {
	if cfg, ok := scenarioBlocks[metric]; ok {
		return cfg
	}
	return blockConfig{
		waitEvents: true, topSQL: true, sqlText: true,
		blockingChain: true, keyMetrics: true,
	}
}

// ──────────────────────────────────────────────────────────────────
// Box drawing helpers — consistent right border at terminal width
// ──────────────────────────────────────────────────────────────────

// writeBoxHeader writes "┌─ title ───...───┐" spanning exactly width columns.
func writeBoxHeader(b *strings.Builder, title string, width int) {
	titleW := displayWidth(title)
	dashes := width - 3 - titleW
	if dashes < 1 {
		dashes = 1
	}
	b.WriteString("┌─" + title + strings.Repeat("─", dashes) + "┐\n")
}

// writeBoxLine writes "│ content     │" padded so the line is exactly width columns.
// innerW = width - 4 (accounts for "│ " left and " │" right).
func writeBoxLine(b *strings.Builder, content string, innerW int) {
	visLen := displayWidth(content)
	pad := innerW - visLen
	if pad < 0 {
		content = truncByWidth(content, innerW-1) + "…"
		visLen = displayWidth(content)
		pad = innerW - visLen
		if pad < 0 {
			pad = 0
		}
	}
	b.WriteString("│ " + content + strings.Repeat(" ", pad) + " │\n")
}

// writeBoxFooter writes "└───...───┘" spanning exactly width columns.
func writeBoxFooter(b *strings.Builder, width int) {
	b.WriteString("└" + strings.Repeat("─", width-2) + "┘")
}

// ──────────────────────────────────────────────────────────────────
// FormatRuleDiagnosisWidth — per-scenario monitoring data compositor
// ──────────────────────────────────────────────────────────────────

// FormatRuleDiagnosisWidth renders a BurstReport using per-scenario blocks.
// The box right border aligns to termWidth. No 120-column cap.
func FormatRuleDiagnosisWidth(report BurstReport, termWidth int) string {
	if termWidth < 40 {
		termWidth = 80
	}
	innerW := termWidth - 4

	blocks := blocksForMetric(MetricName(report.TriggerEvent.Metric))

	var b strings.Builder
	writeLine := func(content string) {
		writeBoxLine(&b, content, innerW)
	}

	// Header.
	writeBoxHeader(&b, " 监控数据 ", termWidth)

	// Block A: Trigger info + stats (always shown).
	renderBlockA(report, writeLine)

	// Block F: Key metric summaries.
	if blocks.keyMetrics {
		renderBlockF(report, writeLine)
	}

	// Block B: Wait events top 5.
	if blocks.waitEvents {
		renderBlockB(report, writeLine)
	}

	// Block C: Top SQL.
	if blocks.topSQL {
		renderBlockC(report, writeLine)
	}

	// Block E: Blocking chains.
	if blocks.blockingChain {
		renderBlockE(report, writeLine)
	}

	// Block D: SQL text.
	if blocks.sqlText {
		renderBlockD(report, innerW, writeLine)
	}

	// Block G: Space details.
	if blocks.spaceDetail {
		renderBlockG(report, writeLine)
	}

	// Block H: Related parameters.
	if blocks.paramDetail {
		renderBlockH(report, writeLine)
	}

	// Footer.
	writeBoxFooter(&b, termWidth)

	return b.String()
}

// ──────────────────────────────────────────────────────────────────
// Block renderers
// ──────────────────────────────────────────────────────────────────

// renderBlockA writes trigger info and basic stats (always shown).
func renderBlockA(report BurstReport, writeLine func(string)) {
	trigger := report.TriggerEvent
	if trigger.Metric != "" {
		writeLine("触发: " + FormatAlertDescription(trigger, report.DurationSec))
	}

	// Build the stats line. Always show duration.
	stats := fmt.Sprintf("持续: %.1fs", report.DurationSec)

	// Only show active session stats when meaningful (peak > 1 or baseline > 0).
	if report.PeakActive > 1 || report.BaselineActive > 0 {
		stats += fmt.Sprintf(" | 活跃会话: %d (基线 %.0f)", report.PeakActive, report.BaselineActive)
	}
	writeLine(stats)
}

// renderBlockB writes wait events top 5 with bar chart.
func renderBlockB(report BurstReport, writeLine func(string)) {
	if len(report.WaitProfile) == 0 {
		return
	}
	writeLine("")
	writeLine("## 等待事件 Top 5")
	limit := len(report.WaitProfile)
	if limit > 5 {
		limit = 5
	}
	for i := 0; i < limit; i++ {
		wp := report.WaitProfile[i]
		if wp.Percentage < 1 {
			continue
		}
		bar := strings.Repeat("█", int(wp.Percentage/5))
		name := wp.Event
		if name == "" {
			name = wp.WaitClass
		}
		writeLine(fmt.Sprintf("  %-30s %5.1f%% %s",
			truncStr(name, 30), wp.Percentage, bar))
	}
}

// renderBlockC writes top SQL summary.
func renderBlockC(report BurstReport, writeLine func(string)) {
	if len(report.TopSQLs) == 0 {
		return
	}
	tsStart, tsEnd := "", ""
	if !report.StartTime.IsZero() {
		tsStart = report.StartTime.Format("15:04:05")
	}
	if !report.EndTime.IsZero() {
		tsEnd = report.EndTime.Format("15:04:05")
	}
	writeLine("")
	if tsStart != "" && tsEnd != "" {
		writeLine(fmt.Sprintf("## Top SQL (%s ~ %s, %.0fs)", tsStart, tsEnd, report.DurationSec))
	} else {
		writeLine("## Top SQL")
	}
	limit := len(report.TopSQLs)
	if limit > 3 {
		limit = 3
	}
	for i := 0; i < limit; i++ {
		s := report.TopSQLs[i]
		waitInfo := s.Event
		if waitInfo == "" {
			waitInfo = s.WaitClass
		}
		writeLine(fmt.Sprintf("  %d. %s  最长耗时:%.1fs  最大并发:%d  最多等待:%s",
			i+1, s.SQLID, s.MaxElapsedSec, s.MaxConcurrent, waitInfo))
	}
}

// renderBlockD writes SQL text for top SQLs.
func renderBlockD(report BurstReport, innerW int, writeLine func(string)) {
	if len(report.TopSQLs) == 0 {
		return
	}
	writeLine("")
	writeLine("## SQL 文本")
	limit := len(report.TopSQLs)
	if limit > 3 {
		limit = 3
	}
	maxTextLen := innerW - 6
	if maxTextLen < 40 {
		maxTextLen = 40
	}
	for i := 0; i < limit; i++ {
		s := report.TopSQLs[i]
		if s.SQLText == "" || strings.TrimSpace(s.SQLText) == "" {
			continue
		}
		writeLine(fmt.Sprintf("  %s:", s.SQLID))
		writeLine(fmt.Sprintf("    %s", truncStr(strings.TrimSpace(s.SQLText), maxTextLen)))
	}
}

// renderBlockE writes blocking chain details.
func renderBlockE(report BurstReport, writeLine func(string)) {
	if len(report.BlockingChains) == 0 {
		return
	}
	writeLine("")
	writeLine("## 阻塞链")
	for _, chain := range report.BlockingChains {
		writeLine(fmt.Sprintf("  SID %d(根阻塞者) → %d 个等待会话 (SQL: %s)",
			chain.RootSID, chain.VictimCount, chain.RootSQLID))
	}
}

// renderBlockG writes space/capacity details.
func renderBlockG(report BurstReport, writeLine func(string)) {
	if len(report.SpaceDetails) == 0 {
		return
	}
	writeLine("")

	metric := MetricName(report.TriggerEvent.Metric)

	// Title varies by scenario.
	switch metric {
	case MetricTablespaceUsedPct:
		writeLine("## 表空间明细")
	case MetricTempUsedPct:
		writeLine("## 临时空间明细")
	case MetricUndoUsedPct:
		writeLine("## Undo 空间明细")
	case MetricFRAUsedPct:
		writeLine("## FRA 使用明细")
	case MetricASMUsedPct:
		writeLine("## ASM 磁盘组明细")
	default:
		writeLine("## 空间明细")
	}

	for _, d := range report.SpaceDetails {
		if metric == MetricFRAUsedPct {
			// FRA shows file type + percent.
			writeLine(fmt.Sprintf("  %-30s %s", truncStr(d.Name, 30), d.Extra))
		} else if d.TotalMB > 0 {
			usedGB := d.UsedMB / 1024
			totalGB := d.TotalMB / 1024
			if totalGB >= 1 {
				writeLine(fmt.Sprintf("  %-30s %.1fGB / %.1fGB  %.1f%%",
					truncStr(d.Name, 30), usedGB, totalGB, d.UsedPct))
			} else {
				writeLine(fmt.Sprintf("  %-30s %.0fMB / %.0fMB  %.1f%%",
					truncStr(d.Name, 30), d.UsedMB, d.TotalMB, d.UsedPct))
			}
		} else {
			writeLine(fmt.Sprintf("  %-30s %.1f%%", truncStr(d.Name, 30), d.UsedPct))
		}
	}
}

// renderBlockH writes related database parameters.
func renderBlockH(report BurstReport, writeLine func(string)) {
	if len(report.ParamDetails) == 0 {
		return
	}
	writeLine("")
	writeLine("## 相关参数")
	for _, p := range report.ParamDetails {
		writeLine(fmt.Sprintf("  %-30s %s", p.Name, p.Value))
	}
}

// burstMetricDisplay defines display info for burst-collected metrics.
// Ordered by importance for rendering in Block F.
var burstMetricDisplay = []struct {
	Key   string // map key in report.Metrics
	Label string
	Unit  string
}{
	{string(MetricActive), "活跃会话", "个"},
	{string(MetricCPU), "On CPU", "个"},
	{string(MetricIO), "I/O Wait", "个"},
	{string(MetricCommitRate), "TPS", "/s"},
	{"qps", "QPS", "/s"},
	{string(MetricRedoRate), "Redo Rate", "KB/s"},
	{string(MetricTotalSessions), "总会话", "个"},
	{"db%", "DB Time %", "%"},
	{"wtr%", "Wait Time %", "%"},
}

// renderBlockF writes key metric summaries from burst data.
func renderBlockF(report BurstReport, writeLine func(string)) {
	if len(report.Metrics) == 0 {
		return
	}

	triggerKey := report.TriggerEvent.Metric

	// First pass: count how many lines we'll actually render.
	count := 0
	for _, m := range burstMetricDisplay {
		if m.Key == triggerKey {
			continue
		}
		if _, ok := report.Metrics[m.Key]; ok {
			count++
		}
	}
	// Also count MetricDef-based metrics not in burstMetricDisplay.
	burstKeys := make(map[string]bool, len(burstMetricDisplay))
	for _, m := range burstMetricDisplay {
		burstKeys[m.Key] = true
	}
	for _, name := range metricPriorityOrder {
		if burstKeys[string(name)] || string(name) == triggerKey {
			continue
		}
		if _, ok := report.Metrics[string(name)]; ok {
			count++
		}
	}

	if count == 0 {
		return
	}

	writeLine("")
	writeLine("## 关键指标")

	// Render burst metrics with custom display.
	for _, m := range burstMetricDisplay {
		if m.Key == triggerKey {
			continue
		}
		ms, ok := report.Metrics[m.Key]
		if !ok {
			continue
		}
		icon := trendToIcon(ms.Trend)
		if m.Unit == "%" {
			writeLine(fmt.Sprintf("  %-28s avg:%.1f%%  max:%.1f%%  %s",
				truncStr(m.Label, 28), ms.Avg, ms.Max, icon))
		} else if m.Unit != "" {
			writeLine(fmt.Sprintf("  %-28s avg:%.1f  max:%.1f%s  %s",
				truncStr(m.Label, 28), ms.Avg, ms.Max, m.Unit, icon))
		} else {
			writeLine(fmt.Sprintf("  %-28s avg:%.1f  max:%.1f  %s",
				truncStr(m.Label, 28), ms.Avg, ms.Max, icon))
		}
	}

	// Render any additional MetricDef-based metrics not already shown.
	for _, name := range metricPriorityOrder {
		if burstKeys[string(name)] || string(name) == triggerKey {
			continue
		}
		ms, ok := report.Metrics[string(name)]
		if !ok {
			continue
		}
		def := GetMetricDef(name)
		if def == nil {
			continue
		}
		icon := trendToIcon(ms.Trend)
		if def.Unit == "%" {
			writeLine(fmt.Sprintf("  %-28s avg:%.1f%%  max:%.1f%%  %s",
				truncStr(def.Label, 28), ms.Avg, ms.Max, icon))
		} else if def.Unit != "" {
			writeLine(fmt.Sprintf("  %-28s avg:%.1f  max:%.1f%s  %s",
				truncStr(def.Label, 28), ms.Avg, ms.Max, def.Unit, icon))
		} else {
			writeLine(fmt.Sprintf("  %-28s avg:%.1f  max:%.1f  %s",
				truncStr(def.Label, 28), ms.Avg, ms.Max, icon))
		}
	}
}

// trendToIcon converts a trend string to a visual indicator.
func trendToIcon(trend string) string {
	switch trend {
	case "rising":
		return "↑"
	case "falling":
		return "↓"
	case "spike":
		return "⬆"
	case "drop":
		return "⬇"
	default:
		return "─"
	}
}
