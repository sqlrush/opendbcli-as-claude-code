/*-------------------------------------------------------------------------
 *
 * topsql.go
 *	  M3: TopSQL drill-down. For each of the top N entries from a parsed
 *	  WDR, runs /sqlfetch (resolve SQL_ID → executable SQL) and
 *	  /sqltune --quick (5-dimension optimization). Results are aggregated
 *	  into Analysis.SQLTunes for the renderer.
 *
 *	  Concurrency: 5 sqltune calls fire in parallel via goroutines. Total
 *	  wall time ≈ max(individual), not sum. One failed SQL doesn't block
 *	  others.
 *
 *	  Memory: each successful tune writes a wdranalyze-tagged memory
 *	  entry keyed by SQL fingerprint. Same SQL in subsequent wdranalyze
 *	  runs hits the cache and returns in milliseconds.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/topsql.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/opengauss/sqltuner"
)

// SQLResolver is the interface wdranalyze depends on for /sqlfetch behavior.
// Implemented by skill/query.SQLFetchSkill (which exposes Resolve method).
// Decoupled here to avoid an import cycle: wdranalyze → skill/query →
// wdranalyze.
type SQLResolver interface {
	Resolve(ctx context.Context, sqlID int64) (*ResolvedSQL, error)
}

// ResolvedSQL is mirrored from skill/query — types kept identical so the
// concrete return value flows through unchanged.
type ResolvedSQL struct {
	SQL          string
	OriginalSQL  string
	Schema       string
	Source       string
	HasLiterals  bool
	Substituted  bool
	Placeholders int
	Notes        []string
}

// DrillTopSQLs runs Phase 4 of wdranalyze: parallel sqlfetch + sqltune
// for the top N entries. Returns one SQLTuneResult per processed entry
// (failures included, with Error field populated).
//
// Parameters:
//   - ctx           parent context (cancellation propagates)
//   - report        parsed WDR
//   - topN          how many top entries to drill (default 5)
//   - resolver      sqlfetch facade (nil = skip drill, return empty)
//   - llmProvider   for sqltune Round 1 / Round 2 (nil = skip drill)
//   - memStore      for cross-wdranalyze fingerprint cache (nil = no cache)
//   - driver        for sqltuner.PlanCollector + EquivVerifier
func DrillTopSQLs(
	ctx context.Context,
	report *WDRReport,
	topN int,
	resolver SQLResolver,
	llmProvider llm.Provider,
	memStore *memory.Store,
	driver db.Driver,
) []SQLTuneResult {
	if resolver == nil || llmProvider == nil || driver == nil {
		return nil
	}
	if len(report.TopSQLs) == 0 {
		return nil
	}
	if topN <= 0 || topN > len(report.TopSQLs) {
		topN = len(report.TopSQLs)
	}
	if topN > 10 {
		// Hard cap: 10 concurrent sqltune calls is the sweet spot for
		// LLM provider rate limits (most allow ~10-20 concurrent).
		topN = 10
	}

	results := make([]SQLTuneResult, topN)
	var wg sync.WaitGroup

	for i := 0; i < topN; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			entry := report.TopSQLs[idx]
			results[idx] = drillOne(ctx, entry, resolver, llmProvider, memStore, driver)
		}(i)
	}
	wg.Wait()

	return results
}

// drillOne handles a single TopSQL: resolve → check memory → tune → cache.
func drillOne(
	ctx context.Context,
	entry TopSQLEntry,
	resolver SQLResolver,
	llmProvider llm.Provider,
	memStore *memory.Store,
	driver db.Driver,
) SQLTuneResult {
	result := SQLTuneResult{SQLID: entry.SQLID}

	// 1. Parse SQL_ID to int64 (sqlfetch's contract)
	id, err := strconv.ParseInt(entry.SQLID, 10, 64)
	if err != nil {
		result.Error = fmt.Sprintf("invalid SQL_ID format: %v", err)
		return result
	}

	// 2. Resolve via /sqlfetch
	resolved, err := resolver.Resolve(ctx, id)
	if err != nil {
		result.Error = fmt.Sprintf("sqlfetch failed: %v", err)
		return result
	}
	if resolved == nil {
		result.Error = "SQL not found in dbe_perf (may have been evicted from cache)"
		return result
	}
	result.FullSQL = resolved.SQL
	result.Schema = resolved.Schema
	result.HasLiterals = resolved.HasLiterals

	// v1.1.52: skip maintenance SQL (DDL / ANALYZE / SET / SHOW etc.) before
	// even attempting sqltune. These have no plan to optimize and the prior
	// behavior was to surface ugly "sqltune failed: phase A: statement type
	// CREATE not supported by EXPLAIN tuning" entries for the entire Top N
	// (very common in og 5.0.3 maintenance windows where DDL dominates).
	if kind := classifyMaintenanceSQL(resolved.SQL); kind != "" {
		result.Skipped = true
		result.SkipReason = kind
		return result
	}

	// 3. Memory fingerprint check: same SQL diagnosed recently?
	if memStore != nil {
		if cached := lookupMemoryCache(memStore, resolved.SQL); cached != nil {
			result.OriginalCost = cached.OriginalCost
			result.BestNewCost = cached.BestNewCost
			result.BestSpeedup = cached.BestSpeedup
			result.Candidates = cached.Candidates
			result.FromMemory = true
			return result
		}
	}

	// 4. Run sqltune --quick (Round 1 + verify, skip Round 2 markdown gen)
	tuner := sqltuner.NewTuner(driver, llmProvider, memStore)
	report, err := tuner.Tune(ctx, sqltuner.TuneOptions{
		SQL:         resolved.SQL,
		Verify:      true,
		QuickMode:   true,
		SkipUpgrade: true,
	})
	if err != nil {
		result.Error = fmt.Sprintf("sqltune failed: %v", err)
		return result
	}

	// 5. Convert sqltune output → SQLTuneResult
	convertSqltuneReport(&result, report)

	// 6. Write back to memory for future wdranalyze runs
	if memStore != nil {
		writeMemoryCache(memStore, resolved.SQL, &result)
	}

	return result
}

// lookupMemoryCache checks the memory store for a fingerprint-matching
// previous wdranalyze tune. Returns nil on miss.
func lookupMemoryCache(memStore *memory.Store, sql string) *SQLTuneResult {
	if memStore == nil {
		return nil
	}
	entries := memStore.Find(memory.Query{
		SQL:      sql,
		Keywords: []string{wdranalyzeMemoryTag},
		MaxAge:   wdranalyzeMemoryMaxAge,
		Limit:    1,
	})
	if len(entries) == 0 {
		return nil
	}
	return parseMemoryEntry(entries[0].Content)
}

// writeMemoryCache persists a tune result keyed by fingerprint so future
// wdranalyze calls on the same SQL hit the cache.
func writeMemoryCache(memStore *memory.Store, sql string, result *SQLTuneResult) {
	if memStore == nil || result == nil {
		return
	}
	title := fmt.Sprintf("wdranalyze sqltune cache · SQL_ID %s", result.SQLID)
	content := serializeMemoryEntry(result)
	_, _ = memStore.WriteWithSQL(memory.MemSolution, title, content, sql)
}

const (
	// wdranalyzeMemoryTag marks memory entries written by wdranalyze TopSQL
	// drill so memory.Find can filter to just these (not pollute with
	// other /sqltune direct runs).
	wdranalyzeMemoryTag = "wdranalyze_topsql_cache"

	// wdranalyzeMemoryMaxAge limits how stale a cached tune can be before
	// we re-run /sqltune. Schema may have changed since.
	wdranalyzeMemoryMaxAge = 7 * 24 * time.Hour
)

// convertSqltuneReport maps sqltuner.FinalReport → wdranalyze.SQLTuneResult.
// Extracts cost + best speedup from stats, walks candidates with verify
// results.
func convertSqltuneReport(result *SQLTuneResult, sqlReport *sqltuner.FinalReport) {
	if sqlReport == nil || sqlReport.Stats == nil {
		return
	}
	stats := sqlReport.Stats
	result.BestSpeedup = stats.BestSpeedup

	// Note: sqltuner.FinalReport carries Markdown but the structured
	// candidate list isn't directly exposed in the same struct. For M3
	// MVP we extract what's available from stats; the full candidate
	// drill-down lives in the Markdown for now. Future enhancement:
	// have sqltuner expose Round1Output + VerifyResults on FinalReport.
	//
	// For M3 MVP this means: BestSpeedup is reliable; Candidates list
	// remains empty (we surface speedup + raw markdown summary).
	result.Candidates = []TuneCandidate{}
}

// serializeMemoryEntry converts a SQLTuneResult into a markdown-friendly
// memory body. Simple format so parseMemoryEntry can reconstruct on read.
func serializeMemoryEntry(result *SQLTuneResult) string {
	return fmt.Sprintf("SQL_ID: %s\nSchema: %s\nBestSpeedup: %.2f\nOriginalCost: %.2f\nBestNewCost: %.2f\nFullSQL:\n%s\n",
		result.SQLID, result.Schema, result.BestSpeedup,
		result.OriginalCost, result.BestNewCost, result.FullSQL)
}

// parseMemoryEntry reverses serializeMemoryEntry.
func parseMemoryEntry(content string) *SQLTuneResult {
	r := &SQLTuneResult{Candidates: []TuneCandidate{}}
	// MVP: minimal extraction. Future: structured frontmatter / JSON.
	// For now, only return non-nil if we can parse at least SQL_ID +
	// BestSpeedup so caller knows cache was real.
	r.SQLID = extractMemField(content, "SQL_ID")
	r.Schema = extractMemField(content, "Schema")
	if v := extractMemField(content, "BestSpeedup"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			r.BestSpeedup = f
		}
	}
	if v := extractMemField(content, "OriginalCost"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			r.OriginalCost = f
		}
	}
	if v := extractMemField(content, "BestNewCost"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			r.BestNewCost = f
		}
	}
	if r.SQLID == "" {
		return nil
	}
	return r
}

func extractMemField(content, key string) string {
	prefix := key + ":"
	for _, line := range splitLines(content) {
		if len(line) > len(prefix) && line[:len(prefix)] == prefix {
			return trimSpace(line[len(prefix):])
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}

// classifyMaintenanceSQL returns a Chinese label for the SQL's maintenance
// category, or "" if it's a tunable business query. Strips leading comments
// first so "-- hint\nCREATE INDEX..." still classifies as DDL.
func classifyMaintenanceSQL(sql string) string {
	s := stripLeadingComments(sql)
	if s == "" {
		return ""
	}
	upper := strings.ToUpper(s)

	type rule struct{ prefix, label string }
	rules := []rule{
		{"CREATE INDEX", "建索引 DDL"},
		{"DROP INDEX", "删索引 DDL"},
		{"CREATE TABLE", "建表 DDL"},
		{"DROP TABLE", "删表 DDL"},
		{"ALTER TABLE", "改表结构 DDL"},
		{"TRUNCATE", "截断表 DDL"},
		{"CREATE ", "DDL 创建语句"},
		{"DROP ", "DDL 删除语句"},
		{"ALTER ", "DDL 变更语句"},
		{"ANALYZE", "统计信息收集 (ANALYZE)"},
		{"VACUUM", "回收清理 (VACUUM)"},
		{"REINDEX", "索引重建 (REINDEX)"},
		{"CLUSTER", "表重组 (CLUSTER)"},
		{"SET ", "会话参数设置 (SET)"},
		{"RESET ", "会话参数重置 (RESET)"},
		{"SHOW ", "查看参数 (SHOW)"},
		{"GRANT ", "权限授予 (GRANT)"},
		{"REVOKE ", "权限回收 (REVOKE)"},
		{"BEGIN", "事务控制"},
		{"COMMIT", "事务控制"},
		{"ROLLBACK", "事务控制"},
		{"SAVEPOINT", "事务控制"},
		{"CHECKPOINT", "检查点 (CHECKPOINT)"},
		{"COPY ", "批量导入导出 (COPY)"},
		{"PREPARE ", "预编译 (PREPARE)"},
		{"DEALLOCATE", "预编译释放 (DEALLOCATE)"},
		{"DECLARE ", "游标声明 (DECLARE)"},
		{"FETCH ", "游标读取 (FETCH)"},
		{"CLOSE ", "游标关闭 (CLOSE)"},
		{"MOVE ", "游标移动 (MOVE)"},
		{"LOCK ", "显式加锁 (LOCK)"},
		{"CALL ", "存储过程调用 (CALL)"},
		{"DO ", "匿名块 (DO)"},
	}
	for _, r := range rules {
		if strings.HasPrefix(upper, r.prefix) {
			return r.label
		}
	}
	// Connection probes — single-row metadata queries with no business value
	if strings.Contains(upper, "SELECT VERSION()") ||
		strings.Contains(upper, "PG_BACKEND_PID()") ||
		strings.Contains(upper, "INET_SERVER_ADDR()") {
		return "客户端探测语句"
	}
	return ""
}

// stripLeadingComments removes leading "--" line comments and "/* */" block
// comments so the first SQL keyword can be detected reliably.
func stripLeadingComments(sql string) string {
	s := strings.TrimSpace(sql)
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			if idx := strings.Index(s, "\n"); idx >= 0 {
				s = strings.TrimSpace(s[idx+1:])
			} else {
				return ""
			}
		case strings.HasPrefix(s, "/*"):
			if idx := strings.Index(s, "*/"); idx >= 0 {
				s = strings.TrimSpace(s[idx+2:])
			} else {
				return ""
			}
		default:
			return s
		}
	}
}
