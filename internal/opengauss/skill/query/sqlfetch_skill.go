/*-------------------------------------------------------------------------
 *
 * sqlfetch_skill.go
 *	  SQLFetchSkill resolves a SQL_ID to an executable SQL text. Given an
 *	  og unique_sql_id (from /slowsql or /topsql), pulls the literal-bearing
 *	  SQL from dbe_perf.statement_history and returns it ready for /sqltune
 *	  or /explain.
 *
 *	  Without this skill, /llm + /sqltune routinely fail because:
 *	    - dbe_perf.statement.query is normalized (LIKE ?, etc.) — EXPLAIN
 *	      can't run it.
 *	    - the schema_name from the original session is not preserved.
 *	    - LLMs don't know og's column naming (unique_sql_id vs unique_query_id).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/sqlfetch_skill.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/opengauss/sqltuner"
	"github.com/sqlrush/opendb/internal/skill"
)

// SQLFetchSkill resolves an og SQL_ID to an executable, schema-qualified SQL.
//
// The skill exists to bridge a knowledge / tooling gap: when a user asks "how
// to optimize SQL_ID 33402943", LLMs end up burning 18+ tool calls trying to
// fish the literal-bearing SQL out of og metadata. This skill encapsulates
// that lookup with og-correct column names and returns ready-for-/sqltune
// output in a single call.
//
// Lookup precedence:
//  1. dbe_perf.statement_history with `unique_query_id = <id>` and a non-
//     placeholder query — wins because it carries actual literals AND
//     schema_name from the executing session.
//  2. dbe_perf.statement with `unique_sql_id = <id>` — only normalized form
//     is available; we return it with a clear warning so the LLM knows to
//     either substitute literals or call sqltune which will reject it.
type SQLFetchSkill struct {
	driver db.Driver
}

// NewSQLFetchSkill constructs a /sqlfetch skill.
func NewSQLFetchSkill(driver db.Driver) *SQLFetchSkill {
	return &SQLFetchSkill{driver: driver}
}

func (s *SQLFetchSkill) Name() string                       { return "sqlfetch" }
func (s *SQLFetchSkill) Description() string                { return "按 SQL_ID 拉取可 EXPLAIN 的完整 SQL（带字面量+schema），喂给 /sqltune 用" }
func (s *SQLFetchSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLFetchSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqlfetch",
		Description: "Resolve an og SQL_ID (from /slowsql or /topsql output) to the actual executable SQL text. " +
			"Looks up dbe_perf.statement_history (literals + schema preserved) first, falls back to " +
			"dbe_perf.statement (normalized) with warnings. Output is ready to feed into /sqltune. " +
			"Use this BEFORE /sqltune when the user gives only a SQL_ID — saves 18+ rounds of raw sql calls.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "SQL_ID (numeric, e.g. 33402943). May also include trailing words; only the first integer is used.",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *SQLFetchSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "sqlfetch",
		Aliases:     []string{"sqlf"},
		Usage:       "/sqlfetch <SQL_ID>",
		Description: "按 SQL_ID 拉取可 EXPLAIN 的完整 SQL（带字面量+schema）",
		Examples: []string{
			"/sqlfetch 33402943",
			"/sqlfetch 33402943   # 拿到完整 SQL 后, /sqltune <返回的 SQL>",
		},
	}
}

func (s *SQLFetchSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("需要提供 SQL_ID")
	}
	if _, err := parseSQLID(args); err != nil {
		return err
	}
	return nil
}

// fetchResult bundles the resolved SQL with metadata about how we got it. The
// metadata helps the LLM (and the user) understand whether the result is
// directly usable or whether they should still expect EXPLAIN issues.
type fetchResult struct {
	SQL          string                   // original (or substituted) SQL ready for sqltune
	OriginalSQL  string                   // (v1.1.30) raw SQL before auto-substitute, if applied
	Schema       string
	Source       string // "statement_history" | "statement" | "none"
	StartTime    string
	HasLiterals  bool
	Placeholders int
	Notes        []string
	Substituted  bool                     // (v1.1.30) auto-substitute was applied
	Subs         []sqltuner.Substitution  // (v1.1.30) details of substitutions
}

func (s *SQLFetchSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	id, err := parseSQLID(args)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqlfetch 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}

	res, err := s.lookup(ctx, id)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqlfetch 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}
	if res != nil {
		// v1.1.31: detect SQL truncation by og's track_activity_query_size.
		// Default is 1024 bytes; stored SQL >= this length is likely cut off.
		// If we deliver a truncated SQL to sqltune, it'll fail with "syntax
		// error at end of input" — confusing for the LLM. Warn the user
		// (and the LLM via prompt) so they know to either bump the GUC or
		// supply the full SQL manually.
		if looksTruncated(res.SQL) {
			res.Notes = append(res.Notes,
				fmt.Sprintf("⚠️ SQL 看起来被 og 截断（长度 %d, 末尾不完整）— GUC track_activity_query_size 默认 1024 字节，长 SQL 会被切掉。",
					len(res.SQL)),
				"修法：sudo su - omm -c \"gs_guc set ... -c 'track_activity_query_size=16384'\" 然后 gs_ctl restart 后重新执行该 SQL，再调 /sqlfetch。",
			)
		}
	}
	if res != nil && res.Placeholders > 0 {
		// v1.1.30: auto-substitute placeholders by default. The substituted
		// SQL is what we return as the primary output; the original is kept
		// in OriginalSQL for transparency. Substitution is deterministic
		// (column type + operator context) so the result is reproducible.
		substituter := sqltuner.NewPlaceholderSubstituter(s.driver)
		newSQL, subs, sErr := substituter.Substitute(ctx, res.SQL, res.Schema)
		if sErr == nil && len(subs) > 0 {
			res.OriginalSQL = res.SQL
			res.SQL = newSQL
			res.Subs = subs
			res.Substituted = true
			res.HasLiterals = true
			// Update notes: replace the "L1 模式" warnings with substitute info
			res.Notes = []string{
				fmt.Sprintf("✅ 已自动用合理样例值替换 %d 个占位符（auto-substitute / v1.1.30），SQL 现在可直接 EXPLAIN。", len(subs)),
				"⚠️ 替换值是为 EXPLAIN 跑通用的合成样例 — CBO 估算基于统计信息而非具体字面量，所以执行计划结构与真实 SQL 一致。",
			}
		}
	}
	if res == nil {
		return &skill.Result{
			Type: skill.ResultText,
			Rendered: fmt.Sprintf(
				"  ⚠️ 找不到 SQL_ID %d 对应的 SQL 文本\n"+
					"     可能原因：\n"+
					"       1. SQL_ID 拼写错误 — 用 /slowsql 或 /topsql 重新核对\n"+
					"       2. 该 SQL 已从 dbe_perf.statement / statement_history 淘汰\n"+
					"       3. instr_unique_sql_count 缓冲区不够，原 SQL 文本未保留\n"+
					"     建议：让用户提供 SQL 全文，再用 /sqltune <SQL> 调优。",
				id),
			Summary: fmt.Sprintf("SQL_ID %d not found", id),
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: renderFetchResult(id, res),
		Summary:  summarizeFetchResult(id, res),
	}, nil
}

// ResolvedSQL is the structured output of programmatic SQL resolution.
// Used by /wdranalyze and other internal callers that need raw access
// without going through the skill.Result formatting layer.
type ResolvedSQL struct {
	SQL          string // post-substitute, ready for /sqltune
	OriginalSQL  string // pre-substitute (if substitution applied)
	Schema       string // schema_name from dbe_perf.statement_history
	Source       string // "statement_history" / "statement"
	HasLiterals  bool   // true if SQL is ready to EXPLAIN (no ? / $N)
	Substituted  bool   // true if auto-substitute filled in literals
	Placeholders int
	Notes        []string
}

// Resolve is the programmatic entry point for /sqlfetch's core logic.
// Returns nil result + nil error if the SQL_ID is not found (not an
// error — caller should check for nil). Designed for wdranalyze.Drill
// to use without going through the skill.Result layer.
func (s *SQLFetchSkill) Resolve(ctx context.Context, sqlID int64) (*ResolvedSQL, error) {
	res, err := s.lookup(ctx, sqlID)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}

	// Apply auto-substitute (v1.1.30): hand off normalized SQL to substituter
	// so /sqltune downstream gets ready-for-EXPLAIN text. Same logic as in
	// Execute() but without the skill.Result wrapping.
	if res.Placeholders > 0 {
		substituter := sqltuner.NewPlaceholderSubstituter(s.driver)
		newSQL, subs, sErr := substituter.Substitute(ctx, res.SQL, res.Schema)
		if sErr == nil && len(subs) > 0 {
			res.OriginalSQL = res.SQL
			res.SQL = newSQL
			res.Substituted = true
			res.HasLiterals = true
		}
	}

	return &ResolvedSQL{
		SQL:          res.SQL,
		OriginalSQL:  res.OriginalSQL,
		Schema:       res.Schema,
		Source:       res.Source,
		HasLiterals:  res.HasLiterals,
		Substituted:  res.Substituted,
		Placeholders: res.Placeholders,
		Notes:        res.Notes,
	}, nil
}

// lookup runs the two-stage resolution: statement_history (preferred) →
// statement (fallback). Returns nil if both miss.
func (s *SQLFetchSkill) lookup(ctx context.Context, id int64) (*fetchResult, error) {
	// Stage 1: statement_history. Filter out the "missing SQL statement"
	// placeholder string that og emits when the instr_unique_sql_count buffer
	// overflowed. statement_history *can* contain literals if og is configured
	// with track_stmt_stat_level='L2,L2' or higher — but most production
	// installs run L1 for overhead reasons, so we still need to check the
	// returned text for `?` placeholders rather than trust the source.
	histSQL := `SELECT schema_name, query, start_time
                FROM dbe_perf.statement_history
                WHERE unique_query_id = $1
                  AND query NOT LIKE '/* missing SQL statement%'
                ORDER BY start_time DESC
                LIMIT 1`
	histRes, err := s.driver.Query(ctx, histSQL, id)
	if err == nil && histRes != nil && len(histRes.Rows) > 0 {
		schema := strOrEmpty(histRes.Rows[0][0])
		query := strOrEmpty(histRes.Rows[0][1])
		start := strOrEmpty(histRes.Rows[0][2])
		if query != "" {
			ph := countPlaceholders(query)
			r := &fetchResult{
				SQL:          query,
				Schema:       schema,
				Source:       "statement_history",
				StartTime:    start,
				HasLiterals:  ph == 0,
				Placeholders: ph,
			}
			if ph > 0 {
				r.Notes = append(r.Notes,
					fmt.Sprintf("⚠️ 含 %d 个 ? 占位符 — og 当前以 L1 模式跟踪，字面量已被归一化。", ph),
					"如需带字面量的版本：su - omm -c \"gs_guc set -D <data_dir> -c \\\"track_stmt_stat_level = 'L2,L2'\\\"\" 然后重启 og（仅诊断期，L2 有少量性能开销）。",
					"或手动把 ? 替换成代表性样例值（来自 /slowsql 的 ROWS、AVG_MS 大致猜测），再传 /sqltune。",
				)
			}
			return r, nil
		}
	}

	// Stage 2: statement (normalized only).
	stmtSQL := `SELECT query FROM dbe_perf.statement WHERE unique_sql_id = $1 LIMIT 1`
	stmtRes, err2 := s.driver.Query(ctx, stmtSQL, id)
	if err2 == nil && stmtRes != nil && len(stmtRes.Rows) > 0 {
		query := strOrEmpty(stmtRes.Rows[0][0])
		if query != "" {
			ph := countPlaceholders(query)
			r := &fetchResult{
				SQL:          query,
				Source:       "statement",
				HasLiterals:  false,
				Placeholders: ph,
				Notes: []string{
					"⚠️ 这是归一化后的 SQL（字面量已替换为 ?），无法直接 EXPLAIN。",
					"建议把 ? 替换成样例值后再传 /sqltune，或先增大 instr_unique_sql_count 让 statement_history 重新捕获带字面量版本。",
				},
			}
			return r, nil
		}
	}

	return nil, nil
}

// parseSQLID extracts the first integer from the args string. We accept
// "33402943", "SQL_ID 33402943", "33402943 (slow)" and similar forms — LLMs
// sometimes pass a mix of label + number.
func parseSQLID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	// Trim common label prefixes the LLM might paste.
	for _, p := range []string{"sql_id", "SQL_ID", "sqlid", "SQLID", "id"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, p))
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, ":"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "="))
	// Find the first run of digits.
	start := -1
	end := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			if start == -1 {
				start = i
			}
			end = i + 1
		} else if start != -1 {
			break
		}
	}
	if start == -1 {
		return 0, fmt.Errorf("invalid SQL_ID %q (expected integer)", s)
	}
	var id int64
	if _, err := fmt.Sscanf(s[start:end], "%d", &id); err != nil {
		return 0, fmt.Errorf("invalid SQL_ID %q: %w", s[start:end], err)
	}
	return id, nil
}

// countPlaceholders counts occurrences of ? / $N / :N — used only for the
// metadata banner; the canonical implementation lives in
// internal/opengauss/sqltuner/plan_collector.go (detectPlaceholders) and is
// invoked by /sqltune when this SQL is later piped into it.
func countPlaceholders(sql string) int {
	count := 0
	inStr := false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if c == '\'' {
			// Toggle, but handle '' as escape.
			if inStr && i+1 < len(sql) && sql[i+1] == '\'' {
				i++
				continue
			}
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		if c == '?' {
			count++
		}
	}
	return count
}

func strOrEmpty(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func renderFetchResult(id int64, r *fetchResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("  ✓ /sqlfetch %d 命中（来源：dbe_perf.%s）\n\n", id, r.Source))
	if r.Schema != "" {
		b.WriteString(fmt.Sprintf("  schema: %s\n", r.Schema))
	}
	if r.StartTime != "" {
		b.WriteString(fmt.Sprintf("  最近执行时间: %s\n", r.StartTime))
	}
	if r.HasLiterals {
		b.WriteString("  状态: ✅ 含字面量，可直接喂给 /sqltune\n")
	} else {
		b.WriteString(fmt.Sprintf("  状态: ⚠️ 归一化版本（含 %d 个 ? 占位符），不可直接 EXPLAIN\n", r.Placeholders))
	}
	for _, note := range r.Notes {
		b.WriteString("  " + note + "\n")
	}
	if r.Substituted {
		b.WriteString(sqltuner.FormatSubstitutions(r.Subs))
	}
	b.WriteString("\n  --- SQL ---\n\n")
	if r.Schema != "" && r.HasLiterals {
		b.WriteString(fmt.Sprintf("  -- 注意: 原会话 search_path = %s\n", r.Schema))
		b.WriteString(fmt.Sprintf("  -- 如裸表名 EXPLAIN 失败, 在表名前加 %s. 即可\n", r.Schema))
	}
	b.WriteString(r.SQL)
	if !strings.HasSuffix(r.SQL, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n  --- 下一步 ---\n")
	if r.HasLiterals {
		b.WriteString("\n  /sqltune <把上面 SQL 完整粘贴进来>\n")
	} else {
		b.WriteString("\n  把上面 SQL 中的 ? 替换成实际值后再调 /sqltune\n")
	}
	return b.String()
}

func summarizeFetchResult(id int64, r *fetchResult) string {
	if r.HasLiterals {
		return fmt.Sprintf("SQL_ID %d resolved (schema=%s, source=%s, with literals, ready for /sqltune)",
			id, r.Schema, r.Source)
	}
	return fmt.Sprintf("SQL_ID %d resolved (source=%s, normalized form with %d placeholders, NOT ready for /sqltune)",
		id, r.Source, r.Placeholders)
}

// looksTruncated detects SQL that was likely cut off by og's
// track_activity_query_size GUC (default 1024 bytes). Heuristics:
//
//	(a) length is suspiciously close to power-of-2 boundary (1024 / 2048 / etc)
//	(b) ends with SQL keyword like "FROM" / "WHERE" suggesting continuation
//	(c) ends mid-keyword (e.g. "FRO" cut from "FROM")
//
// Returns true if at least 2 heuristics fire.
func looksTruncated(sql string) bool {
	sql = strings.TrimRight(sql, " \t\n;")
	n := len(sql)
	if n == 0 {
		return false
	}
	hits := 0
	for _, boundary := range []int{1024, 2048, 4096, 8192, 16384} {
		if n >= boundary-8 && n <= boundary {
			hits++
			break
		}
	}
	lower := strings.ToLower(sql)
	for _, kw := range []string{
		" from", " where", " select", " and", " or", " join",
		" on", " in", " group by", " order by", " having", " limit",
	} {
		if strings.HasSuffix(lower, kw) {
			hits += 2
			break
		}
	}
	last := sql[n-1]
	if (last >= 'a' && last <= 'z') || (last >= 'A' && last <= 'Z') ||
		(last >= '0' && last <= '9') || last == '_' {
		w := lastWordOfSQL(sql)
		upper := strings.ToUpper(w)
		for _, kw := range []string{"FROM", "SELECT", "WHERE", "JOIN", "GROUP", "ORDER", "HAVING", "LIMIT", "INNER", "OUTER", "LEFT", "RIGHT"} {
			if strings.HasPrefix(kw, upper) && len(upper) < len(kw) && len(upper) >= 3 {
				hits += 2
				break
			}
		}
	}
	return hits >= 2
}

func lastWordOfSQL(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '(' || c == ',' {
			return s[i+1:]
		}
	}
	return s
}
