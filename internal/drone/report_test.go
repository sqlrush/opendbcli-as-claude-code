/*-------------------------------------------------------------------------
 *
 * report_test.go
 *	  Test cases for report.go (drone package):
 *	  TestGenerateFaultReport_AllSections,
 *	  TestGenerateFaultReport_NilMetrics, TestSaveReport_CreatesFile.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/report_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleDiagnosis returns a DiagnosisInput with realistic LLM output.
func sampleDiagnosis() DiagnosisInput {
	return DiagnosisInput{
		Analysis: `## 根因分析

### 诊断过程
1. 查询 v$session 发现 50 个活跃会话中 40 个在等待 db file sequential read
2. 查询 v$sql 发现 SQL_ID abc123def45 占用 85% 的活跃会话

### 根因结论
SQL abc123def45 缺少索引，导致全表扫描。

## 修复方案

### 紧急止血
` + "```sql\n" + `CREATE INDEX idx_orders_customer_id ON orders(customer_id) ONLINE;
` + "```\n" + `
## 验证方法

` + "```sql\n" + `SELECT index_name, status FROM dba_indexes WHERE table_name = 'ORDERS';
` + "```",
		RoundsUsed: 3,
		Mode:       "auto",
	}
}

// sampleMetrics returns a MetricsInput with representative burst data.
func sampleMetrics() *MetricsInput {
	return &MetricsInput{
		DurationSec:    15,
		PeakActive:     50,
		BaselineActive: 10,
		TopSQLs: []SQLSummary{
			{SQLID: "abc123def45", Executions: 500, AvgElapsedMs: 240, CPUMs: 80, IOWaitMs: 160, MaxConcurrent: 8},
			{SQLID: "xyz789ghi01", Executions: 200, AvgElapsedMs: 120, CPUMs: 100, IOWaitMs: 20, MaxConcurrent: 3},
		},
		WaitProfile: []WaitSummary{
			{Event: "db file sequential read", WaitClass: "User I/O", TotalMs: 72000, Percentage: 60},
			{Event: "cursor: pin S", WaitClass: "Concurrency", TotalMs: 30000, Percentage: 25},
		},
		Metrics: map[string]MetricPair{
			"Active Sessions": {Baseline: 10, Current: 50, Trend: "spike"},
			"DB CPU (%)":      {Baseline: 15, Current: 85, Trend: "rising"},
		},
		Classification: "Bad SQL (全表扫描)",
		Confidence:     0.85,
		Evidence:       []string{"SQL abc123def45 full table scan on ORDERS"},
	}
}

func TestGenerateFaultReport_AllSections(t *testing.T) {
	diag := sampleDiagnosis()
	metrics := sampleMetrics()

	report := GenerateFaultReport(diag, metrics, "ORCLCDB", "oracle", "prod-db-01")

	if report.ID == "" {
		t.Error("report ID should not be empty")
	}
	if report.Instance != "ORCLCDB" {
		t.Errorf("instance = %q, want ORCLCDB", report.Instance)
	}
	if report.DBType != "oracle" {
		t.Errorf("dbType = %q, want oracle", report.DBType)
	}
	if report.Severity != "critical" {
		t.Errorf("severity = %q, want critical (peak 50 / baseline 10 = 5x)", report.Severity)
	}
	if report.Summary == "" {
		t.Error("summary should not be empty")
	}

	// Verify all required section titles.
	requiredTitles := []string{"基本信息", "异常摘要", "性能指标快照", "根因分析", "修复方案", "验证方法"}
	titles := make(map[string]bool)
	for _, s := range report.Sections {
		titles[s.Title] = true
	}
	for _, title := range requiredTitles {
		if !titles[title] {
			t.Errorf("missing required section: %s", title)
		}
	}
}

func TestGenerateFaultReport_NilMetrics(t *testing.T) {
	diag := sampleDiagnosis()

	report := GenerateFaultReport(diag, nil, "testdb", "mysql", "db-host-01")

	if report.Instance != "testdb" {
		t.Errorf("instance = %q, want testdb", report.Instance)
	}

	// Without metrics, should NOT have 性能指标快照 section.
	for _, s := range report.Sections {
		if s.Title == "性能指标快照" {
			t.Error("should not have metrics section when metrics is nil")
		}
	}

	// Should still have the other 5 sections.
	if len(report.Sections) != 5 {
		t.Errorf("section count = %d, want 5 (no metrics)", len(report.Sections))
	}
}

func TestSaveReport_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	diag := sampleDiagnosis()
	metrics := sampleMetrics()
	report := GenerateFaultReport(diag, metrics, "ORCLCDB", "oracle", "prod-db-01")

	path, err := SaveReport(tmpDir, report)
	if err != nil {
		t.Fatalf("SaveReport: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("report file not created")
	}

	// Verify path structure: {base}/reports/ORCLCDB/{timestamp}_fault.md
	if !strings.Contains(path, filepath.Join("reports", "ORCLCDB")) {
		t.Errorf("path %q should contain reports/ORCLCDB", path)
	}
	if !strings.HasSuffix(path, "_fault.md") {
		t.Errorf("path %q should end with _fault.md", path)
	}

	// Verify content.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# 故障分析报告") {
		t.Error("content should contain report title")
	}
}

func TestRenderMarkdown_ContainsAllHeaders(t *testing.T) {
	diag := sampleDiagnosis()
	metrics := sampleMetrics()
	report := GenerateFaultReport(diag, metrics, "ORCLCDB", "oracle", "prod-db-01")

	md := RenderMarkdown(report)

	expectedHeaders := []string{
		"# 故障分析报告",
		"## 基本信息",
		"## 异常摘要",
		"## 性能指标快照",
		"## 根因分析",
		"## 修复方案",
		"## 验证方法",
	}
	for _, h := range expectedHeaders {
		if !strings.Contains(md, h) {
			t.Errorf("markdown missing header: %s", h)
		}
	}

	// Check metadata line.
	if !strings.Contains(md, "> 报告ID:") {
		t.Error("markdown missing report ID metadata")
	}
	if !strings.Contains(md, "> 摘要:") {
		t.Error("markdown missing summary metadata")
	}
}

func TestRenderMarkdown_MetricsContent(t *testing.T) {
	diag := sampleDiagnosis()
	metrics := sampleMetrics()
	report := GenerateFaultReport(diag, metrics, "ORCLCDB", "oracle", "prod-db-01")

	md := RenderMarkdown(report)

	// Verify metrics data appears.
	if !strings.Contains(md, "abc123def45") {
		t.Error("markdown should contain SQL_ID from top SQL")
	}
	if !strings.Contains(md, "db file sequential read") {
		t.Error("markdown should contain wait event")
	}
	if !strings.Contains(md, "Active Sessions") {
		t.Error("markdown should contain metric name")
	}
}

func TestLoadReport_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	diag := sampleDiagnosis()
	metrics := sampleMetrics()
	original := GenerateFaultReport(diag, metrics, "ORCLCDB", "oracle", "prod-db-01")

	path, err := SaveReport(tmpDir, original)
	if err != nil {
		t.Fatalf("SaveReport: %v", err)
	}

	loaded, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}

	// Verify key fields round-trip.
	if loaded.ID != original.ID {
		t.Errorf("loaded ID = %q, want %q", loaded.ID, original.ID)
	}
	if loaded.Severity != original.Severity {
		t.Errorf("loaded Severity = %q, want %q", loaded.Severity, original.Severity)
	}
	if loaded.Summary != original.Summary {
		t.Errorf("loaded Summary = %q, want %q", loaded.Summary, original.Summary)
	}
	if loaded.Instance != original.Instance {
		t.Errorf("loaded Instance = %q, want %q", loaded.Instance, original.Instance)
	}

	// Verify same number of sections.
	if len(loaded.Sections) != len(original.Sections) {
		t.Errorf("loaded sections = %d, want %d", len(loaded.Sections), len(original.Sections))
	}

	// Verify section titles match.
	for i, s := range original.Sections {
		if i < len(loaded.Sections) && loaded.Sections[i].Title != s.Title {
			t.Errorf("section[%d] title = %q, want %q", i, loaded.Sections[i].Title, s.Title)
		}
	}
}

func TestLoadReport_NonexistentFile(t *testing.T) {
	_, err := LoadReport("/nonexistent/path/report.md")
	if err == nil {
		t.Error("LoadReport should fail for nonexistent file")
	}
}

func TestClassifyReportSeverity(t *testing.T) {
	tests := []struct {
		name     string
		diag     DiagnosisInput
		metrics  *MetricsInput
		expected string
	}{
		{
			name:     "critical_by_ratio",
			diag:     DiagnosisInput{},
			metrics:  &MetricsInput{PeakActive: 60, BaselineActive: 10},
			expected: "critical",
		},
		{
			name:     "warning_by_ratio",
			diag:     DiagnosisInput{},
			metrics:  &MetricsInput{PeakActive: 30, BaselineActive: 10},
			expected: "warning",
		},
		{
			name:     "critical_by_text",
			diag:     DiagnosisInput{Analysis: "This is a critical issue"},
			metrics:  nil,
			expected: "critical",
		},
		{
			name:     "info_default",
			diag:     DiagnosisInput{Analysis: "Normal observation"},
			metrics:  nil,
			expected: "info",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyReportSeverity(tt.diag, tt.metrics)
			if got != tt.expected {
				t.Errorf("severity = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestExtractSection(t *testing.T) {
	text := "## 根因分析\n这是根因内容\n## 修复方案\n这是修复内容"

	rootCause := extractSection(text, "根因分析", "## ")
	if rootCause != "这是根因内容" {
		t.Errorf("root cause = %q, want '这是根因内容'", rootCause)
	}

	remediation := extractSection(text, "修复方案", "## ")
	if remediation != "这是修复内容" {
		t.Errorf("remediation = %q, want '这是修复内容'", remediation)
	}

	missing := extractSection(text, "不存在的段落", "## ")
	if missing != "" {
		t.Errorf("missing section = %q, want empty", missing)
	}
}

func TestBuildSummary(t *testing.T) {
	// With classification.
	metrics := &MetricsInput{Classification: "Bad SQL", PeakActive: 50, Confidence: 0.85}
	summary := buildSummary(DiagnosisInput{}, metrics)
	if !strings.Contains(summary, "Bad SQL") {
		t.Errorf("summary = %q, should contain 'Bad SQL'", summary)
	}

	// Without classification, from analysis.
	diag := DiagnosisInput{Analysis: "First line of analysis\nSecond line"}
	summary = buildSummary(diag, nil)
	if summary != "First line of analysis" {
		t.Errorf("summary = %q, want 'First line of analysis'", summary)
	}

	// Empty diagnosis.
	summary = buildSummary(DiagnosisInput{}, nil)
	if summary != "故障分析报告" {
		t.Errorf("summary = %q, want '故障分析报告'", summary)
	}
}
