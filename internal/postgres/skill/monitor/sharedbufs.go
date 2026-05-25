/*-------------------------------------------------------------------------
 *
 * sharedbufs.go
 *	  SharedBufsSkill shows shared_buffers usage and hit ratios.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/sharedbufs.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const sharedBufsSettingSQL = `SELECT
  current_setting('shared_buffers') AS shared_buffers,
  current_setting('effective_cache_size') AS effective_cache_size`

const sharedBufsStatsSQL = `SELECT
  COALESCE(buffers_checkpoint, 0) AS buffers_checkpoint,
  COALESCE(buffers_clean, 0) AS buffers_clean,
  COALESCE(buffers_backend, 0) AS buffers_backend,
  COALESCE(buffers_alloc, 0) AS buffers_alloc,
  COALESCE(maxwritten_clean, 0) AS maxwritten_clean
FROM pg_stat_bgwriter`

const sharedBufsCacheSQL = `SELECT
  COALESCE(sum(blks_hit), 0) AS blks_hit,
  COALESCE(sum(blks_read), 0) AS blks_read
FROM pg_stat_database`

// SharedBufsSkill shows shared_buffers usage and hit ratios.
type SharedBufsSkill struct{ driver db.Driver }

// NewSharedBufsSkill creates a SharedBufsSkill backed by the given driver.
func NewSharedBufsSkill(driver db.Driver) *SharedBufsSkill {
	return &SharedBufsSkill{driver: driver}
}

func (s *SharedBufsSkill) Name() string                       { return "sharedbufs" }
func (s *SharedBufsSkill) Description() string                { return "Shared Buffers 使用情况" }
func (s *SharedBufsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SharedBufsSkill) Validate(_ skill.Params) error      { return nil }
func (s *SharedBufsSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/sharedbufs"} }
func (s *SharedBufsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "sharedbufs", Description: "Show shared_buffers configuration and cache hit ratios"}
}

func (s *SharedBufsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	sections := []format.PanelSection{}

	// Settings
	settingResult, err := s.driver.Query(ctx, sharedBufsSettingSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(settingResult.Rows) > 0 {
		row := settingResult.Rows[0]
		sections = append(sections, format.PanelSection{
			Header: "Configuration",
			Lines: []string{
				fmt.Sprintf("shared_buffers      : %s", asStr(row[0])),
				fmt.Sprintf("effective_cache_size : %s", asStr(row[1])),
			},
		})
	}

	// Cache hit ratio
	cacheResult, err := s.driver.Query(ctx, sharedBufsCacheSQL)
	if err == nil && len(cacheResult.Rows) > 0 {
		hit := toFloat64Val(cacheResult.Rows[0][0])
		read := toFloat64Val(cacheResult.Rows[0][1])
		hitPct := 0.0
		if hit+read > 0 {
			hitPct = hit / (hit + read) * 100
		}
		sections = append(sections, format.PanelSection{
			Header: "Cache Hit Ratio",
			Lines: []string{
				fmt.Sprintf("Blocks Hit      : %.0f", hit),
				fmt.Sprintf("Blocks Read     : %.0f", read),
				fmt.Sprintf("Hit Ratio       : %.2f%%  %s", hitPct, format.ProgressBar(hitPct, 20)),
			},
		})
	}

	// BGWriter stats
	statsResult, err := s.driver.Query(ctx, sharedBufsStatsSQL)
	if err == nil && len(statsResult.Rows) > 0 {
		row := statsResult.Rows[0]
		sections = append(sections, format.PanelSection{
			Header: "BGWriter",
			Lines: []string{
				fmt.Sprintf("Checkpoint Bufs : %s", asStr(row[0])),
				fmt.Sprintf("Clean Bufs      : %s", asStr(row[1])),
				fmt.Sprintf("Backend Bufs    : %s", asStr(row[2])),
				fmt.Sprintf("Allocated Bufs  : %s", asStr(row[3])),
				fmt.Sprintf("MaxWritten Clean: %s", asStr(row[4])),
			},
		})
	}

	rendered := format.Panel("Shared Buffers", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "shared buffers usage",
	}, nil
}
