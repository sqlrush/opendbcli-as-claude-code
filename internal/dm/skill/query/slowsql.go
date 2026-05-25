/*-------------------------------------------------------------------------
 *
 * slowsql.go
 *	  slowsql — SlowSQLSkill plus helpers (NewSlowSQLSkill) used by
 *	  the query package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/query/slowsql.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// SlowSQL: 当前正在执行的长 SQL (V$LONG_EXEC_SQLS)
// 字段名未真机校验, 用 SELECT * 兜底, Phase 1 优化时收紧.
const slowSQL = `SELECT * FROM V$LONG_EXEC_SQLS LIMIT 30`

type SlowSQLSkill struct{ driver db.Driver }

func NewSlowSQLSkill(driver db.Driver) *SlowSQLSkill { return &SlowSQLSkill{driver: driver} }

func (s *SlowSQLSkill) Name() string                       { return "slowsql" }
func (s *SlowSQLSkill) Description() string                { return "当前长 SQL (V$LONG_EXEC_SQLS)" }
func (s *SlowSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SlowSQLSkill) Validate(_ skill.Params) error      { return nil }
func (s *SlowSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "slowsql", Description: "Show currently long-running DM SQL"}
}
func (s *SlowSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "slowsql", Usage: "/slowsql"}
}

func (s *SlowSQLSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, slowSQL)
	if err != nil {
		return nil, fmt.Errorf("dm slowsql: %w", err)
	}
	if len(r.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无长 SQL\n[summary]\nlong_sql_count: 0\n",
		}, nil
	}
	entries := []dmutil.SummaryEntry{{Key: "long_sql_count", Val: len(r.Rows)}}
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("长 SQL — %d 条", len(r.Rows)),
	}, nil
}
