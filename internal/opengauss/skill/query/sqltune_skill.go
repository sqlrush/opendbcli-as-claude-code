/*-------------------------------------------------------------------------
 *
 * sqltune_skill.go
 *	  SQLTuneSkill provides complex SQL performance tuning for
 *	  OpenGauss. See docs/sqltune/design-og-sqltuner.md for design.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/sqltune_skill.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/model"
	_ "github.com/sqlrush/opendb/internal/opengauss/sqltuner" // register og planner + tuner factory
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/sqltune"
)

// SQLTuneSkill provides complex SQL performance tuning for OpenGauss.
// See docs/sqltune/design-og-sqltuner.md for design.
type SQLTuneSkill struct {
	driver   db.Driver
	modelMgr *model.Manager
	memStore *memory.Store
}

// NewSQLTuneSkill constructs a /sqltune skill.
// memStore may be nil — tuner gracefully skips memory injection.
func NewSQLTuneSkill(driver db.Driver, modelMgr *model.Manager, memStore *memory.Store) *SQLTuneSkill {
	return &SQLTuneSkill{
		driver:   driver,
		modelMgr: modelMgr,
		memStore: memStore,
	}
}

func (s *SQLTuneSkill) Name() string { return "sqltune" }
func (s *SQLTuneSkill) Description() string {
	return "复杂 SQL 性能调优 (LLM + 真实数据 + 验证闭环)"
}
func (s *SQLTuneSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLTuneSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqltune",
		Description: "Analyze a SQL with EXPLAIN + schema + dialect + runtime context, run LLM-driven tuning to produce 5-dimension optimization proposals (rewrite/index/hint/schema/stats), each verified by EXPLAIN cost diff and equivalence check. " +
			"**Accepts either full SQL text OR a numeric SQL_ID** (unique_query_id from /slowsql or dbe_perf.statement). " +
			"When given a SQL_ID, automatically fetches the normalized SQL from dbe_perf.statement_history and substitutes placeholders with safe defaults so EXPLAIN succeeds. " +
			"Default mode is 'quick' (Round 1 + verify only, ~30-90s) which is what most LLMs should use. " +
			"Use mode='deep' only when quick mode's report is insufficient — deep adds Round 2 LLM polish + auto-upgrade iterative refinement, costs 5-15min. " +
			"This tool returns a complete optimization report ready for the user — DO NOT call additional tools afterwards to 'verify' or 'check'; surface the report verbatim.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Either full SQL text (SELECT/WITH ...) OR a numeric SQL_ID like 581990336. SQL_ID is preferred when user mentions one — opendb auto-fetches the SQL.",
				},
				"mode": map[string]any{
					"type":        "string",
					"enum":        []string{"quick", "deep"},
					"description": "Optional. 'quick' (default, ~30-90s) skips Round 2 LLM and auto-upgrade. 'deep' (~5-15min) runs full pipeline. Prefer 'quick' for first attempt.",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *SQLTuneSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "sqltune",
		Aliases:     []string{"sqlt"},
		Usage:       "/sqltune <SQL>",
		Description: "复杂 SQL 性能调优 (5 维度方案 + EXPLAIN 验证 + 等价性检查)",
		Examples: []string{
			"/sqltune SELECT count(*) FROM orders WHERE uid = 12345",
			"/sqltune <粘贴你的复杂 SQL>",
		},
	}
}

func (s *SQLTuneSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("需要提供 SQL")
	}
	return nil
}

func (s *SQLTuneSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	sqltextArg := strings.TrimSpace(params.StringOr("args", ""))
	sqlText := sqltextArg
	sqlIDForReport := "" // set when input was a SQL_ID (for status banner)
	var resolvedSQL *ResolvedSQL
	if sqlText == "" {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "  /sqltune 需要提供 SQL — 用法: /sqltune <SQL>",
		}, nil
	}

	// 检查 LLM 可用性 — sqltune 强依赖 LLM
	if s.modelMgr == nil || s.modelMgr.Provider() == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "  /sqltune 需要配置 LLM 模型 (active_model). 当前无可用 model.\n  请用 `dbaa configure` 配置 model 后重试.",
		}, nil
	}

	// SQL_ID 模式: 用户传纯数字 ID，或 LLM 传 "SQL ID 581990336 如何优化"
	// 这类混合文本时，复用 /sqlfetch 的单一解析路径。不要在 /sqltune 内
	// 再维护一套弱版 statement_history 查询 / 占位符替换逻辑。
	if sqlID, ok := parseSQLTuneIDArg(sqlText); ok {
		resolved, err := NewSQLFetchSkill(s.driver).Resolve(ctx, sqlID)
		if err != nil {
			return &skill.Result{
				Type: skill.ResultError,
				Rendered: fmt.Sprintf(
					"  ⚠️ /sqltune SQL_ID=%d 解析失败: %s\n"+
						"     恢复: 直接传完整 SQL 文本 → /sqltune <SELECT ...>",
					sqlID, err.Error()),
				Summary: err.Error(),
			}, nil
		}
		if resolved == nil || strings.TrimSpace(resolved.SQL) == "" {
			return &skill.Result{
				Type: skill.ResultError,
				Rendered: fmt.Sprintf(
					"  ⚠️ /sqltune SQL_ID=%d 在 dbe_perf.statement_history / dbe_perf.statement 都找不到\n"+
						"     可能: ① track_stmt_stat_level=L0 且 unique SQL 已过期 ② SQL 已被 ring buffer 覆盖 ③ ID 错\n"+
						"     恢复: 直接传完整 SQL 文本 → /sqltune <SELECT ...>",
					sqlID),
				Summary: fmt.Sprintf("SQL_ID %d not found", sqlID),
			}, nil
		}
		sqlText = resolved.SQL
		sqlIDForReport = strconv.FormatInt(sqlID, 10)
		resolvedSQL = resolved
	}

	// v1.1.31: default to QuickMode + SkipUpgrade. Real-world trace showed
	// Round 2 LLM markdown gen + auto-upgrade deep mode pushed sqltune to
	// 5-15min on 10-table SQL, which then timed out the engine's per-round
	// LLM call (>600s) when accumulated context grew large. Quick mode skips
	// Round 2 (uses deterministic fallback render — same 5-dim structure,
	// just no LLM prose polish) and skips deep mode auto-upgrade. Brings
	// typical complex-SQL sqltune call from ~10min to 30-90s. Users wanting
	// full mode pass mode=deep via tool params.
	mode := strings.TrimSpace(params.StringOr("mode", "quick"))

	// M1.5: dispatch through neutral sqltune.BuildTuner so this skill
	// is dialect-free. og tuner is registered at init in the og
	// sqltuner package, lookup-by-DialectKind picks it.
	engine, err := sqltune.BuildTuner(sqltune.DialectOpenGauss, sqltune.TunerDeps{
		Driver:   s.driver,
		Provider: s.modelMgr.Provider(),
		MemStore: s.memStore,
	})
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqltune 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}
	report, err := engine.Tune(ctx, sqltune.TuneOptions{
		SQL:         sqlText,
		Verify:      true,
		QuickMode:   mode != "deep",
		SkipUpgrade: mode != "deep",
		ForceDeep:   mode == "deep",
	})
	if err != nil {
		// Surface placeholder errors specifically — the LLM (or user) can
		// then fetch the literal-bearing SQL from dbe_perf.statement_history
		// and retry. Without this branch the error gets wrapped as the
		// opaque "phase A: plan collection failed" which is what made three
		// different LLMs (35B / Opus / GLM-5.1) all fail to recover.
		var pe *sqltune.PlaceholderError
		if errors.As(err, &pe) {
			msg := fmt.Sprintf(
				"  ⚠️ /sqltune 输入 SQL 含 %d 个未绑定占位符 %v (kind=%s)\n"+
					"     这通常是从 dbe_perf.statement (归一化视图) 拿来的 SQL，无法直接 EXPLAIN。\n"+
					"     请改从 dbe_perf.statement_history (带字面量) 取，或把 ? / $N 替换成实际值后重试。\n"+
					"     示例查询带字面量原始 SQL：\n"+
					"       SELECT query FROM dbe_perf.statement_history\n"+
					"        WHERE unique_query LIKE '%%<前缀>%%'\n"+
					"        ORDER BY start_time DESC LIMIT 1;",
				len(pe.Placeholders), pe.Placeholders, pe.DetectedKind)
			return &skill.Result{
				Type:     skill.ResultError,
				Rendered: msg,
				Summary:  pe.Error(),
			}, nil
		}
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqltune 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}

	// Summary doubles as a "next round" hint to the LLM agent: signal
	// that the report IS the final answer and the agent must surface it
	// verbatim instead of continuing tool calls. 35B agent kept calling
	// /explain or /sql after a successful sqltune; this banner aims to
	// preempt that. opus exhibits the same drift, so the wording is
	// model-agnostic.
	bestSummary := "best unverified"
	if report.Stats.VerifiedCount > 0 {
		bestSummary = fmt.Sprintf("best %.1f×", report.Stats.BestSpeedup)
	}
	summary := fmt.Sprintf(
		"✅ DONE: sqltune completed (%d candidates, %d verified, %s, %s). "+
			"The 'rendered' markdown above IS THE FINAL ANSWER for the user — "+
			"surface it verbatim. DO NOT call additional tools (no /explain, /sql, "+
			"/health follow-ups). User wants the report, not a one-line version check.",
		report.Stats.CandidateCount, report.Stats.VerifiedCount,
		bestSummary, report.Stats.TotalDuration.Round(1e6))

	rendered := report.Markdown
	if sqlIDForReport != "" {
		banner := sqlTuneSQLIDBanner(sqlIDForReport, resolvedSQL)
		rendered = banner + rendered
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     report.Stats,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

func sqlTuneSQLIDBanner(sqlID string, resolved *ResolvedSQL) string {
	if resolved == nil {
		return fmt.Sprintf("> ℹ️ SQL_ID=%s 已解析并进入 /sqltune。\n\n", sqlID)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("> ℹ️ SQL_ID=%s 已由 /sqlfetch 解析后进入 /sqltune\n", sqlID))
	if resolved.Source != "" {
		b.WriteString(fmt.Sprintf("> - 来源: dbe_perf.%s\n", resolved.Source))
	}
	if resolved.Schema != "" {
		b.WriteString(fmt.Sprintf("> - 原会话 schema/search_path: %s\n", resolved.Schema))
	}
	if resolved.Substituted {
		b.WriteString(fmt.Sprintf("> - 占位符: 已自动替换 %d 个归一化占位符，plan/cost 为 synthetic bind 参考\n", resolved.Placeholders))
		b.WriteString("> - 证据口径: synthetic bind 仅用于让 EXPLAIN 成功，报告只代表 plan-shape；不要把替换后的字面量当作真实业务取值\n")
	} else if resolved.Placeholders > 0 {
		b.WriteString(fmt.Sprintf("> - 占位符: 仍检测到 %d 个占位符，若 EXPLAIN 失败请提供真实 SQL 文本\n", resolved.Placeholders))
	}
	for _, note := range resolved.Notes {
		if strings.TrimSpace(note) != "" {
			b.WriteString("> - " + strings.TrimSpace(note) + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

func parseSQLTuneIDArg(arg string) (int64, bool) {
	s := strings.TrimSpace(arg)
	if s == "" || startsWithSQLKeyword(s) {
		return 0, false
	}
	id, err := parseSQLID(s)
	if err != nil {
		return 0, false
	}
	if looksLikeSQLID(s) {
		return id, true
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "sql_id") || strings.Contains(lower, "sql id") || strings.Contains(lower, "sqlid") {
		return id, true
	}
	if hasSingleDigitRun(s) && hasTuneIntent(lower) {
		return id, true
	}
	return 0, false
}

func startsWithSQLKeyword(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	fields := strings.Fields(lower)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "select", "with", "insert", "update", "delete", "merge", "explain", "create", "alter", "drop", "truncate", "do", "call":
		return true
	default:
		return false
	}
}

func hasTuneIntent(lower string) bool {
	for _, kw := range []string{"优化", "调优", "怎么改", "如何改", "tune", "tuning", "optimize", "执行计划", "慢"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func hasSingleDigitRun(s string) bool {
	runs := 0
	inRun := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			if !inRun {
				runs++
				inRun = true
			}
			continue
		}
		inRun = false
	}
	return runs == 1
}

// looksLikeSQLID returns true when args is a bare numeric ID (no spaces,
// no SQL keywords). og 的 unique_sql_id / debug_query_id 都是大整数,
// 比 SQL 文本短得多, 全数字格式无歧义. 例: "581990336" 是 ID;
// "SELECT 1" 不是; "12+3" 不是 (含运算符).
func looksLikeSQLID(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 25 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// fetchLiteralSQLByID looks up dbe_perf.statement_history for a real
// execution sample matching unique_query_id, returning the bound SQL when
// available. Falls back to debug_query_id and then dbe_perf.statement's
// normalized SQL so /topsql IDs remain tunable even after statement_history
// expires.
//
// Why this exists: /sqltune <id> from /slowsql output is the common
// agent-mode path. Without this fallback, og's /sqltune sees a SQL_ID
// as opaque text, fails PLAN compilation, and small LLMs (35B / qwen)
// give up after a few sqlfetch / explain retries — total agent waste.
func fetchLiteralSQLByID(ctx context.Context, driver db.Driver, sqlID string) (string, error) {
	// statement_history is the canonical source with literals when
	// track_stmt_stat_level is configured (typically L1,L1 or higher).
	// Order by start_time DESC to get the most recent execution.
	q := `SELECT query FROM dbe_perf.statement_history
	       WHERE unique_query_id = :1
	         AND query IS NOT NULL
	         AND query <> ''
	       ORDER BY start_time DESC
	       LIMIT 1`
	res, err := driver.Query(ctx, q, sqlID)
	if err != nil {
		return "", fmt.Errorf("query statement_history: %w", err)
	}
	if len(res.Rows) > 0 {
		return strings.TrimSpace(toString(res.Rows[0][0])), nil
	}

	// statement_history empty — try debug_query_id (per-query ID assigned
	// even when track_stmt_stat_level=L0). Last resort before giving up.
	q2 := `SELECT query FROM dbe_perf.statement_history
	        WHERE debug_query_id = :1
	          AND query IS NOT NULL
	        ORDER BY start_time DESC
	        LIMIT 1`
	res, err = driver.Query(ctx, q2, sqlID)
	if err != nil {
		return "", fmt.Errorf("query statement_history (debug_query_id fallback): %w", err)
	}
	if len(res.Rows) > 0 {
		return strings.TrimSpace(toString(res.Rows[0][0])), nil
	}

	// statement_history empty/expired — fall back to the normalized unique SQL
	// source used by /topsql and /slowsql. Placeholders will be substituted by
	// the caller's blindNullSubstitute path before the tuner runs.
	q3 := `SELECT query FROM dbe_perf.statement
	       WHERE unique_sql_id = :1
	         AND query IS NOT NULL
	         AND query <> ''
	       LIMIT 1`
	res, err = driver.Query(ctx, q3, sqlID)
	if err != nil {
		return "", fmt.Errorf("query statement fallback: %w", err)
	}
	if len(res.Rows) == 0 {
		return "", fmt.Errorf("no statement_history/statement row found for sql_id=%s; track_stmt_stat_level may be L0 or query expired from ring buffer", sqlID)
	}
	return strings.TrimSpace(toString(res.Rows[0][0])), nil
}

// toString is a tiny value coercer for driver row values.
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}

// blindNullSubstitute (misnamed for compat — actually smart) replaces
// unbound `?` placeholders outside string literals with safe default
// values that won't break EXPLAIN. Context-aware:
//
//   - `interval ?`           → `interval '1 day'`  (NULL is rejected by og)
//   - `LIMIT ?` / `OFFSET ?` → `LIMIT 100` / `OFFSET 0`
//   - `IN (?, ?, ?)`         → `IN (0, 0, 0)`  (NULL list works but
//     integer is more representative)
//   - everywhere else        → `0`  (universally safe — works in equality,
//     comparison, expression contexts)
//
// String literals preserved — ? inside 'who?' or "what?" not replaced.
//
// Why this matters: og's existing PlaceholderSubstituter is good at
// common patterns but leaves a tail of `?` unsubstituted (LIMIT, OFFSET,
// edge expressions). With this final pass, EXPLAIN succeeds → plan tree
// → LLM gets 5 candidates → no agent early-stop.
//
// Plan is approximate (synthetic values, not real binds) but cbo
// reasoning is still useful — DBA + LLM diagnose access path / join
// algo choices on the resulting plan.
func blindNullSubstitute(sql string) string {
	var b strings.Builder
	b.Grow(len(sql) + 50)
	inSingle, inDouble := false, false
	// Track lower-cased recent context for keyword detection.
	// Cheap: just scan back up to 12 chars when we hit a ?.
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case c == '\\' && i+1 < len(sql):
			b.WriteByte(c)
			b.WriteByte(sql[i+1])
			i++
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			b.WriteByte(c)
		case c == '"' && !inSingle:
			inDouble = !inDouble
			b.WriteByte(c)
		case inSingle || inDouble:
			b.WriteByte(c)
		case c == '?':
			b.WriteString(substituteForQmark(sql, i))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// substituteForQmark picks a safe replacement value for `?` at position
// pos in sql based on left-context keyword detection.
func substituteForQmark(sql string, pos int) string {
	// Scan back to find preceding word (skip spaces, parens, commas).
	end := pos
	// Skip whitespace + open paren + comma immediately before ?
	for end > 0 && (sql[end-1] == ' ' || sql[end-1] == '\t' || sql[end-1] == '\n' || sql[end-1] == '(' || sql[end-1] == ',') {
		end--
	}
	// Extract preceding word
	start := end
	for start > 0 {
		ch := sql[start-1]
		isAlpha := (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_'
		if !isAlpha {
			break
		}
		start--
	}
	if start == end {
		return "0" // no preceding word
	}
	word := strings.ToLower(sql[start:end])

	switch word {
	case "interval":
		return "'1 day'"
	case "limit":
		return "100"
	case "offset":
		return "0"
	case "like", "ilike":
		return "'%'"
	}
	return "0" // safe default for numeric/comparison contexts
}

// containsPlaceholders returns true when sql contains unbound `?` /
// `$N` / `:N` placeholders outside string literals. Used to decide
// whether the statement_history-fetched SQL is normalized vs has
// literals. Defensive: bails out on first detection.
func containsPlaceholders(sql string) bool {
	inSingle, inDouble := false, false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case c == '\\' && i+1 < len(sql):
			i++ // skip escaped char
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle || inDouble:
			// inside string literal — skip
		case c == '?':
			return true
		case c == '$' && i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9':
			return true
		case c == ':' && i+1 < len(sql):
			next := sql[i+1]
			// `:N` numeric bind or `:identifier` named bind
			if (next >= '0' && next <= '9') || (next >= 'A' && next <= 'Z') ||
				(next >= 'a' && next <= 'z') || next == '_' {
				// skip `::` (PG cast, but og 5.0 doesn't use these in normalized SQL)
				if next != ':' {
					return true
				}
			}
		}
	}
	return false
}

// truncForDisplay truncates sql to maxLen chars with " ..." suffix
// when longer. Used in error messages — keep output bounded so terminal
// doesn't get flooded by a 10000-char SQL.
func truncForDisplay(sql string, maxLen int) string {
	sql = strings.TrimSpace(sql)
	if len(sql) <= maxLen {
		return sql
	}
	return sql[:maxLen] + " ..."
}
