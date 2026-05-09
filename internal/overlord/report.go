/*-------------------------------------------------------------------------
 *
 * report.go
 *	  report.go implements Overlord-level cross-node fault report
 *	  generation. The Overlord aggregates Worker fault reports into
 *	  regional summaries, adding cross-node correlation analysis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/report.go
 *
 *-------------------------------------------------------------------------
 */
// report.go implements Overlord-level cross-node fault report generation.
// The Overlord aggregates Worker fault reports into regional summaries,
// adding cross-node correlation analysis.
package overlord

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RegionFaultReport is the aggregated fault report for a region.
type RegionFaultReport struct {
	ID          string         // unique report ID
	Timestamp   time.Time      // report generation time
	Region      string         // region name
	Period      string         // e.g. "2026-04-11 09:00 ~ 10:00"
	WorkerCount int            // total workers in region
	OnlineCount int            // online workers
	FaultCount  int            // number of faults in period
	Faults      []FaultSummary // individual fault entries
	Correlation string         // cross-node correlation analysis
}

// FaultSummary is a condensed view of one Worker fault for region-level reporting.
type FaultSummary struct {
	Timestamp time.Time
	WorkerID  string
	Instance  string
	Severity  string // "critical" / "warning" / "info"
	RootCause string
	Status    string // "方案已生成" / "需人工介入"
	ReportID  string // link to worker-level report
}

// GenerateRegionReport creates a RegionFaultReport from the given inputs.
// All parameters are value-typed; the function returns a new object (immutable pattern).
func GenerateRegionReport(
	region string,
	workerCount, onlineCount int,
	faults []FaultSummary,
	correlation string,
) *RegionFaultReport {
	now := time.Now()
	reportID := fmt.Sprintf("region_%s_%s", region, now.Format("20060102_150405"))
	period := fmt.Sprintf("%s ~ %s",
		now.Add(-1*time.Hour).Format("2006-01-02 15:04"),
		now.Format("2006-01-02 15:04"))

	// Copy faults slice to avoid mutation of caller's data.
	faultsCopy := make([]FaultSummary, len(faults))
	copy(faultsCopy, faults)

	return &RegionFaultReport{
		ID:          reportID,
		Timestamp:   now,
		Region:      region,
		Period:      period,
		WorkerCount: workerCount,
		OnlineCount: onlineCount,
		FaultCount:  len(faultsCopy),
		Faults:      faultsCopy,
		Correlation: correlation,
	}
}

// RenderRegionMarkdown renders a RegionFaultReport to a complete markdown string.
func RenderRegionMarkdown(report *RegionFaultReport) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# 区域故障汇总报告 — %s\n\n", report.Region))
	b.WriteString(fmt.Sprintf("> 报告ID: %s\n\n", report.ID))

	// Report period.
	b.WriteString("## 报告周期\n\n")
	b.WriteString(report.Period + "\n\n")

	// Region overview.
	b.WriteString("## 区域概览\n\n")
	b.WriteString("| 项目 | 值 |\n")
	b.WriteString("|------|-----|\n")
	b.WriteString(fmt.Sprintf("| Worker 总数 | %d |\n", report.WorkerCount))
	b.WriteString(fmt.Sprintf("| 在线 | %d |\n", report.OnlineCount))
	b.WriteString(fmt.Sprintf("| 本期故障数 | %d |\n", report.FaultCount))
	needHuman := countNeedHuman(report.Faults)
	b.WriteString(fmt.Sprintf("| 自主分析完成 | %d |\n", report.FaultCount-needHuman))
	b.WriteString(fmt.Sprintf("| 需人工介入 | %d |\n", needHuman))
	b.WriteString("\n")

	// Fault list.
	b.WriteString("## 故障列表\n\n")
	if len(report.Faults) == 0 {
		b.WriteString("本期无故障。\n\n")
	} else {
		b.WriteString("| # | 时间 | Worker | 数据库 | 严重级别 | 根因 | 状态 |\n")
		b.WriteString("|---|------|--------|--------|---------|------|------|\n")
		for i, f := range report.Faults {
			b.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s | %s | %s |\n",
				i+1, f.Timestamp.Format("15:04"), f.WorkerID,
				f.Instance, f.Severity, f.RootCause, f.Status))
		}
		b.WriteString("\n")
	}

	// Correlation analysis.
	b.WriteString("## 关联分析\n\n")
	if report.Correlation != "" {
		b.WriteString(report.Correlation + "\n\n")
	} else {
		b.WriteString("未发现跨节点关联。\n\n")
	}

	// Report links.
	if len(report.Faults) > 0 {
		b.WriteString("## 详细报告链接\n\n")
		for _, f := range report.Faults {
			if f.ReportID != "" {
				b.WriteString(fmt.Sprintf("- [%s 详细报告](./%s/%s_fault.md)\n",
					f.WorkerID, f.WorkerID, f.ReportID))
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

// SaveRegionReport saves a region report as a markdown file.
// Path: {baseDir}/reports/{region}/{timestamp}_region.md
func SaveRegionReport(baseDir, region string, report *RegionFaultReport) (string, error) {
	dir := filepath.Join(baseDir, "reports", region)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create region report dir %s: %w", dir, err)
	}

	filename := report.Timestamp.Format("20060102_150405") + "_region.md"
	path := filepath.Join(dir, filename)

	content := RenderRegionMarkdown(report)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write region report %s: %w", path, err)
	}

	return path, nil
}

// countNeedHuman counts faults that require human intervention.
func countNeedHuman(faults []FaultSummary) int {
	count := 0
	for _, f := range faults {
		if f.Status == "需人工介入" {
			count++
		}
	}
	return count
}
