/*-------------------------------------------------------------------------
 *
 * bgworker.go
 *	  BgWorkerSkill shows background process health.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/bgworker.go
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

// bgworkerSQL enumerates OG background threads via pg_thread_wait_status,
// which is the only view that exposes the named persistent threads
// (PageWriter, BgWriter, CheckPointer, WalWriter, Autovacuum, WLMArbiter,
// WorkloadMonitor, WDRSnapshot, JobScheduler, ...). OG is a thread-pooled
// process so ps-style backend-type enumeration isn't available.
//
// Client backends get thread_name = 'gsql' / 'JDBC' / 'workload' etc and
// are filtered out; 'WorkerSession' belongs to worker threads serving
// client queries and is also excluded.
const bgworkerSQL = `SELECT
  thread_name,
  COUNT(*) AS count,
  STRING_AGG(DISTINCT wait_status, ', ') AS wait_statuses
FROM pg_thread_wait_status
WHERE thread_name IS NOT NULL
  AND thread_name NOT IN ('gsql', 'WorkerSession', 'workload', 'JDBC')
GROUP BY thread_name
ORDER BY thread_name`

// archiverSQL — pg_stat_archiver is a PG 9.4+ view. OG 5.0 does not expose
// it. Keep the SQL but downgrade errors to a soft warning in the execute
// path so the skill still shows bgworker rows when archiver is unavailable.
const archiverSQL = `SELECT
  archived_count,
  last_archived_wal,
  last_archived_time::text,
  failed_count,
  last_failed_wal,
  last_failed_time::text,
  stats_reset::text
FROM pg_stat_archiver`

// BgWorkerSkill shows background process health.
type BgWorkerSkill struct{ driver db.Driver }

// NewBgWorkerSkill creates a BgWorkerSkill.
func NewBgWorkerSkill(driver db.Driver) *BgWorkerSkill { return &BgWorkerSkill{driver: driver} }

func (s *BgWorkerSkill) Name() string                       { return "bgworker" }
func (s *BgWorkerSkill) Description() string                { return "后台进程状态" }
func (s *BgWorkerSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *BgWorkerSkill) Validate(_ skill.Params) error      { return nil }
func (s *BgWorkerSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/bgworker"} }

func (s *BgWorkerSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "bgworker",
		Description: "Show background processes (bgwriter, walwriter, archiver, autovacuum launcher) with waits",
	}
}

func (s *BgWorkerSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, bgworkerSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	// Pull archiver counters for the summary line — they often reveal archiver
	// backlog before it becomes a replication problem.
	arch, _ := s.driver.Query(ctx, archiverSQL)
	archSummary := ""
	if arch != nil && len(arch.Rows) == 1 && len(arch.Rows[0]) >= 4 {
		failed := rowInt(arch.Rows[0], 3)
		if failed > 0 {
			archSummary = fmt.Sprintf("  ⚠ archiver 累计失败 %d 次", failed)
		}
	}

	rows := 0
	if result != nil {
		rows = len(result.Rows)
	}

	rendered := fmt.Sprintf("后台进程 — %d 个%s", rows, archSummary)
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d bgworkers", rows),
	}, nil
}
