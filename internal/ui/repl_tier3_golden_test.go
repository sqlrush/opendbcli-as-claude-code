/*-------------------------------------------------------------------------
 *
 * repl_tier3_golden_test.go
 *    Tier 3 UI golden contracts for async diagnosis rendering.
 *
 * These tests intentionally avoid live DB/LLM dependencies. Live PTY coverage
 * lives in internal/ui/uitest/tier3_golden_test.go.
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestTier3DiagProgressRoundsAreCommittedOnce(t *testing.T) {
	r := newTestREPL(18, 100)
	r.contentRow = 1

	r.renderDiagProgress(DiagProgressEvent{Phase: DiagPhaseStart, Message: "AI 分析 (auto, 最多20轮)..."})
	r.renderDiagProgress(DiagProgressEvent{Phase: DiagPhaseAIRound, Message: "第1轮: 调用 health, activesessions, waits, topsql, slowsql, blocktree"})
	r.renderDiagProgress(DiagProgressEvent{Phase: DiagPhaseAIRound, Message: "第2轮: 基于已采集证据生成诊断报告"})
	r.renderDiagProgress(DiagProgressEvent{Phase: DiagPhaseDone, Result: &skill.Result{Type: skill.ResultText, Rendered: "## 总结\n当前数据库无在线故障。"}})

	plain := stripAnsi(strings.Join(r.outputBuffer, "\n"))
	for _, want := range []string{
		"第1轮: 调用 health, activesessions, waits, topsql, slowsql, blocktree",
		"第2轮: 基于已采集证据生成诊断报告",
	} {
		if got := strings.Count(plain, want); got != 1 {
			t.Fatalf("progress line %q count=%d, want 1\n%s", want, got, plain)
		}
	}
	if strings.Index(plain, "第1轮") > strings.Index(plain, "第2轮") {
		t.Fatalf("progress rounds rendered out of order:\n%s", plain)
	}
}

func TestTier3LongDiagOutputDoesNotRepeatTail(t *testing.T) {
	r := newTestREPL(16, 96)
	r.contentRow = 1

	rendered := strings.Join([]string{
		"## 根因分析总表",
		"| 维度 | 数据 | 来源 |",
		"|---|---|---|",
		"| 整体健康 | Overall OK (19 checks passed) | health |",
		"| 活跃会话 | 4 total, 0 waiting | activesessions / waits |",
		"| 阻塞链 | 当前无阻塞链 | blocktree |",
		"",
		"## 当前在线问题",
		"结论：当前无在线故障。健康检查全项通过，活跃会话无业务等待，无阻塞链。",
		"",
		"## 历史 Top/Slow SQL 明细",
		"1. SELECT pg_sleep(?) 为历史故障注入测试。",
		"2. fault_lock / fault_cpu / fault_io / fault_wal 为历史压测脚本统计残留。",
		"3. 历史统计会污染 topsql/slowsql，不能当成当前在线故障。",
		"",
		"## 建议动作",
		"```sql",
		"SELECT jobid, what, last_start_date, next_run_date, broken FROM pg_job WHERE what ILIKE '%fault%' OR what ILIKE '%pg_sleep%';",
		"SELECT pg_stat_reset();",
		"SELECT reset_unique_sql('GLOBAL', 'ALL', 0);",
		"TRUNCATE TABLE fault_lock, fault_cpu, fault_io, fault_wal;",
		"```",
		"",
		"## 总结",
		"当前数据库无在线故障；需处理历史故障注入影响，避免性能基线失真。",
	}, "\n")

	r.renderDiagProgress(DiagProgressEvent{Phase: DiagPhaseDone, Result: &skill.Result{Type: skill.ResultText, Rendered: rendered}})

	plainLines := nonEmptyPlainLines(r.outputBuffer)
	plain := strings.Join(plainLines, "\n")
	for _, anchor := range []string{
		"当前数据库无在线故障；需处理历史故障注入影响，避免性能基线失真。",
		"TRUNCATE TABLE fault_lock, fault_cpu, fault_io, fault_wal;",
	} {
		if got := strings.Count(plain, anchor); got != 1 {
			t.Fatalf("tail anchor %q count=%d, want 1\n%s", anchor, got, plain)
		}
	}
	if hasRepeatedTailBlock(plainLines, 5) {
		t.Fatalf("last 5-line tail block repeated; output tail:\n%s", strings.Join(lastN(plainLines, 12), "\n"))
	}
}

func TestTier3MarkdownTableAndPlanRefsRenderCleanly(t *testing.T) {
	r := newTestREPL(24, 132)
	r.contentRow = 1

	rendered := strings.Join([]string{
		"## 根因分析",
		"| 维度 | 数据 | 来源 |",
		"|---|---|---|",
		"| 整体健康 | Overall OK (19 项检查通过) | health |",
		"| 活跃会话 | 4 个全为 CPU/On CPU，0 个等待 | activesessions / waits |",
		"| 阻塞链 | 当前无阻塞链 | blocktree |",
		"",
		"```plan",
		"Limit cost=243184 rows=1",
		"  - [P1] Seq Scan on bench_orders o cost=59318 rows=1",
		"  - [P2] Seq Scan on bench_shipments s cost=33653 rows=1800000",
		"```",
	}, "\n")

	r.renderDiagProgress(DiagProgressEvent{Phase: DiagPhaseDone, Result: &skill.Result{Type: skill.ResultText, Rendered: rendered}})

	plain := stripAnsi(strings.Join(r.outputBuffer, "\n"))
	for _, want := range []string{"┌", "┬", "整体健康", "[P1]", "[P2]"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered output missing %q\n%s", want, plain)
		}
	}
	for i, line := range r.outputBuffer {
		if w := visibleWidth(stripAnsi(line)); w > r.cols {
			t.Fatalf("line %d overflows: width=%d cols=%d line=%q", i, w, r.cols, stripAnsi(line))
		}
	}
}

func nonEmptyPlainLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(stripAnsi(line))
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func hasRepeatedTailBlock(lines []string, n int) bool {
	if n <= 0 || len(lines) < n*2 {
		return false
	}
	for i := 0; i < n; i++ {
		if lines[len(lines)-1-i] != lines[len(lines)-1-n-i] {
			return false
		}
	}
	return true
}

func lastN(lines []string, n int) []string {
	if n >= len(lines) {
		return lines
	}
	return lines[len(lines)-n:]
}
