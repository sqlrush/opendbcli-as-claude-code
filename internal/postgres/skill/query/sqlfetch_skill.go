/*-------------------------------------------------------------------------
 *
 * sqlfetch_skill.go
 *	  PG SQLFetchSkill — resolves a queryid (from /slowsql or /topsql) to
 *	  the SQL text in pg_stat_statements. PG always stores normalized form
 *	  (literals replaced with $N), so the output requires the LLM (or user)
 *	  to substitute literals before EXPLAIN. We make that requirement
 *	  explicit so calls don't burn rounds rediscovering the limitation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/query/sqlfetch_skill.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// SQLFetchSkill resolves a PG queryid to the SQL text recorded by
// pg_stat_statements. Unlike og's dbe_perf.statement_history (which can
// optionally preserve literals), pg_stat_statements *always* normalizes —
// so this skill's job is mainly:
//   - encapsulate column-name knowledge (queryid is bigint, not "query_id"
//     and not "sql_id")
//   - return the SQL with a clear placeholder warning
//   - point the LLM toward auto_explain or an explicit literal SQL retry
type SQLFetchSkill struct{ driver db.Driver }

func NewSQLFetchSkill(driver db.Driver) *SQLFetchSkill { return &SQLFetchSkill{driver: driver} }

func (s *SQLFetchSkill) Name() string                       { return "sqlfetch" }
func (s *SQLFetchSkill) Description() string                { return "按 queryid 拉取 SQL 文本（pg_stat_statements），带占位符提示" }
func (s *SQLFetchSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLFetchSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqlfetch",
		Description: "Resolve a PostgreSQL queryid (bigint, from /slowsql or /topsql) to its SQL text. " +
			"PG's pg_stat_statements always stores normalized form (literals → $N), so the returned SQL " +
			"will have placeholders — the caller must substitute realistic values before EXPLAIN. " +
			"Use BEFORE /sqltune or /explain when the user supplies only a queryid.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "queryid (bigint, e.g. -1832049281). May include trailing words; first integer used.",
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
		Usage:       "/sqlfetch <queryid>",
		Description: "按 queryid 拉取 SQL（pg_stat_statements 归一化版本）",
		Examples: []string{
			"/sqlfetch -1832049281",
			"/sqlfetch 1234567890",
		},
	}
}

func (s *SQLFetchSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("需要提供 queryid")
	}
	if _, err := parseQueryID(args); err != nil {
		return err
	}
	return nil
}

func (s *SQLFetchSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	id, err := parseQueryID(args)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqlfetch 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}

	// pg_stat_statements ALWAYS stores normalized form. We also try to find
	// the database name to help the user reconstruct context.
	row := `SELECT d.datname, s.query, s.calls, ROUND(s.mean_exec_time::numeric, 2)
            FROM pg_stat_statements s
            JOIN pg_database d ON d.oid = s.dbid
            WHERE s.queryid = $1
            LIMIT 1`
	res, qerr := s.driver.Query(ctx, row, id)
	if qerr != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqlfetch 失败: " + qerr.Error() + "\n  注意: 需要 pg_stat_statements 扩展启用 (CREATE EXTENSION pg_stat_statements;)",
			Summary:  qerr.Error(),
		}, nil
	}
	if res == nil || len(res.Rows) == 0 {
		return &skill.Result{
			Type: skill.ResultText,
			Rendered: fmt.Sprintf(
				"  ⚠️ 找不到 queryid %d 对应的 SQL\n"+
					"     可能原因：\n"+
					"       1. queryid 拼写错误 — 用 /slowsql 或 /topsql 重新核对\n"+
					"       2. 该 SQL 已从 pg_stat_statements 淘汰（pg_stat_statements.max 缓冲区被覆盖）\n"+
					"     建议：让用户提供 SQL 全文，再用 /explain 或人工分析。",
				id),
			Summary: fmt.Sprintf("queryid %d not found", id),
		}, nil
	}

	dbname := pgStrCell(res.Rows[0][0])
	query := pgStrCell(res.Rows[0][1])
	ph := pgCountPlaceholders(query)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  ✓ /sqlfetch %d 命中（来源：pg_stat_statements）\n\n", id))
	if dbname != "" {
		b.WriteString(fmt.Sprintf("  database: %s\n", dbname))
	}
	b.WriteString(fmt.Sprintf("  状态: ⚠️ 归一化版本（含 %d 个 $N 占位符），不可直接 EXPLAIN\n", ph))
	b.WriteString("  ⚠️ pg_stat_statements 不存字面量 — 这是 PG 设计，没有 og 的 L2 跟踪模式可切换。\n")
	b.WriteString("     方案：\n")
	b.WriteString("       1. 启用 auto_explain 拿到带字面量的 SQL（log_min_duration > 0）\n")
	b.WriteString("       2. 等待用户提供 SQL 全文，或从应用日志找原始 SQL\n")
	b.WriteString("       3. 把 $N 替换成代表性样例值（基于 SQL 语义猜）后再传 /explain\n")

	b.WriteString("\n  --- SQL ---\n\n")
	b.WriteString(query)
	if !strings.HasSuffix(query, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n  --- 下一步 ---\n")
	b.WriteString("\n  替换 $N 为真实值后, /explain <修复后的 SQL>\n")

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("queryid %d resolved (db=%s, normalized form with %d placeholders)", id, dbname, ph),
	}, nil
}

// parseQueryID extracts a signed integer from input. PG queryid is int64
// and CAN be negative (it's a hash output). Accept "-1832049281",
// "queryid -1832049281", "id: 1234567890" etc.
func parseQueryID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, p := range []string{"queryid", "QueryID", "QUERYID", "query_id", "id"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, p))
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, ":"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "="))
	// Find first signed-int run.
	start := -1
	end := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c == '-' && start == -1 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9') ||
			(c >= '0' && c <= '9') {
			if start == -1 {
				start = i
			}
			end = i + 1
		} else if start != -1 {
			break
		}
	}
	if start == -1 {
		return 0, fmt.Errorf("invalid queryid %q (expected integer, possibly negative)", s)
	}
	var id int64
	if _, err := fmt.Sscanf(s[start:end], "%d", &id); err != nil {
		return 0, fmt.Errorf("invalid queryid %q: %w", s[start:end], err)
	}
	return id, nil
}

// pgStrCell normalizes Query result cells to string.
func pgStrCell(v any) string {
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

// pgCountPlaceholders counts ?, $N occurrences outside string literals.
// Used for the metadata banner; we don't reject the SQL — caller decides
// what to do with it.
func pgCountPlaceholders(sql string) int {
	count := 0
	inStr := false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if c == '\'' {
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
			continue
		}
		if c == '$' && i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
			count++
			j := i + 1
			for j < len(sql) && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			i = j - 1
		}
	}
	return count
}
