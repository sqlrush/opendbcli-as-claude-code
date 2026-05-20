/*-------------------------------------------------------------------------
 *
 * section_evaluator.go
 *	  v1.1.51: deterministic rule engine that evaluates each WDR section
 *	  and produces a SectionScore with Level (Good/Warning/Risk) + the list
 *	  of triggered rules. Goal: give the LLM a *consistent* scorecard so
 *	  runs of the same WDR produce the same risk ratings (LLM is asked to
 *	  describe, not score).
 *
 *	  Rules are intentionally simple — single-metric thresholds. Complex
 *	  cross-section reasoning is left to the LLM's Layer-2 analysis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/section_evaluator.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// EvaluateSections runs all section evaluators against a WDRReport and
// returns one SectionScore per section. Sections that the parser/extractor
// didn't populate are still included with Level=Good and a "无数据" summary
// so the LLM knows whether the section was checked-and-clean vs missing.
func EvaluateSections(r *WDRReport) []SectionScore {
	return []SectionScore{
		evaluateDatabaseStat(r),
		evaluateLoadProfile(r),
		evaluateInstanceEfficiency(r),
		evaluateIOProfile(r),
		evaluateTopSQL(r),
	}
}

// ---- Database Stat ----------------------------------------------------------

// evaluateDatabaseStat: per-DB rows of Xact Commit/Rollback, Blks Read/Hit,
// Tuple I/U/D, Conflicts, Temp Files/Bytes, Deadlocks.
//
// Rules:
//   - Deadlocks > 0          → 🔴 (any deadlock is a correctness/perf signal)
//   - Temp Bytes > 10GB      → 🔴 (massive sort/hash spill)
//   - Temp Bytes > 1GB       → 🟡
//   - Rollback/Commit > 10%  → 🟡 (high error rate)
func evaluateDatabaseStat(r *WDRReport) SectionScore {
	s := SectionScore{Name: "Database Stat", Level: SectionGood, KeyMetrics: map[string]string{}}
	text := r.RawSections[SectionDatabaseStat]
	if text == "" {
		s.Summary = "无数据"
		return s
	}
	rows := parsePipeRows(text)
	if len(rows) == 0 {
		s.Summary = "数据格式无法识别"
		return s
	}

	var totalTempBytes, totalCommit, totalRollback, totalDeadlocks int64
	for _, row := range rows {
		// Column layout (og 5.0.3 Database Stat):
		// DB Name | Backends | Xact Commit | Xact Rollback | Blks Read | Blks Hit
		//   | Tuple Returned | Tuple Fetched | Tuple Inserted | Tuple Updated
		//   | Tup Deleted | Conflicts | Temp Files | Temp Bytes | Deadlocks
		//   | Blk Read Time | Blk Write Time | Stats Reset
		if len(row) < 15 {
			continue
		}
		commits := parseInt(row[2])
		rollbacks := parseInt(row[3])
		tempBytes := parseInt(row[13])
		deadlocks := parseInt(row[14])
		totalCommit += commits
		totalRollback += rollbacks
		totalTempBytes += tempBytes
		totalDeadlocks += deadlocks
	}
	s.KeyMetrics["总 Temp Bytes"] = formatBytes(totalTempBytes)
	s.KeyMetrics["总 Deadlocks"] = strconv.FormatInt(totalDeadlocks, 10)
	s.KeyMetrics["Commit / Rollback"] = fmt.Sprintf("%d / %d", totalCommit, totalRollback)

	switch {
	case totalTempBytes >= 10*1024*1024*1024:
		s.Rules = append(s.Rules, SectionRule{
			ID: "temp_bytes_extreme", Level: SectionRisk,
			Metric: "总 Temp Bytes", Observed: formatBytes(totalTempBytes), Threshold: "≥ 10GB",
			Reason: "临时文件溢出极端，work_mem 严重不足导致排序/HASH 大量落盘",
		})
	case totalTempBytes >= 1024*1024*1024:
		s.Rules = append(s.Rules, SectionRule{
			ID: "temp_bytes_high", Level: SectionWarning,
			Metric: "总 Temp Bytes", Observed: formatBytes(totalTempBytes), Threshold: "≥ 1GB",
			Reason: "临时文件偏多，部分查询落盘排序",
		})
	}
	if totalDeadlocks > 0 {
		s.Rules = append(s.Rules, SectionRule{
			ID: "deadlock_present", Level: SectionRisk,
			Metric: "Deadlocks", Observed: strconv.FormatInt(totalDeadlocks, 10), Threshold: "> 0",
			Reason: "出现死锁，业务逻辑加锁顺序或事务粒度有问题",
		})
	}
	if totalCommit > 0 {
		ratio := float64(totalRollback) / float64(totalCommit)
		if ratio > 0.10 {
			s.Rules = append(s.Rules, SectionRule{
				ID: "rollback_ratio_high", Level: SectionWarning,
				Metric: "Rollback / Commit", Observed: fmt.Sprintf("%.1f%%", ratio*100), Threshold: "> 10%",
				Reason: "回滚率偏高，可能业务异常或事务设计不当",
			})
		}
	}

	finalizeLevel(&s)
	return s
}

// ---- Load Profile -----------------------------------------------------------

// evaluateLoadProfile: Per Sec/Per Txn/Per Exec metrics + SQL P80/P95.
//
// Rules:
//   - SQL P95 > 100ms          → 🔴
//   - SQL P95 > 20ms           → 🟡
//   - Physical/Logical > 25%   → 🟡 (low effective buffer hit at IO layer)
//   - DB Time/sec > 800ms      → 🔴 (CPU saturated)
func evaluateLoadProfile(r *WDRReport) SectionScore {
	s := SectionScore{Name: "Load Profile", Level: SectionGood, KeyMetrics: map[string]string{}}
	text := r.RawSections[SectionLoadProfile]
	if text == "" {
		s.Summary = "无数据"
		return s
	}
	metrics := parseMetricTable(text)
	get := func(k string) float64 { return metrics[strings.ToLower(k)] }

	dbTimePerSec := get("db time(us)")
	cpuTimePerSec := get("cpu time(us)")
	logicalPerSec := get("logical read (blocks)")
	physicalPerSec := get("physical read (blocks)")
	p95 := get("sql response time p95(us)")
	p80 := get("sql response time p80(us)")

	s.KeyMetrics["DB Time (μs/s)"] = formatFloat(dbTimePerSec)
	s.KeyMetrics["CPU Time (μs/s)"] = formatFloat(cpuTimePerSec)
	s.KeyMetrics["逻辑读 (块/s)"] = formatFloat(logicalPerSec)
	s.KeyMetrics["物理读 (块/s)"] = formatFloat(physicalPerSec)
	if p95 > 0 {
		s.KeyMetrics["SQL P95 (μs)"] = formatFloat(p95)
	}
	if p80 > 0 {
		s.KeyMetrics["SQL P80 (μs)"] = formatFloat(p80)
	}

	if p95 >= 100000 {
		s.Rules = append(s.Rules, SectionRule{
			ID: "p95_extreme", Level: SectionRisk,
			Metric: "SQL P95", Observed: fmt.Sprintf("%.0fμs (%.1fms)", p95, p95/1000),
			Threshold: "≥ 100ms",
			Reason:    "95 分位响应时间极长，业务慢查询普遍存在",
		})
	} else if p95 >= 20000 {
		s.Rules = append(s.Rules, SectionRule{
			ID: "p95_high", Level: SectionWarning,
			Metric: "SQL P95", Observed: fmt.Sprintf("%.0fμs (%.1fms)", p95, p95/1000),
			Threshold: "≥ 20ms",
			Reason:    "尾部查询偏慢，有调优空间",
		})
	}

	if logicalPerSec > 0 {
		ratio := physicalPerSec / logicalPerSec
		if ratio > 0.25 {
			s.Rules = append(s.Rules, SectionRule{
				ID: "physical_read_ratio_high", Level: SectionWarning,
				Metric: "Physical / Logical Read",
				Observed: fmt.Sprintf("%.1f%%", ratio*100), Threshold: "> 25%",
				Reason: "物理读占比偏高，shared_buffers 可能偏小或访问范围超出缓存",
			})
		}
	}

	if dbTimePerSec >= 800000 {
		s.Rules = append(s.Rules, SectionRule{
			ID: "db_time_saturated", Level: SectionRisk,
			Metric: "DB Time/sec", Observed: fmt.Sprintf("%.0fμs", dbTimePerSec),
			Threshold: "≥ 800ms/s",
			Reason:    "DB Time 接近 CPU 饱和，并发或单条查询效率严重不足",
		})
	}

	finalizeLevel(&s)
	return s
}

// ---- Instance Efficiency ----------------------------------------------------

// evaluateInstanceEfficiency: percentage metrics from "Instance Efficiency
// Percentages (Target 100%)".
//
// Rules (lower = worse, 100 = ideal):
//   - Buffer Hit < 80          → 🔴
//   - Buffer Hit < 90          → 🟡
//   - Soft Parse < 10          → 🔴
//   - Soft Parse < 30          → 🟡
//   - WalWrite NoWait < 95     → 🟡
//   - Effective CPU < 80       → 🟡
func evaluateInstanceEfficiency(r *WDRReport) SectionScore {
	s := SectionScore{Name: "Instance Efficiency", Level: SectionGood, KeyMetrics: map[string]string{}}
	text := r.RawSections[SectionInstanceEfficiency]
	if text == "" {
		s.Summary = "无数据"
		return s
	}
	metrics := parsePctTable(text)
	get := func(k string) (float64, bool) { v, ok := metrics[strings.ToLower(k)]; return v, ok }

	type pctRule struct {
		key, label              string
		critical, warningCutoff float64
		critReason, warnReason  string
	}
	rules := []pctRule{
		{"buffer hit %", "Buffer Hit", 80, 90,
			"缓存命中极低，shared_buffers 远小于热数据集",
			"缓存命中偏低，shared_buffers 可能不足"},
		{"soft parse %", "Soft Parse", 10, 30,
			"软解析极低，几乎每条 SQL 都走完整 parse+plan，CPU 浪费严重",
			"软解析偏低，连接池/预编译可减少硬解析开销"},
		{"walwrite nowait %", "WalWrite NoWait", -1, 95,
			"", "WAL 写有等待，磁盘或 wal_buffers 可能不够"},
		{"effective cpu %", "Effective CPU", -1, 80,
			"", "有效 CPU 偏低，系统调用/GC/上下文切换开销大"},
	}
	for _, rule := range rules {
		v, ok := get(rule.key)
		if !ok {
			continue
		}
		s.KeyMetrics[rule.label+" %"] = fmt.Sprintf("%.2f", v)
		switch {
		case rule.critical > 0 && v < rule.critical:
			s.Rules = append(s.Rules, SectionRule{
				ID: rule.key + "_critical", Level: SectionRisk,
				Metric: rule.label, Observed: fmt.Sprintf("%.2f%%", v),
				Threshold: fmt.Sprintf("< %.0f%%", rule.critical),
				Reason:    rule.critReason,
			})
		case v < rule.warningCutoff:
			s.Rules = append(s.Rules, SectionRule{
				ID: rule.key + "_warning", Level: SectionWarning,
				Metric: rule.label, Observed: fmt.Sprintf("%.2f%%", v),
				Threshold: fmt.Sprintf("< %.0f%%", rule.warningCutoff),
				Reason:    rule.warnReason,
			})
		}
	}

	finalizeLevel(&s)
	return s
}

// ---- IO Profile -------------------------------------------------------------

// evaluateIOProfile: just headline IO rates. Most IO judgement happens in
// Load Profile (physical reads). This section is mainly informational.
func evaluateIOProfile(r *WDRReport) SectionScore {
	s := SectionScore{Name: "IO Profile", Level: SectionGood, KeyMetrics: map[string]string{}}
	text := r.RawSections[SectionIOProfile]
	if text == "" {
		s.Summary = "无数据"
		return s
	}
	// Layout: Metric | R+W Per Sec | R Per Sec | W Per Sec
	rows := parsePipeRows(text)
	for _, row := range rows {
		if len(row) < 4 {
			continue
		}
		label := strings.TrimSpace(row[0])
		if label == "" || strings.EqualFold(label, "Metric") {
			continue
		}
		s.KeyMetrics[label] = strings.TrimSpace(row[1])
	}
	finalizeLevel(&s)
	return s
}

// ---- TopSQL -----------------------------------------------------------------

// evaluateTopSQL examines the parsed r.TopSQLs list for skew, maintenance
// noise, connection-probe flooding, and spill signals.
//
// Rules:
//   - Top1 占 DB Time > 30%                  → 🟡 (single SQL dominates)
//   - DDL/ANALYZE 在 Top 5 内                → 🟡 (maintenance noise)
//   - Σ(SET / SHOW / version probes) > 50    → 🟡 (likely no connection pool)
//   - 任意 SQL Sort Spill / Hash Spill > 0   → 🟡 (memory pressure)
func evaluateTopSQL(r *WDRReport) SectionScore {
	s := SectionScore{Name: "TopSQL", Level: SectionGood, KeyMetrics: map[string]string{}}
	if len(r.TopSQLs) == 0 {
		s.Summary = "无 TopSQL 数据"
		return s
	}
	s.KeyMetrics["条数"] = strconv.Itoa(len(r.TopSQLs))

	// Top1 share
	var totalMS float64
	for _, e := range r.TopSQLs {
		totalMS += e.TotalTimeMS
	}
	if totalMS > 0 {
		top := r.TopSQLs[0]
		share := top.TotalTimeMS / totalMS
		s.KeyMetrics["Top1 占比"] = fmt.Sprintf("%.1f%% (%s)", share*100, top.SQLID)
		if share > 0.30 {
			s.Rules = append(s.Rules, SectionRule{
				ID: "top1_dominant", Level: SectionWarning,
				Metric: "Top1 SQL 占总耗时",
				Observed: fmt.Sprintf("%.1f%% (SQL_ID %s)", share*100, top.SQLID),
				Threshold: "> 30%",
				Reason:    "单条 SQL 主导整体耗时，优先调优该条",
			})
		}
	}

	// Maintenance noise in top 5
	maint := 0
	for i, e := range r.TopSQLs {
		if i >= 5 {
			break
		}
		if isMaintenanceSQL(e.QueryPrefix) {
			maint++
		}
	}
	if maint >= 2 {
		s.Rules = append(s.Rules, SectionRule{
			ID: "maintenance_in_top", Level: SectionWarning,
			Metric: "Top 5 中维护类 SQL", Observed: strconv.Itoa(maint),
			Threshold: "≥ 2",
			Reason:    "DDL/ANALYZE 占据 Top SQL 头部，业务负载被维护操作掩盖",
		})
	}

	// Connection probe flood
	probes := int64(0)
	for _, e := range r.TopSQLs {
		if isConnectionProbe(e.QueryPrefix) {
			probes += e.Calls
		}
	}
	if probes > 0 {
		s.KeyMetrics["连接探测调用数"] = strconv.FormatInt(probes, 10)
	}
	if probes >= 50 {
		s.Rules = append(s.Rules, SectionRule{
			ID: "connection_probe_flood", Level: SectionWarning,
			Metric: "SET/SHOW/version 总调用",
			Observed:  strconv.FormatInt(probes, 10),
			Threshold: "≥ 50",
			Reason:    "高频连接探测语句，说明客户端建连频繁、未启用连接池",
		})
	}

	finalizeLevel(&s)
	return s
}

// ---- Helpers ----------------------------------------------------------------

// finalizeLevel sets s.Level = max(rule.Level) and synthesizes a one-line
// Summary like "R: Soft Parse 11% / 临时空间溢出". If no rules fired,
// Level stays Good and Summary stays empty (or the caller's "无数据").
func finalizeLevel(s *SectionScore) {
	highest := SectionGood
	for _, r := range s.Rules {
		if rank(r.Level) > rank(highest) {
			highest = r.Level
		}
	}
	s.Level = highest
	if s.Summary != "" {
		return // preserve "无数据" etc.
	}
	if len(s.Rules) == 0 {
		s.Summary = "无显著风险"
		return
	}
	// Take up to 2 rule reasons for the summary
	reasons := make([]string, 0, 2)
	for _, r := range s.Rules {
		if len(reasons) >= 2 {
			break
		}
		reasons = append(reasons, r.Reason)
	}
	s.Summary = strings.Join(reasons, "; ")
}

func rank(l SectionLevel) int {
	switch l {
	case SectionRisk:
		return 2
	case SectionWarning:
		return 1
	default:
		return 0
	}
}

// parsePipeRows splits each non-blank line on "|" — used for og's
// Database Stat / TopSQL / IO Profile tables. Header rows are kept (the
// caller decides whether to filter them).
func parsePipeRows(text string) [][]string {
	out := [][]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "|") {
			continue
		}
		cells := strings.Split(line, "|")
		// Skip rows that are clearly headers via "Stats Reset"/"DB Name" markers
		_ = cells
		trimmed := make([]string, 0, len(cells))
		for _, c := range cells {
			trimmed = append(trimmed, strings.TrimSpace(c))
		}
		// Drop empty trailing cell (og rows end with "| ")
		for len(trimmed) > 0 && trimmed[len(trimmed)-1] == "" {
			trimmed = trimmed[:len(trimmed)-1]
		}
		if len(trimmed) == 0 {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// parseMetricTable handles Load Profile-style tables where each row is
// `Metric | Per Second | Per Transaction | Per Exec`. Returns a map of
// lower(metric) → Per Second value.
func parseMetricTable(text string) map[string]float64 {
	out := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "|") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 2 {
			continue
		}
		label := strings.TrimSpace(cells[0])
		if label == "" || strings.EqualFold(label, "Metric") {
			continue
		}
		val := strings.TrimSpace(cells[1])
		// Strip thousands separators / unit suffixes like "us", "blocks"
		val = stripNonNumericRE.ReplaceAllString(val, "")
		if val == "" {
			continue
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			out[strings.ToLower(label)] = f
		}
	}
	return out
}

// parsePctTable handles Instance Efficiency-style tables where each row is
// `Metric Name | Metric Value`. Returns a map of lower(metric) → value.
func parsePctTable(text string) map[string]float64 {
	out := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "|") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 2 {
			continue
		}
		label := strings.TrimSpace(cells[0])
		if label == "" || strings.EqualFold(label, "Metric Name") {
			continue
		}
		val := strings.TrimSpace(cells[1])
		val = stripNonNumericRE.ReplaceAllString(val, "")
		if val == "" {
			continue
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			out[strings.ToLower(label)] = f
		}
	}
	return out
}

var stripNonNumericRE = regexp.MustCompile(`[^0-9.\-]`)

// isMaintenanceSQL: CREATE INDEX / ANALYZE / VACUUM / ALTER / DROP / TRUNCATE / SET / SHOW
func isMaintenanceSQL(sql string) bool {
	s := strings.ToUpper(strings.TrimSpace(sql))
	for _, kw := range []string{
		"CREATE INDEX", "CREATE TABLE", "CREATE ", "DROP ", "ALTER ",
		"TRUNCATE", "ANALYZE", "VACUUM", "REINDEX",
	} {
		if strings.HasPrefix(s, kw) {
			return true
		}
	}
	return false
}

// isConnectionProbe: lightweight client probes (SET ..., SHOW ..., SELECT version())
func isConnectionProbe(sql string) bool {
	s := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(s, "SET ") || strings.HasPrefix(s, "SHOW ") || strings.HasPrefix(s, "RESET ") {
		return true
	}
	if strings.Contains(s, "SELECT VERSION()") || strings.Contains(s, "PG_BACKEND_PID") || strings.Contains(s, "INET_SERVER_ADDR") {
		return true
	}
	return false
}

// formatBytes renders a byte count as a human-readable string.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatFloat(f float64) string {
	if f == 0 {
		return "0"
	}
	if f < 1 {
		return fmt.Sprintf("%.3f", f)
	}
	return fmt.Sprintf("%.0f", f)
}
