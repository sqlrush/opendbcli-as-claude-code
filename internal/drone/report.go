/*-------------------------------------------------------------------------
 *
 * report.go
 *	  report.go implements Worker-level fault analysis report
 *	  generation. Reports are the core product deliverable: OpenDB never
 *	  executes changes, only generates expert-level reports with
 *	  actionable SQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/report.go
 *
 *-------------------------------------------------------------------------
 */
// report.go implements Worker-level fault analysis report generation.
// Reports are the core product deliverable: OpenDB never executes changes,
// only generates expert-level reports with actionable SQL.
package drone

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FaultReport is the structured representation of a Worker fault analysis report.
type FaultReport struct {
	ID        string          // unique report ID (timestamp-based)
	Timestamp time.Time       // report generation time
	Instance  string          // database instance name
	DBType    string          // oracle/mysql/postgres
	Host      string          // hostname or IP
	Severity  string          // critical/warning/info
	Summary   string          // one-line summary
	Sections  []ReportSection // ordered report sections
}

// ReportSection is one section of a fault report.
type ReportSection struct {
	Title   string // section heading
	Content string // markdown content
}

// DiagnosisInput holds the LLM/RuleEngine diagnosis output needed for reports.
// Callers map their product-specific DiagnoseResult to this generic type.
type DiagnosisInput struct {
	Analysis   string // full LLM analysis text (markdown)
	RoundsUsed int    // number of LLM round-trips
	Mode       string // diagnosis mode (auto/assist/playbook)
}

// MetricsInput holds burst metrics data needed for reports.
// Callers map their product-specific BurstReport to this generic type.
type MetricsInput struct {
	DurationSec    float64
	PeakActive     int
	BaselineActive float64
	TopSQLs        []SQLSummary
	WaitProfile    []WaitSummary
	Metrics        map[string]MetricPair
	Classification string  // root cause classification
	Confidence     float64 // confidence 0.0~1.0
	Evidence       []string
	StartTime      time.Time
	EndTime        time.Time
}

// SQLSummary is a simplified top-SQL entry for report rendering.
type SQLSummary struct {
	SQLID         string
	Executions    int64
	AvgElapsedMs  float64
	CPUMs         float64
	IOWaitMs      float64
	MaxConcurrent int
}

// WaitSummary is a simplified wait event entry for report rendering.
type WaitSummary struct {
	Event      string
	WaitClass  string
	TotalMs    float64
	Percentage float64 // 0.0~100.0
}

// MetricPair holds baseline vs anomaly values for a single metric.
type MetricPair struct {
	Baseline float64
	Current  float64
	Trend    string // "rising"/"falling"/"stable"/"spike"
}

// GenerateFaultReport creates a structured report from diagnosis and metrics data.
// The metrics parameter may be nil for on-demand diagnoses without Sentinel trigger.
func GenerateFaultReport(
	diagnosis DiagnosisInput,
	metrics *MetricsInput,
	instance, dbType, host string,
) *FaultReport {
	now := time.Now()
	reportID := fmt.Sprintf("%s_%s", now.Format("20060102_150405"), instance)
	severity := classifyReportSeverity(diagnosis, metrics)

	sections := buildSections(diagnosis, metrics, instance, dbType, host, now)

	summary := buildSummary(diagnosis, metrics)

	return &FaultReport{
		ID:        reportID,
		Timestamp: now,
		Instance:  instance,
		DBType:    dbType,
		Host:      host,
		Severity:  severity,
		Summary:   summary,
		Sections:  sections,
	}
}

// buildSections constructs all report sections in order.
func buildSections(
	diag DiagnosisInput,
	metrics *MetricsInput,
	instance, dbType, host string,
	ts time.Time,
) []ReportSection {
	sections := make([]ReportSection, 0, 6)

	// 1. Basic info.
	sections = append(sections, buildBasicInfoSection(instance, dbType, host, ts, metrics))

	// 2. Anomaly summary.
	sections = append(sections, buildAnomalySummarySection(diag, metrics))

	// 3. Metrics snapshot (only if metrics available).
	if metrics != nil {
		sections = append(sections, buildMetricsSection(metrics))
	}

	// 4. Root cause analysis.
	sections = append(sections, buildRootCauseSection(diag))

	// 5. Remediation plan.
	sections = append(sections, buildRemediationSection(diag))

	// 6. Verification methods.
	sections = append(sections, buildVerificationSection(diag))

	return sections
}

// buildBasicInfoSection creates the basic information table.
func buildBasicInfoSection(
	instance, dbType, host string,
	ts time.Time,
	metrics *MetricsInput,
) ReportSection {
	var b strings.Builder
	b.WriteString("| 项目 | 值 |\n")
	b.WriteString("|------|-----|\n")
	b.WriteString(fmt.Sprintf("| 报告时间 | %s |\n", ts.Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("| 数据库类型 | %s |\n", dbType))
	b.WriteString(fmt.Sprintf("| 实例名称 | %s |\n", instance))
	b.WriteString(fmt.Sprintf("| 主机 | %s |\n", host))
	if metrics != nil {
		b.WriteString(fmt.Sprintf("| 异常持续时间 | %.0f 秒 |\n", metrics.DurationSec))
	}
	return ReportSection{Title: "基本信息", Content: b.String()}
}

// buildAnomalySummarySection creates the anomaly summary.
func buildAnomalySummarySection(diag DiagnosisInput, metrics *MetricsInput) ReportSection {
	var b strings.Builder
	if metrics != nil {
		b.WriteString(fmt.Sprintf("活跃会话从基线 %.0f 飙升至峰值 %d。\n",
			metrics.BaselineActive, metrics.PeakActive))
		if metrics.Classification != "" {
			b.WriteString(fmt.Sprintf("根因分析置信度: %.0f%% — %s。\n",
				metrics.Confidence*100, metrics.Classification))
		}
	}
	b.WriteString(fmt.Sprintf("诊断模式: %s, 共 %d 轮。\n", diag.Mode, diag.RoundsUsed))
	return ReportSection{Title: "异常摘要", Content: b.String()}
}

// buildMetricsSection creates the performance metrics snapshot.
func buildMetricsSection(metrics *MetricsInput) ReportSection {
	var b strings.Builder

	// Metrics comparison table.
	if len(metrics.Metrics) > 0 {
		b.WriteString("### 触发时段 vs 基线对比\n")
		b.WriteString("| 指标 | 基线 | 异常时 | 趋势 |\n")
		b.WriteString("|------|------|--------|------|\n")
		for name, mp := range metrics.Metrics {
			b.WriteString(fmt.Sprintf("| %s | %.1f | %.1f | %s |\n",
				name, mp.Baseline, mp.Current, mp.Trend))
		}
		b.WriteString("\n")
	}

	// Wait profile.
	if len(metrics.WaitProfile) > 0 {
		b.WriteString("### Top 等待事件\n")
		b.WriteString("| # | 等待事件 | 等待类 | 占比 | 总耗时(ms) |\n")
		b.WriteString("|---|---------|--------|------|----------|\n")
		for i, w := range metrics.WaitProfile {
			b.WriteString(fmt.Sprintf("| %d | %s | %s | %.1f%% | %.0f |\n",
				i+1, w.Event, w.WaitClass, w.Percentage, w.TotalMs))
		}
		b.WriteString("\n")
	}

	// Top SQL.
	if len(metrics.TopSQLs) > 0 {
		b.WriteString("### Top SQL\n")
		b.WriteString("| # | SQL_ID | 执行次数 | 平均耗时(ms) | CPU(ms) | I/O等待(ms) | 并发数 |\n")
		b.WriteString("|---|--------|---------|-------------|---------|------------|--------|\n")
		for i, s := range metrics.TopSQLs {
			b.WriteString(fmt.Sprintf("| %d | %s | %d | %.1f | %.1f | %.1f | %d |\n",
				i+1, s.SQLID, s.Executions, s.AvgElapsedMs,
				s.CPUMs, s.IOWaitMs, s.MaxConcurrent))
		}
	}

	return ReportSection{Title: "性能指标快照", Content: b.String()}
}

// buildRootCauseSection extracts root cause analysis from the LLM diagnosis.
func buildRootCauseSection(diag DiagnosisInput) ReportSection {
	content := extractSection(diag.Analysis, "根因分析", "## ")
	if content == "" {
		content = "LLM 诊断未返回根因分析内容。"
	}
	return ReportSection{Title: "根因分析", Content: content}
}

// buildRemediationSection extracts the remediation plan from the LLM diagnosis.
func buildRemediationSection(diag DiagnosisInput) ReportSection {
	content := extractSection(diag.Analysis, "修复方案", "## ")
	if content == "" {
		content = extractSection(diag.Analysis, "紧急措施", "## ")
	}
	if content == "" {
		content = "LLM 诊断未返回修复方案。请人工分析后制定修复计划。"
	}
	return ReportSection{Title: "修复方案", Content: content}
}

// buildVerificationSection extracts verification methods from the LLM diagnosis.
func buildVerificationSection(diag DiagnosisInput) ReportSection {
	content := extractSection(diag.Analysis, "验证方法", "## ")
	if content == "" {
		content = extractSection(diag.Analysis, "根因修复", "## ")
	}
	if content == "" {
		content = "请在执行修复后验证数据库性能指标恢复正常。"
	}
	return ReportSection{Title: "验证方法", Content: content}
}

// extractSection finds a section by title in markdown text and returns its content.
func extractSection(text, sectionTitle, headerPrefix string) string {
	marker := headerPrefix + sectionTitle
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	// Find the next section header or end of text.
	rest := text[start:]
	nextIdx := strings.Index(rest, "\n"+headerPrefix)
	if nextIdx >= 0 {
		return strings.TrimSpace(rest[:nextIdx])
	}
	return strings.TrimSpace(rest)
}

// classifyReportSeverity determines severity from diagnosis and metrics.
func classifyReportSeverity(diag DiagnosisInput, metrics *MetricsInput) string {
	if metrics != nil && metrics.PeakActive > 0 && metrics.BaselineActive > 0 {
		ratio := float64(metrics.PeakActive) / metrics.BaselineActive
		if ratio >= 5 {
			return "critical"
		}
		if ratio >= 2 {
			return "warning"
		}
	}
	// Check analysis text for severity indicators.
	lower := strings.ToLower(diag.Analysis)
	if strings.Contains(lower, "critical") || strings.Contains(lower, "紧急") {
		return "critical"
	}
	if strings.Contains(lower, "warning") || strings.Contains(lower, "告警") {
		return "warning"
	}
	return "info"
}

// buildSummary creates a one-line summary for the report.
func buildSummary(diag DiagnosisInput, metrics *MetricsInput) string {
	if metrics != nil && metrics.Classification != "" {
		return fmt.Sprintf("%s (峰值活跃会话: %d, 置信度: %.0f%%)",
			metrics.Classification, metrics.PeakActive, metrics.Confidence*100)
	}
	// Fallback: first line of analysis.
	if diag.Analysis != "" {
		firstLine := strings.SplitN(diag.Analysis, "\n", 2)[0]
		runes := []rune(firstLine)
		if len(runes) > 80 {
			return string(runes[:80]) + "..."
		}
		return firstLine
	}
	return "故障分析报告"
}

// RenderMarkdown renders a FaultReport to a complete markdown string.
func RenderMarkdown(report *FaultReport) string {
	var b strings.Builder
	b.WriteString("# 故障分析报告\n\n")
	b.WriteString(fmt.Sprintf("> 报告ID: %s | 严重级别: %s\n", report.ID, report.Severity))
	b.WriteString(fmt.Sprintf("> 摘要: %s\n\n", report.Summary))

	for _, s := range report.Sections {
		b.WriteString(fmt.Sprintf("## %s\n\n", s.Title))
		b.WriteString(s.Content)
		b.WriteString("\n\n")
	}

	return b.String()
}

// SaveReport saves a fault report as a markdown file.
// Path: {baseDir}/reports/{instance}/{timestamp}_fault.md
func SaveReport(baseDir string, report *FaultReport) (string, error) {
	dir := filepath.Join(baseDir, "reports", report.Instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create report dir %s: %w", dir, err)
	}

	filename := report.Timestamp.Format("20060102_150405") + "_fault.md"
	path := filepath.Join(dir, filename)

	content := RenderMarkdown(report)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write report %s: %w", path, err)
	}

	return path, nil
}

// LoadReport reads a fault report from a markdown file and parses it back.
func LoadReport(path string) (*FaultReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report %s: %w", path, err)
	}
	return parseMarkdownReport(string(data), path)
}

// parseMarkdownReport parses a markdown report back into a FaultReport struct.
func parseMarkdownReport(content, path string) (*FaultReport, error) {
	report := &FaultReport{
		Timestamp: time.Now(),
	}

	// Parse header metadata from blockquote lines.
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "> 报告ID:") {
			parts := strings.Split(trimmed, "|")
			if len(parts) >= 1 {
				report.ID = strings.TrimSpace(strings.TrimPrefix(parts[0], "> 报告ID:"))
			}
			if len(parts) >= 2 {
				report.Severity = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(parts[1]), "严重级别:"))
			}
		}
		if strings.HasPrefix(trimmed, "> 摘要:") {
			report.Summary = strings.TrimSpace(strings.TrimPrefix(trimmed, "> 摘要:"))
		}
	}

	// Parse sections by splitting on "## " headers.
	report.Sections = splitMarkdownSections(content)

	// Extract instance from path if possible.
	dir := filepath.Dir(path)
	report.Instance = filepath.Base(dir)

	return report, nil
}

// splitMarkdownSections splits markdown by H2 headers into ReportSection slices.
func splitMarkdownSections(content string) []ReportSection {
	var sections []ReportSection
	lines := strings.Split(content, "\n")
	var currentTitle string
	var currentContent strings.Builder

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			// Save previous section.
			if currentTitle != "" {
				sections = append(sections, ReportSection{
					Title:   currentTitle,
					Content: strings.TrimSpace(currentContent.String()),
				})
			}
			currentTitle = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			currentContent.Reset()
		} else if currentTitle != "" {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}
	// Save last section.
	if currentTitle != "" {
		sections = append(sections, ReportSection{
			Title:   currentTitle,
			Content: strings.TrimSpace(currentContent.String()),
		})
	}
	return sections
}
