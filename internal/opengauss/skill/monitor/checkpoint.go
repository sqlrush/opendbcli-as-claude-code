/*-------------------------------------------------------------------------
 *
 * checkpoint.go
 *	  CheckpointSkill analyses checkpoint frequency and WAL write
 *	  amplification.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/checkpoint.go
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

// checkpointSQL pulls the full pg_stat_bgwriter snapshot. maxwritten_clean
// and buffers_alloc are added so we can surface bgwriter throttle events
// and total allocation pressure — both are diagnostic signals when
// checkpoint tuning goes wrong.
const checkpointSQL = `SELECT
  checkpoints_timed,
  checkpoints_req,
  checkpoint_write_time::int AS write_ms,
  checkpoint_sync_time::int  AS sync_ms,
  buffers_checkpoint,
  buffers_clean,
  maxwritten_clean,
  buffers_backend,
  buffers_backend_fsync,
  buffers_alloc,
  stats_reset::text
FROM pg_stat_bgwriter`

// checkpointParamsSQL fetches settings that drive checkpoint behaviour.
const checkpointParamsSQL = `SELECT name, setting, unit
FROM pg_settings
WHERE name IN (
  'checkpoint_timeout', 'max_wal_size', 'min_wal_size',
  'checkpoint_completion_target', 'full_page_writes',
  'wal_compression', 'synchronous_commit'
)
ORDER BY name`

// CheckpointSkill analyses checkpoint frequency and WAL write amplification.
type CheckpointSkill struct{ driver db.Driver }

// NewCheckpointSkill creates a CheckpointSkill.
func NewCheckpointSkill(driver db.Driver) *CheckpointSkill {
	return &CheckpointSkill{driver: driver}
}

func (s *CheckpointSkill) Name() string                       { return "checkpoint" }
func (s *CheckpointSkill) Description() string                { return "Checkpoint 频率和 WAL 写放大" }
func (s *CheckpointSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *CheckpointSkill) Validate(_ skill.Params) error      { return nil }
func (s *CheckpointSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/checkpoint"} }

func (s *CheckpointSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "checkpoint",
		Description: "Show checkpoint counters, write/sync timing and related settings",
	}
}

func (s *CheckpointSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	stats, err := s.driver.Query(ctx, checkpointSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	params, _ := s.driver.Query(ctx, checkpointParamsSQL)

	// Warn when requested checkpoints dominate timed ones (indicates
	// max_wal_size is too small or write volume too high).
	warn := ""
	if stats != nil && len(stats.Rows) == 1 {
		row := stats.Rows[0]
		timed := rowInt(row, 0)
		req := rowInt(row, 1)
		if req > timed && timed+req > 0 {
			pct := 100.0 * float64(req) / float64(timed+req)
			warn = fmt.Sprintf("  ⚠ 请求式 checkpoint 占比 %.0f%%（%d/%d），建议增大 max_wal_size",
				pct, req, timed+req)
		}
	}

	paramsRows := 0
	if params != nil {
		paramsRows = len(params.Rows)
	}
	rendered := fmt.Sprintf("Checkpoint 统计 + %d 项相关参数%s\n%s",
		paramsRows, "", warn)

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     stats,
		Rendered: rendered,
		Summary:  "checkpoint stats",
	}, nil
}
