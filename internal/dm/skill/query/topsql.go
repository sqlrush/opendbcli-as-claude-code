/*-------------------------------------------------------------------------
 *
 * topsql.go
 *	  topsql — TopSQLSkill plus helpers (NewTopSQLSkill) used by the
 *	  query package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/query/topsql.go
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

// TopSQL: V$SQL_HISTORY 按执行次数 GROUP BY SQL_ID 聚合 TOP 20
//
// V$SQL_HISTORY 是单次执行历史（每次执行一行），需要 GROUP BY SQL_ID 才能拿
// 总执行次数 / 平均时间。SQL 文本取每组里第一条 (MIN 聚合).
const topSQLByCallsSQL = `SELECT
    SQL_ID,
    COUNT(*) AS EXEC_COUNT,
    SUM(TIME_USED) AS TOTAL_TIME_MS,
    ROUND(AVG(TIME_USED), 2) AS AVG_TIME_MS,
    SUBSTR(MIN(TOP_SQL_TEXT), 1, 100) AS SAMPLE_SQL
FROM V$SQL_HISTORY
WHERE SQL_ID IS NOT NULL
GROUP BY SQL_ID
ORDER BY EXEC_COUNT DESC
LIMIT 20`

type TopSQLSkill struct{ driver db.Driver }

func NewTopSQLSkill(driver db.Driver) *TopSQLSkill { return &TopSQLSkill{driver: driver} }

func (s *TopSQLSkill) Name() string                       { return "topsql" }
func (s *TopSQLSkill) Description() string                { return "Top SQL by exec count (V$SQL_HISTORY)" }
func (s *TopSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *TopSQLSkill) Validate(_ skill.Params) error      { return nil }
func (s *TopSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "topsql", Description: "Top DM SQL by execution count"}
}
func (s *TopSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "topsql", Usage: "/topsql"}
}

func (s *TopSQLSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, topSQLByCallsSQL)
	if err != nil {
		return nil, fmt.Errorf("dm topsql: %w", err)
	}

	// SQL_ID(0), EXEC_COUNT(1), TOTAL_TIME_MS(2), AVG_TIME_MS(3), SAMPLE_SQL(4)
	entries := []dmutil.SummaryEntry{
		{Key: "unique_sql_count", Val: len(r.Rows)},
	}
	if len(r.Rows) > 0 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "hottest_sql_id", Val: r.Rows[0][0]},
			dmutil.SummaryEntry{Key: "hottest_exec_count", Val: r.Rows[0][1]},
			dmutil.SummaryEntry{Key: "hottest_total_time_ms", Val: r.Rows[0][2]},
			dmutil.SummaryEntry{Key: "hottest_avg_time_ms", Val: r.Rows[0][3]},
			dmutil.SummaryEntry{Key: "hottest_sql_sample", Val: r.Rows[0][4]},
		)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("Top SQL — %d 条", len(r.Rows)),
	}, nil
}
