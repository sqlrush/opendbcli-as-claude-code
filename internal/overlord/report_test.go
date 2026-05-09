/*-------------------------------------------------------------------------
 *
 * report_test.go
 *	  Test cases for report.go (overlord package):
 *	  TestGenerateRegionReport_MultipleFaults,
 *	  TestGenerateRegionReport_EmptyFaults,
 *	  TestRenderRegionMarkdown_AllSections.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/report_test.go
 *
 *-------------------------------------------------------------------------
 */
package overlord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleFaults() []FaultSummary {
	now := time.Now()
	return []FaultSummary{
		{
			Timestamp: now.Add(-45 * time.Minute),
			WorkerID:  "worker-oracle-01",
			Instance:  "ORCLCDB",
			Severity:  "critical",
			RootCause: "Bad SQL (全表扫描)",
			Status:    "方案已生成",
			ReportID:  "20260411_091500",
		},
		{
			Timestamp: now.Add(-28 * time.Minute),
			WorkerID:  "worker-mysql-03",
			Instance:  "proddb",
			Severity:  "warning",
			RootCause: "连接池满",
			Status:    "方案已生成",
			ReportID:  "20260411_093200",
		},
		{
			Timestamp: now.Add(-15 * time.Minute),
			WorkerID:  "worker-pg-02",
			Instance:  "analytics",
			Severity:  "critical",
			RootCause: "锁等待超时",
			Status:    "需人工介入",
			ReportID:  "20260411_094500",
		},
	}
}

func TestGenerateRegionReport_MultipleFaults(t *testing.T) {
	faults := sampleFaults()
	correlation := "故障 #1 和 #3 可能相关：同一时段 I/O 压力升高"

	report := GenerateRegionReport("china-east", 50, 48, faults, correlation)

	if report.ID == "" {
		t.Error("report ID should not be empty")
	}
	if report.Region != "china-east" {
		t.Errorf("region = %q, want china-east", report.Region)
	}
	if report.WorkerCount != 50 {
		t.Errorf("WorkerCount = %d, want 50", report.WorkerCount)
	}
	if report.OnlineCount != 48 {
		t.Errorf("OnlineCount = %d, want 48", report.OnlineCount)
	}
	if report.FaultCount != 3 {
		t.Errorf("FaultCount = %d, want 3", report.FaultCount)
	}
	if report.Correlation != correlation {
		t.Errorf("Correlation = %q, want %q", report.Correlation, correlation)
	}
	if report.Period == "" {
		t.Error("Period should not be empty")
	}

	// Verify faults are a copy (immutability).
	faults[0].WorkerID = "mutated"
	if report.Faults[0].WorkerID == "mutated" {
		t.Error("faults should be copied, not referenced")
	}
}

func TestGenerateRegionReport_EmptyFaults(t *testing.T) {
	report := GenerateRegionReport("china-north", 10, 10, nil, "")

	if report.FaultCount != 0 {
		t.Errorf("FaultCount = %d, want 0", report.FaultCount)
	}
	if len(report.Faults) != 0 {
		t.Errorf("Faults length = %d, want 0", len(report.Faults))
	}
}

func TestRenderRegionMarkdown_AllSections(t *testing.T) {
	faults := sampleFaults()
	report := GenerateRegionReport("china-east", 50, 48, faults,
		"故障 #1 和 #3 可能相关：同一时段 I/O 压力升高")

	md := RenderRegionMarkdown(report)

	expectedHeaders := []string{
		"# 区域故障汇总报告",
		"## 报告周期",
		"## 区域概览",
		"## 故障列表",
		"## 关联分析",
		"## 详细报告链接",
	}
	for _, h := range expectedHeaders {
		if !strings.Contains(md, h) {
			t.Errorf("markdown missing header: %s", h)
		}
	}

	// Verify data in tables.
	if !strings.Contains(md, "worker-oracle-01") {
		t.Error("markdown should contain worker ID")
	}
	if !strings.Contains(md, "ORCLCDB") {
		t.Error("markdown should contain instance name")
	}
	if !strings.Contains(md, "需人工介入") {
		t.Error("markdown should contain human intervention status")
	}
	if !strings.Contains(md, "故障 #1 和 #3 可能相关") {
		t.Error("markdown should contain correlation analysis")
	}
}

func TestRenderRegionMarkdown_NoFaults(t *testing.T) {
	report := GenerateRegionReport("china-north", 10, 10, nil, "")

	md := RenderRegionMarkdown(report)

	if !strings.Contains(md, "本期无故障") {
		t.Error("empty fault list should show '本期无故障'")
	}
	if !strings.Contains(md, "未发现跨节点关联") {
		t.Error("empty correlation should show default message")
	}
}

func TestSaveRegionReport_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	faults := sampleFaults()
	report := GenerateRegionReport("china-east", 50, 48, faults, "correlation text")

	path, err := SaveRegionReport(tmpDir, "china-east", report)
	if err != nil {
		t.Fatalf("SaveRegionReport: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("region report file not created")
	}

	// Verify path structure.
	if !strings.Contains(path, filepath.Join("reports", "china-east")) {
		t.Errorf("path %q should contain reports/china-east", path)
	}
	if !strings.HasSuffix(path, "_region.md") {
		t.Errorf("path %q should end with _region.md", path)
	}

	// Verify content is valid markdown.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "# 区域故障汇总报告") {
		t.Error("content should contain report title")
	}
}

func TestCountNeedHuman(t *testing.T) {
	faults := sampleFaults()
	count := countNeedHuman(faults)
	if count != 1 {
		t.Errorf("countNeedHuman = %d, want 1", count)
	}

	count = countNeedHuman(nil)
	if count != 0 {
		t.Errorf("countNeedHuman(nil) = %d, want 0", count)
	}
}
