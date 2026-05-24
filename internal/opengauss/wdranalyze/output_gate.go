package wdranalyze

import (
	"regexp"
	"strings"
)

// GateWDRSynthesis keeps LLM-authored WDR prose inside DBA-safe boundaries.
// It does not remove useful analysis; it only fixes presentation artifacts and
// demotes/labels advice when the evidence package does not justify execution.
func GateWDRSynthesis(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</br>", "\n")
	s = fixKnownGaussDBSQLAdvice(s)
	if !wdrTextHasCriticalEvidence(s) {
		s = downgradeP0Advice(s)
	}
	s = softenUnsupportedExactGains(s)
	if wdrNeedsCompatibilityNote(s) && !strings.Contains(s, "兼容性门禁") {
		s += "\n\n> 兼容性门禁：报告中出现 PostgreSQL 习惯对象/参数或应用侧组件名称，执行前必须在当前 GaussDB/openGauss 版本上确认是否支持；未确认前只作为排查方向，不作为可直接执行变更。"
	}
	return strings.TrimSpace(s)
}

func wdrTextHasCriticalEvidence(s string) bool {
	lower := strings.ToLower(s)
	if strings.Contains(s, "🔴") || strings.Contains(lower, "critical") || strings.Contains(s, "严重") {
		return true
	}
	criticalNeedles := []string{"deadlocks > 0", "死锁", "阻塞链", "lock wait", "temp bytes > 0", "磁盘打满", "checkpoint storm"}
	for _, n := range criticalNeedles {
		if strings.Contains(lower, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func downgradeP0Advice(s string) string {
	replacements := []struct{ old, new string }{
		{"| P0 |", "| P1 |"},
		{"|P0|", "|P1|"},
		{" P0 ", " P1 "},
	}
	for _, r := range replacements {
		s = strings.ReplaceAll(s, r.old, r.new)
	}
	re := regexp.MustCompile(`(?m)^(\s{0,6}(?:#{1,6}\s*)?)P0\b`)
	s = re.ReplaceAllString(s, `${1}P1`)
	re = regexp.MustCompile(`(?m)^(\s*[-*]\s*)P0\b`)
	s = re.ReplaceAllString(s, `${1}P1`)
	return s
}

func fixKnownGaussDBSQLAdvice(s string) string {
	// pg_stat_database has datname, not usename. Keep the useful idea but fix
	// the query so golden tests catch future PG-ism regressions.
	s = strings.ReplaceAll(s,
		"SELECT usename, xact_rollback, xact_commit FROM pg_stat_database;",
		"SELECT datname, xact_commit, xact_rollback FROM pg_stat_database;")
	s = strings.ReplaceAll(s,
		"SET LOCAL isolation_level = 'read committed';",
		"-- 如需验证事务隔离级别，使用当前 GaussDB/openGauss 支持的语法；不要在 WDR 报告中直接执行未验证 SET")
	return s
}

func softenUnsupportedExactGains(s string) string {
	re := regexp.MustCompile(`CPU([^。\n]{0,24})降低\s*\d+(?:\.\d+)?%`)
	s = re.ReplaceAllString(s, "CPU${1}下降（需复测量化）")
	re = regexp.MustCompile(`预计\s*\d+\s*(?:天|周|小时|分钟)内`)
	s = re.ReplaceAllString(s, "预计复测通过后")
	return s
}

func wdrNeedsCompatibilityNote(s string) bool {
	lower := strings.ToLower(s)
	needles := []string{"pg_stat_statements", "pgbouncer", "statement_cache_size", "enable_prepared_statement", "prepare threshold"}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}
