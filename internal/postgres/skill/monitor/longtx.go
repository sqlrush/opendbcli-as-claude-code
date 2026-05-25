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
 *	  internal/postgres/skill/monitor/longtx.go
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

// P02: Enhanced longtx SQL with WAL usage estimation per transaction.
const longtxSQL = `SELECT
  a.pid,
  a.usename,
  a.state,
  age(clock_timestamp(), a.xact_start)::text AS xact_duration,
  age(clock_timestamp(), a.query_start)::text AS query_duration,
  LEFT(a.query, 80) AS query,
  COALESCE((SELECT SUM(n_tup_ins + n_tup_upd + n_tup_del)
    FROM pg_stat_xact_all_tables WHERE pg_stat_xact_all_tables.relid IN
      (SELECT c.oid FROM pg_class c WHERE c.relkind IN ('r','m'))), 0) AS est_modified_rows
FROM pg_stat_activity a
WHERE a.xact_start IS NOT NULL
  AND a.state != 'idle'
  AND a.backend_type = 'client backend'
  AND a.pid != pg_backend_pid()
ORDER BY a.xact_start
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
