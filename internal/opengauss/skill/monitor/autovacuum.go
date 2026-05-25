/*-------------------------------------------------------------------------
 *
 * autovacuum.go
 *	  AutoVacuumSkill shows autovacuum progress and recent activity.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/autovacuum.go
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

// autovacuumProgressSQL — pg_stat_progress_vacuum was introduced in PG 9.6;
// OG 5.0 does not expose it. We fall back to an approximation based on
// pg_stat_activity showing autovacuum worker backends.
//
// Excludes the calling session so the skill does not report itself as a
// vacuum worker just because its own SQL contains the word "autovacuum".
const autovacuumProgressSQL = `SELECT
  pid,
  COALESCE(application_name, 'autovacuum') AS worker,
  state,
  EXTRACT(EPOCH FROM now() - query_start)::int AS elapsed_sec,
  LEFT(query, 100) AS current_query
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
  AND (application_name = 'AutoVacuum'
       OR (query ILIKE '%vacuum%' AND query NOT ILIKE '%pg_stat%'))
ORDER BY query_start`

// autovacuumRecentSQL summarises autovacuum activity per table — last run,
// dead tuple count, autovacuum/analyze counters.
var autovacuumRecentSQL = `SELECT
  schemaname || '.' || relname AS table_name,
  n_live_tup,
  n_dead_tup,
  CASE WHEN n_live_tup > 0
       THEN ROUND(100.0 * n_dead_tup / n_live_tup, 1)
       ELSE 0 END AS dead_pct,
  last_autovacuum,
  autovacuum_count,
  last_autoanalyze,
  autoanalyze_count
FROM pg_stat_all_tables
WHERE ` + systemSchemaFilter + `
  AND n_dead_tup > 100
ORDER BY n_dead_tup DESC
LIMIT 20`

// AutoVacuumSkill shows autovacuum progress and recent activity.
type AutoVacuumSkill struct{ driver db.Driver }

// NewAutoVacuumSkill creates an AutoVacuumSkill.
func NewAutoVacuumSkill(driver db.Driver) *AutoVacuumSkill {
	return &AutoVacuumSkill{driver: driver}
}

func (s *AutoVacuumSkill) Name() string                       { return "autovacuum" }
func (s *AutoVacuumSkill) Description() string                { return "autovacuum 进度和近期活动" }
func (s *AutoVacuumSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *AutoVacuumSkill) Validate(_ skill.Params) error      { return nil }
func (s *AutoVacuumSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/autovacuum"} }

func (s *AutoVacuumSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "autovacuum",
		Description: "Show in-progress autovacuum workers and top tables by dead tuples",
	}
}

func (s *AutoVacuumSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	progress, err := s.driver.Query(ctx, autovacuumProgressSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	recent, err := s.driver.Query(ctx, autovacuumRecentSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	progressRows := 0
	if progress != nil {
		progressRows = len(progress.Rows)
	}
	recentRows := 0
	if recent != nil {
		recentRows = len(recent.Rows)
	}

	// Prefer in-progress view when workers are running.
	if progressRows > 0 {
		return &skill.Result{
			Type:     skill.ResultTable,
			Data:     progress,
			Rendered: fmt.Sprintf("autovacuum 进行中 — %d 个 worker（另有 %d 张表死元组堆积）", progressRows, recentRows),
			Summary:  fmt.Sprintf("%d worker running", progressRows),
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     recent,
		Rendered: fmt.Sprintf("autovacuum 空闲 — Top %d 张高死元组表", recentRows),
		Summary:  fmt.Sprintf("idle; top %d dead-tuple tables", recentRows),
	}, nil
}
