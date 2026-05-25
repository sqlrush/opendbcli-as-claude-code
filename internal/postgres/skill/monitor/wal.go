/*-------------------------------------------------------------------------
 *
 * wal.go
 *	  WALSkill shows WAL status including pg_stat_wal and
 *	  pg_stat_archiver.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/wal.go
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

const walLSNSQL = `SELECT
  pg_current_wal_lsn()::text AS current_lsn,
  pg_walfile_name(pg_current_wal_lsn()) AS current_wal_file,
  pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0')::bigint / 1048576 AS total_wal_mb`

const walStatsSQL = `SELECT
  wal_records,
  wal_fpi,
  wal_bytes,
  wal_buffers_full,
  wal_write,
  wal_sync,
  stats_reset::text
FROM pg_stat_wal`

const walArchiverSQL = `SELECT
  archived_count,
  last_archived_wal,
  last_archived_time::text,
  failed_count,
  last_failed_wal,
  last_failed_time::text,
  stats_reset::text
FROM pg_stat_archiver`

// WALSkill shows WAL status including pg_stat_wal and pg_stat_archiver.
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
	return skill.ToolDef{Name: "wal", Description: "Show WAL status: current LSN, pg_stat_wal, pg_stat_archiver (PG14+)"}
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
				fmt.Sprintf("WAL File        : %s", asStr(row[1])),
				fmt.Sprintf("Total WAL       : %s MB", asStr(row[2])),
			},
		})
	}

	// pg_stat_wal (PG14+)
	statsResult, err := s.driver.Query(ctx, walStatsSQL)
	if err == nil && len(statsResult.Rows) > 0 {
		row := statsResult.Rows[0]
		walBytes := toFloat64Val(row[2])
		walMB := walBytes / 1048576
		sections = append(sections, format.PanelSection{
			Header: "WAL Statistics",
			Lines: []string{
				fmt.Sprintf("Records         : %s", asStr(row[0])),
				fmt.Sprintf("Full Page Images: %s", asStr(row[1])),
				fmt.Sprintf("WAL Written     : %.1f MB", walMB),
				fmt.Sprintf("Buffers Full    : %s", asStr(row[3])),
				fmt.Sprintf("Writes          : %s", asStr(row[4])),
				fmt.Sprintf("Syncs           : %s", asStr(row[5])),
				fmt.Sprintf("Stats Reset     : %s", asStr(row[6])),
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

	rendered := format.Panel("WAL Status", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "WAL status",
	}, nil
}
