/*-------------------------------------------------------------------------
 *
 * longtx.go
 *	  LongTxSkill shows long-running transactions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/longtx.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// longtxSQL filters out OG internal WLM collector threads (they run
// permanently and would always dominate the "long transaction" list on an
// idle instance). Also excludes our own session.
const longtxSQL = `SELECT
  pid,
  usename,
  state,
  age(clock_timestamp(), xact_start)::text AS xact_duration,
  age(clock_timestamp(), query_start)::text AS query_duration,
  LEFT(query, 80) AS query
FROM pg_stat_activity
WHERE xact_start IS NOT NULL
  AND state != 'idle'
  AND pid != pg_backend_pid()
  AND query NOT LIKE '%%WLM fetch collect info%%'
  AND query NOT LIKE '%%pg_stat_get_wlm%%'
ORDER BY xact_start
LIMIT %d`

// LongTxSkill shows long-running transactions.
type LongTxSkill struct{ driver db.Driver }

// NewLongTxSkill creates a LongTxSkill backed by the given driver.
func NewLongTxSkill(driver db.Driver) *LongTxSkill {
	return &LongTxSkill{driver: driver}
}

func (s *LongTxSkill) Name() string                       { return "longtx" }
func (s *LongTxSkill) Description() string                { return "长事务列表" }
func (s *LongTxSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *LongTxSkill) Validate(_ skill.Params) error      { return nil }

func (s *LongTxSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/longtx [limit]",
		Examples: []string{"/longtx", "/longtx 50"},
	}
}

func (s *LongTxSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "longtx",
		Description: "Show long-running transactions ordered by transaction start time",
		Parameters: map[string]any{
			"limit": map[string]any{"type": "integer", "description": "Max rows (default 20)"},
		},
	}
}

func (s *LongTxSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	limit := params.IntOr("limit", 20)
	sqlStr := fmt.Sprintf(longtxSQL, limit)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无长事务",
			Summary:  "no long transactions",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("长事务 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个长事务", len(result.Rows)),
	}, nil
}
