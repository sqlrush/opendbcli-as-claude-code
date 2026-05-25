/*-------------------------------------------------------------------------
 *
 * sqlfetch_skill.go
 *	  MySQL SQLFetchSkill — resolves a DIGEST hash (from /slowsql or /topsql
 *	  output) to the DIGEST_TEXT in performance_schema.
 *	  events_statements_summary_by_digest. MySQL always normalizes (literals
 *	  become ? marks), so this skill is mostly column-name encapsulation
 *	  and explicit placeholder warning.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/query/sqlfetch_skill.go
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

// SQLFetchSkill resolves a MySQL DIGEST hash to the DIGEST_TEXT recorded by
// performance_schema. Encapsulates:
//   - DIGEST is a varchar/binary hash (e.g. "abc123def..."), NOT a queryid bigint
//   - DIGEST_TEXT always has ? placeholders (literals stripped at parse time)
//   - sys.x$statement_analysis offers DIGEST_TEXT + first_seen / last_seen as
//     an alternative source with the same normalized form
type SQLFetchSkill struct{ driver db.Driver }

func NewSQLFetchSkill(driver db.Driver) *SQLFetchSkill { return &SQLFetchSkill{driver: driver} }

func (s *SQLFetchSkill) Name() string                       { return "sqlfetch" }
func (s *SQLFetchSkill) Description() string                { return "按 DIGEST 拉取 SQL 文本（performance_schema.events_statements_summary_by_digest）" }
func (s *SQLFetchSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLFetchSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqlfetch",
		Description: "Resolve a MySQL DIGEST (hex string from /slowsql or /topsql) to its SQL text. " +
			"MySQL's events_statements_summary_by_digest always stores normalized form (literals → ?), " +
			"so the returned SQL has placeholders — caller must substitute realistic values before EXPLAIN. " +
			"Use BEFORE /explain when the user supplies only a DIGEST.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "DIGEST hash (hex string, e.g. 'abc123def...'), or DIGEST prefix to do a LIKE search.",
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
		Usage:       "/sqlfetch <DIGEST>",
		Description: "按 DIGEST 拉取 SQL（performance_schema 归一化版本）",
		Examples: []string{
			"/sqlfetch abc123def456",
			"/sqlfetch abc123  # prefix LIKE 搜索",
		},
	}
}

func (s *SQLFetchSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("需要提供 DIGEST")
	}
	return nil
}

func (s *SQLFetchSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	digest := strings.TrimSpace(params.StringOr("args", ""))
	digest = strings.TrimSpace(strings.TrimPrefix(digest, "DIGEST"))
	digest = strings.TrimSpace(strings.TrimPrefix(digest, "digest"))
	digest = strings.TrimSpace(strings.TrimPrefix(digest, ":"))
	if digest == "" {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqlfetch 失败: empty DIGEST",
			Summary:  "empty digest",
		}, nil
	}

	// Try exact match first; if not found and digest looks like a prefix (< 32 chars
	// or non-hex chars at boundary), retry with LIKE.
	exact := `SELECT SCHEMA_NAME, DIGEST_TEXT, COUNT_STAR, ROUND(AVG_TIMER_WAIT/1000000, 2) AS avg_ms
              FROM performance_schema.events_statements_summary_by_digest
              WHERE DIGEST = ?
              LIMIT 1`
	res, err := s.driver.Query(ctx, exact, digest)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqlfetch 失败: " + err.Error() + "\n  注意: 需要 performance_schema 启用且 events_statements_summary_by_digest 有数据。",
			Summary:  err.Error(),
		}, nil
	}
	if res == nil || len(res.Rows) == 0 {
		// Retry with prefix LIKE
		like := `SELECT SCHEMA_NAME, DIGEST_TEXT, COUNT_STAR, ROUND(AVG_TIMER_WAIT/1000000, 2) AS avg_ms
                FROM performance_schema.events_statements_summary_by_digest
                WHERE DIGEST LIKE ?
                ORDER BY COUNT_STAR DESC
                LIMIT 1`
		res, err = s.driver.Query(ctx, like, digest+"%")
		if err != nil || res == nil || len(res.Rows) == 0 {
			return &skill.Result{
				Type: skill.ResultText,
				Rendered: fmt.Sprintf(
					"  ⚠️ 找不到 DIGEST %q 对应的 SQL\n"+
						"     可能原因：\n"+
						"       1. DIGEST 拼写错误 — 用 /slowsql 或 /topsql 重新核对\n"+
						"       2. events_statements_summary_by_digest 已 truncate 或 SQL 已淘汰\n"+
						"       3. performance_schema 未启用对应 instrument\n"+
						"     建议：让用户提供 SQL 全文，再用 /explain 分析。",
					digest),
				Summary: fmt.Sprintf("DIGEST %s not found", digest),
			}, nil
		}
	}

	schema := myStrCell(res.Rows[0][0])
	query := myStrCell(res.Rows[0][1])
	ph := myCountPlaceholders(query)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  ✓ /sqlfetch %s 命中（来源：performance_schema.events_statements_summary_by_digest）\n\n", digest))
	if schema != "" {
		b.WriteString(fmt.Sprintf("  schema: %s\n", schema))
	}
	b.WriteString(fmt.Sprintf("  状态: ⚠️ 归一化版本（含 %d 个 ? 占位符），不可直接 EXPLAIN\n", ph))
	b.WriteString("  ⚠️ MySQL events_statements_summary_by_digest 不存字面量 — 这是 MySQL 设计。\n")
	b.WriteString("     方案：\n")
	b.WriteString("       1. 启用 slow query log（log_slow_queries）拿到带字面量 SQL\n")
	b.WriteString("       2. 启用 general_log（注意性能开销）\n")
	b.WriteString("       3. 把 ? 替换成代表性样例值后再传 /explain\n")

	b.WriteString("\n  --- DIGEST_TEXT ---\n\n")
	b.WriteString(query)
	if !strings.HasSuffix(query, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n  --- 下一步 ---\n")
	b.WriteString("\n  替换 ? 为真实值后, /explain <修复后的 SQL>\n")

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("DIGEST %s resolved (schema=%s, normalized form with %d placeholders)", digest, schema, ph),
	}, nil
}

func myStrCell(v any) string {
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

func myCountPlaceholders(sql string) int {
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
		}
	}
	return count
}
