/*-------------------------------------------------------------------------
 *
 * wal.go
 *	  WALSkill shows WAL status: current xlog location and
 *	  pg_stat_archiver.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/wal.go
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

// OpenGauss uses pg_current_xlog_location() instead of pg_current_wal_lsn().
const walLSNSQL = `SELECT pg_current_xlog_location()::text AS current_lsn`

const walArchiverSQL = `SELECT
  archived_count,
  last_archived_wal,
  last_archived_time::text,
  failed_count,
  last_failed_wal,
  last_failed_time::text,
  stats_reset::text
FROM pg_stat_archiver`

// walSettingsSQL pulls the settings most relevant to WAL sizing and
// switch frequency — matching what Oracle /redo would show (log buffer
// size, log file group count, etc. have analogues here).
const walSettingsSQL = `SELECT name, setting, unit
FROM pg_settings
WHERE name IN (
  'wal_level', 'max_wal_size', 'min_wal_size',
  'wal_buffers', 'wal_writer_delay',
  'checkpoint_timeout', 'checkpoint_completion_target',
  'archive_mode', 'archive_command',
  'synchronous_commit', 'fsync', 'full_page_writes'
)
ORDER BY name`

// walBgwriterSQL gives checkpoint counters that approximate redo switch
// frequency (Oracle calls it "log switches per hour").
const walBgwriterSQL = `SELECT
  checkpoints_timed,
  checkpoints_req,
  EXTRACT(EPOCH FROM (now() - stats_reset))::int AS since_reset_sec
FROM pg_stat_bgwriter`

// WALSkill shows WAL status: current xlog location and pg_stat_archiver.
type WALSkill struct{ driver db.Driver }

// NewWALSkill creates a WALSkill backed by the given driver.
func NewWALSkill(driver db.Driver) *WALSkill {
	return &WALSkill{driver: driver}
}

func (s *WALSkill) Name() string                       { return "wal" }
func (s *WALSkill) Description() string                { return "WAL 状态" }
func (s *WALSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *WALSkill) Validate(_ skill.Params) error      { return nil }
func (s *WALSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/wal"} }
func (s *WALSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "wal", Description: "Show WAL status: current xlog location and pg_stat_archiver"}
}

func (s *WALSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	sections := []format.PanelSection{}

	// Current WAL LSN
	lsnResult, err := s.driver.Query(ctx, walLSNSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(lsnResult.Rows) > 0 {
		row := lsnResult.Rows[0]
		sections = append(sections, format.PanelSection{
			Header: "Current WAL",
			Lines: []string{
				fmt.Sprintf("LSN             : %s", asStr(row[0])),
			},
		})
	}

	// pg_stat_archiver
	archResult, err := s.driver.Query(ctx, walArchiverSQL)
	if err == nil && len(archResult.Rows) > 0 {
		row := archResult.Rows[0]
		archCount := asStr(row[0])
		failedCount := asStr(row[3])

		lines := []string{
			fmt.Sprintf("Archived Count  : %s", archCount),
			fmt.Sprintf("Last Archived   : %s", asStr(row[1])),
			fmt.Sprintf("Last Archive At : %s", asStr(row[2])),
			fmt.Sprintf("Failed Count    : %s", failedCount),
		}
		if failedCount != "0" && failedCount != "" {
			lines = append(lines,
				fmt.Sprintf("Last Failed     : %s", asStr(row[4])),
				fmt.Sprintf("Last Fail At    : %s", asStr(row[5])),
			)
		}
		sections = append(sections, format.PanelSection{
			Header: "Archiver",
			Lines:  lines,
		})
	}

	// Checkpoint switch rate — Oracle /redo shows "log switches / hour"; this
	// is the closest analogue on the PG/OG side.
	if bg, err := s.driver.Query(ctx, walBgwriterSQL); err == nil && len(bg.Rows) > 0 {
		row := bg.Rows[0]
		timed := asStr(row[0])
		req := asStr(row[1])
		since := asStr(row[2])
		sections = append(sections, format.PanelSection{
			Header: "Checkpoint Rate (approximates Oracle log switches)",
			Lines: []string{
				fmt.Sprintf("Timed Checkpoints    : %s", timed),
				fmt.Sprintf("Requested Checkpoints: %s", req),
				fmt.Sprintf("Since stats reset    : %s sec", since),
			},
		})
	}

	// Configuration (sizing / archive / sync / fsync).
	if settings, err := s.driver.Query(ctx, walSettingsSQL); err == nil && len(settings.Rows) > 0 {
		lines := make([]string, 0, len(settings.Rows))
		for _, r := range settings.Rows {
			unit := ""
			if r[2] != nil {
				unit = fmt.Sprintf(" %v", r[2])
			}
			lines = append(lines, fmt.Sprintf("%-32s : %v%s", r[0], r[1], unit))
		}
		sections = append(sections, format.PanelSection{Header: "WAL Settings", Lines: lines})
	}

	rendered := format.Panel("WAL Status", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "WAL status",
	}, nil
}
