/*-------------------------------------------------------------------------
 *
 * sqlfetch_skill.go
 *	  Oracle SQLFetchSkill — resolves a SQL_ID (varchar2(13)) to the SQL
 *	  text in V$SQL / V$SQLAREA. Unlike PG/MySQL/og, Oracle's V$SQL stores
 *	  literal SQL by default (no normalization unless cursor_sharing=FORCE),
 *	  so this skill is the simplest — usually returns ready-to-EXPLAIN SQL.
 *
 *	  Tries V$SQL.SQL_FULLTEXT first (preserves trailing whitespace and
 *	  full text), falls back to V$SQL.SQL_TEXT (truncated to 1000 chars,
 *	  always available).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/sqlfetch_skill.go
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

// SQLFetchSkill resolves an Oracle SQL_ID to V$SQL.SQL_FULLTEXT (full literal
// SQL). Unique among the four supported DBs because Oracle stores literal SQL
// by default — only when cursor_sharing=FORCE/SIMILAR are bind variables
// substituted (and even then sometimes preserved). Result is usually
// ready-to-EXPLAIN.
type SQLFetchSkill struct{ driver db.Driver }

func NewSQLFetchSkill(driver db.Driver) *SQLFetchSkill { return &SQLFetchSkill{driver: driver} }

func (s *SQLFetchSkill) Name() string                       { return "sqlfetch" }
func (s *SQLFetchSkill) Description() string                { return "按 SQL_ID 拉取 SQL 全文（V$SQL.SQL_FULLTEXT，通常带字面量）" }
func (s *SQLFetchSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLFetchSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqlfetch",
		Description: "Resolve an Oracle SQL_ID (13-char varchar2, from /slowsql or /topsql) to its SQL text. " +
			"Oracle's V$SQL stores literal SQL by default — output is usually directly usable. " +
			"Use BEFORE /sqltune or /explain when the user supplies only a SQL_ID. " +
			"Tries SQL_FULLTEXT first; falls back to SQL_TEXT (truncated 1000 chars).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Oracle SQL_ID (e.g. 'a3b4c5d6e7f8g'). May include trailing words.",
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
		Description: "按 SQL_ID 拉取 SQL（V$SQL.SQL_FULLTEXT 带字面量）",
		Examples: []string{
			"/sqlfetch a3b4c5d6e7f8g",
			"/sqlfetch SQL_ID a3b4c5d6e7f8g",
		},
	}
}

func (s *SQLFetchSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("需要提供 SQL_ID")
	}
	return nil
}

func (s *SQLFetchSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	id := normalizeOraSQLID(strings.TrimSpace(params.StringOr("args", "")))
	if id == "" {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqlfetch 失败: empty SQL_ID after normalization",
			Summary:  "empty sql_id",
		}, nil
	}

	// Pull SQL_FULLTEXT (CLOB), parsing_schema_name, and execution stats.
	// V$SQL has one row per child cursor; we take the most-executed one.
	q := `SELECT parsing_schema_name, sql_fulltext, executions, ROUND(elapsed_time/GREATEST(executions,1)/1000, 2) avg_ms
            FROM v$sql
           WHERE sql_id = :1
           ORDER BY executions DESC
           FETCH FIRST 1 ROWS ONLY`
	res, err := s.driver.Query(ctx, q, id)
	if err != nil {
		// Some grants only expose v$sqlarea — try that as fallback.
		q2 := `SELECT parsing_schema_name, sql_fulltext, executions, ROUND(elapsed_time/GREATEST(executions,1)/1000, 2) avg_ms
                FROM v$sqlarea
               WHERE sql_id = :1
               FETCH FIRST 1 ROWS ONLY`
		res, err = s.driver.Query(ctx, q2, id)
		if err != nil {
			return &skill.Result{
				Type:     skill.ResultError,
				Rendered: "  /sqlfetch 失败: " + err.Error() + "\n  注意: 需要 SELECT_CATALOG_ROLE 或对 V$SQL/V$SQLAREA 的直接 SELECT 权限",
				Summary:  err.Error(),
			}, nil
		}
	}
	if res == nil || len(res.Rows) == 0 {
		return &skill.Result{
			Type: skill.ResultText,
			Rendered: fmt.Sprintf(
				"  ⚠️ 找不到 SQL_ID %s 对应的 SQL\n"+
					"     可能原因：\n"+
					"       1. SQL_ID 拼写错误 — 用 /slowsql 或 /topsql 重新核对\n"+
					"       2. 该 SQL 已从 shared pool 淘汰（V$SQL 是内存视图，重启 / FLUSH SHARED_POOL 后清空）\n"+
					"     建议：从 DBA_HIST_SQLTEXT (AWR) 找历史快照，或让用户提供 SQL 全文。",
				id),
			Summary: fmt.Sprintf("SQL_ID %s not found in V$SQL", id),
		}, nil
	}

	schema := oraStrCell(res.Rows[0][0])
	query := oraStrCell(res.Rows[0][1])
	ph := oraCountPlaceholders(query)
	// Detect cursor_sharing-induced bind substitution (e.g. SYS_B_0, SYS_B_1).
	hasBinds := strings.Contains(query, "SYS_B_") || strings.Contains(query, ":B")

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  ✓ /sqlfetch %s 命中（来源：V$SQL）\n\n", id))
	if schema != "" {
		b.WriteString(fmt.Sprintf("  parsing_schema: %s\n", schema))
	}
	if ph == 0 && !hasBinds {
		b.WriteString("  状态: ✅ 含字面量，可直接喂给 /sqltune 或 /explain\n")
	} else if hasBinds {
		b.WriteString("  状态: ⚠️ 含 SYS_B_n 绑定变量（cursor_sharing=FORCE/SIMILAR 替换）\n")
		b.WriteString("       绑定变量值不可见，EXPLAIN 仍可跑（Oracle 自动 peek），但执行计划可能因 bind peeking 不同\n")
	} else {
		b.WriteString(fmt.Sprintf("  状态: ⚠️ 含 %d 个 :N / ? 占位符（应用代码里的绑定变量），需替换样例值\n", ph))
	}

	b.WriteString("\n  --- SQL ---\n\n")
	if schema != "" {
		b.WriteString(fmt.Sprintf("  -- 注意: 原会话 parsing_schema = %s\n", schema))
		b.WriteString(fmt.Sprintf("  -- 如裸表名 EXPLAIN 失败, 在表名前加 %s. 即可\n", schema))
	}
	b.WriteString(query)
	if !strings.HasSuffix(query, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n  --- 下一步 ---\n")
	if ph == 0 && !hasBinds {
		b.WriteString("\n  /sqltune <把上面 SQL 完整粘贴进来>\n")
	} else {
		b.WriteString("\n  替换占位符 / 绑定变量为真实值后, /sqltune <SQL>\n")
	}

	status := "literal"
	if hasBinds {
		status = "cursor_sharing binds"
	} else if ph > 0 {
		status = fmt.Sprintf("%d placeholders", ph)
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("SQL_ID %s resolved (schema=%s, %s)", id, schema, status),
	}, nil
}

// normalizeOraSQLID strips common prefixes the LLM might paste and returns
// the bare SQL_ID. Oracle SQL_IDs are 13-char base32-like strings; we don't
// strictly validate length here because some places truncate / pad them.
func normalizeOraSQLID(s string) string {
	for _, p := range []string{"sql_id", "SQL_ID", "SQLID", "sqlid"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, p))
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, ":"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "="))
	// Take first whitespace-separated token.
	if idx := strings.IndexAny(s, " \t\n"); idx > 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func oraStrCell(v any) string {
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

// oraCountPlaceholders detects ? and :N (numeric Oracle binds), but does NOT
// flag :name (Oracle named binds) since Oracle execution can usually peek
// these. SYS_B_n (cursor_sharing) is handled separately in Execute.
func oraCountPlaceholders(sql string) int {
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
		if c == ':' && i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
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
