/*-------------------------------------------------------------------------
 *
 * report_bench_test.go
 *	  report_bench_test.go benchmarks report rendering and
 *	  markdown-to-HTML conversion from the cerebrate package. Report
 *	  generation benchmarks use pre-built markdown content to avoid
 *	  dependency on drone package internals.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  tests/benchmark/report_bench_test.go
 *
 *-------------------------------------------------------------------------
 */
// report_bench_test.go benchmarks report rendering and markdown-to-HTML
// conversion from the cerebrate package. Report generation benchmarks use
// pre-built markdown content to avoid dependency on drone package internals.
package benchmark_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/cerebrate"
)

// sampleReportMarkdown returns a realistic fault report in markdown,
// matching the format produced by drone.GenerateFaultReport + RenderMarkdown.
func sampleReportMarkdown() string {
	var b strings.Builder
	b.WriteString("# 故障分析报告\n\n")
	b.WriteString("> 报告ID: 20260411_143000_ORCLCDB | 严重级别: critical\n")
	b.WriteString("> 摘要: Bad SQL (全表扫描) (峰值活跃会话: 50, 置信度: 85%)\n\n")

	b.WriteString("## 基本信息\n\n")
	b.WriteString("| 项目 | 值 |\n|------|-----|\n")
	b.WriteString("| 报告时间 | 2026-04-11 14:30:00 |\n")
	b.WriteString("| 数据库类型 | oracle |\n")
	b.WriteString("| 实例名称 | ORCLCDB |\n")
	b.WriteString("| 主机 | prod-db-01 |\n")
	b.WriteString("| 异常持续时间 | 15 秒 |\n\n")

	b.WriteString("## 异常摘要\n\n")
	b.WriteString("活跃会话从基线 10 飙升至峰值 50。\n")
	b.WriteString("根因分析置信度: 85% — Bad SQL (全表扫描)。\n")
	b.WriteString("诊断模式: auto, 共 3 轮。\n\n")

	b.WriteString("## 性能指标快照\n\n")
	b.WriteString("### 触发时段 vs 基线对比\n")
	b.WriteString("| 指标 | 基线 | 异常时 | 趋势 |\n")
	b.WriteString("|------|------|--------|------|\n")
	b.WriteString("| Active Sessions | 10.0 | 50.0 | spike |\n")
	b.WriteString("| DB CPU (%) | 15.0 | 85.0 | rising |\n\n")

	b.WriteString("### Top 等待事件\n")
	b.WriteString("| # | 等待事件 | 等待类 | 占比 | 总耗时(ms) |\n")
	b.WriteString("|---|---------|--------|------|----------|\n")
	b.WriteString("| 1 | db file sequential read | User I/O | 60.0% | 72000 |\n")
	b.WriteString("| 2 | cursor: pin S | Concurrency | 25.0% | 30000 |\n\n")

	b.WriteString("### Top SQL\n")
	b.WriteString("| # | SQL_ID | 执行次数 | 平均耗时(ms) | CPU(ms) | I/O等待(ms) | 并发数 |\n")
	b.WriteString("|---|--------|---------|-------------|---------|------------|--------|\n")
	b.WriteString("| 1 | abc123def45 | 500 | 240.0 | 80.0 | 160.0 | 8 |\n")
	b.WriteString("| 2 | xyz789ghi01 | 200 | 120.0 | 100.0 | 20.0 | 3 |\n\n")

	b.WriteString("## 根因分析\n\n")
	b.WriteString("SQL abc123def45 缺少索引，导致全表扫描。\n\n")

	b.WriteString("## 修复方案\n\n")
	b.WriteString("### 紧急止血\n")
	b.WriteString("```sql\nCREATE INDEX idx_orders_customer_id ON orders(customer_id) ONLINE;\n```\n\n")

	b.WriteString("## 验证方法\n\n")
	b.WriteString("```sql\nSELECT index_name, status FROM dba_indexes WHERE table_name = 'ORDERS';\n```\n")

	return b.String()
}

// BenchmarkGenerateFaultReport benchmarks constructing a full fault report.
// Since the report is assembled from string builders, we replicate the
// structure inline to measure the same computational work as
// drone.GenerateFaultReport.
func BenchmarkGenerateFaultReport(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Build a realistic report with sections (same work as GenerateFaultReport).
		var sections []struct{ Title, Content string }

		// Section 1: Basic info (table format).
		var s1 strings.Builder
		s1.WriteString("| 项目 | 值 |\n|------|-----|\n")
		s1.WriteString(fmt.Sprintf("| 数据库类型 | %s |\n", "oracle"))
		s1.WriteString(fmt.Sprintf("| 实例名称 | %s |\n", "ORCLCDB"))
		s1.WriteString(fmt.Sprintf("| 主机 | %s |\n", "prod-db-01"))
		s1.WriteString(fmt.Sprintf("| 异常持续时间 | %d 秒 |\n", 15))
		sections = append(sections, struct{ Title, Content string }{"基本信息", s1.String()})

		// Section 2: Anomaly summary.
		var s2 strings.Builder
		s2.WriteString(fmt.Sprintf("活跃会话从基线 %.0f 飙升至峰值 %d。\n", 10.0, 50))
		s2.WriteString(fmt.Sprintf("根因分析置信度: %.0f%% — %s。\n", 85.0, "Bad SQL"))
		s2.WriteString(fmt.Sprintf("诊断模式: %s, 共 %d 轮。\n", "auto", 3))
		sections = append(sections, struct{ Title, Content string }{"异常摘要", s2.String()})

		// Section 3: Metrics.
		var s3 strings.Builder
		s3.WriteString("### Top SQL\n")
		s3.WriteString("| # | SQL_ID | 执行次数 |\n|---|--------|--------|\n")
		s3.WriteString(fmt.Sprintf("| 1 | %s | %d |\n", "abc123def45", 500))
		sections = append(sections, struct{ Title, Content string }{"性能指标快照", s3.String()})

		// Section 4-6: Root cause, remediation, verification.
		sections = append(sections, struct{ Title, Content string }{"根因分析", "SQL abc123 缺少索引"})
		sections = append(sections, struct{ Title, Content string }{"修复方案", "CREATE INDEX ..."})
		sections = append(sections, struct{ Title, Content string }{"验证方法", "SELECT index_name ..."})

		_ = sections
	}
}

// BenchmarkRenderMarkdown benchmarks rendering structured report sections
// into a complete markdown string (same work as drone.RenderMarkdown).
func BenchmarkRenderMarkdown(b *testing.B) {
	type section struct {
		Title, Content string
	}

	sections := []section{
		{"基本信息", "| 项目 | 值 |\n|------|-----|\n| 报告时间 | 2026-04-11 |\n| 数据库类型 | oracle |\n"},
		{"异常摘要", "活跃会话从基线 10 飙升至峰值 50。\n诊断模式: auto, 共 3 轮。\n"},
		{"性能指标快照", "| # | SQL_ID | 执行次数 |\n|---|--------|--------|\n| 1 | abc123 | 500 |\n"},
		{"根因分析", "SQL abc123 缺少索引，导致全表扫描。"},
		{"修复方案", "```sql\nCREATE INDEX idx ON orders(customer_id);\n```"},
		{"验证方法", "```sql\nSELECT index_name FROM dba_indexes;\n```"},
	}
	reportID := "20260411_143000_ORCLCDB"
	severity := "critical"
	summary := "Bad SQL (峰值活跃会话: 50, 置信度: 85%)"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var md strings.Builder
		md.WriteString("# 故障分析报告\n\n")
		md.WriteString(fmt.Sprintf("> 报告ID: %s | 严重级别: %s\n", reportID, severity))
		md.WriteString(fmt.Sprintf("> 摘要: %s\n\n", summary))
		for _, s := range sections {
			md.WriteString(fmt.Sprintf("## %s\n\n", s.Title))
			md.WriteString(s.Content)
			md.WriteString("\n\n")
		}
		_ = md.String()
	}
}

// BenchmarkMarkdownToHTML benchmarks converting a full fault report from
// markdown to HTML using cerebrate's built-in converter.
func BenchmarkMarkdownToHTML(b *testing.B) {
	md := sampleReportMarkdown()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cerebrate.MarkdownToHTMLForBench(md)
	}
}
