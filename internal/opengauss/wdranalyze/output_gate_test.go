package wdranalyze

import (
	"strings"
	"testing"
)

func TestGateWDRSynthesisDemotesWarningP0AndCleansAdvice(t *testing.T) {
	input := `## Layer 2: 风险详解
### R1: 回滚率偏高 — Database Stat 🟡

## Layer 3: 优化方案
| 优先级 | 优化项 | 关联风险 | 操作 | 预期效果 |
|---|---|---|---|---|
| P0 | 启用预编译 | R1 | SELECT usename, xact_rollback, xact_commit FROM pg_stat_database;<br>ALTER SYSTEM SET statement_cache_size = 10000; | CPU 利用率降低 15% |

预计 1 周内可改善。`
	got := GateWDRSynthesis(input)
	for _, notWant := range []string{"<br>", "| P0 |", "SELECT usename", "CPU 利用率降低 15%", "预计 1 周内"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("GateWDRSynthesis leaked %q:\n%s", notWant, got)
		}
	}
	for _, want := range []string{"| P1 |", "SELECT datname, xact_commit, xact_rollback FROM pg_stat_database;", "兼容性门禁", "需复测量化"} {
		if !strings.Contains(got, want) {
			t.Fatalf("GateWDRSynthesis missing %q:\n%s", want, got)
		}
	}
}
