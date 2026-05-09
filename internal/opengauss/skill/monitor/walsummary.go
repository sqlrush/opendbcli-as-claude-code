/*-------------------------------------------------------------------------
 *
 * walsummary.go
 *	  WALSummarySkill provides WAL-level archiver detail (vs /wal which
 *	  is more of a high-level status).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/walsummary.go
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

// walSummarySQL — OG 5.0 has no pg_stat_archiver and no archiver-stat
// functions. We pull the archive configuration as a minimum-viable view
// (whether archiving is on and what command runs) and rely on /wal +
// /bgworker for finer signals.
const walSummarySQL = `SELECT
  name,
  setting,
  short_desc
FROM pg_settings
WHERE name IN ('archive_mode', 'archive_command', 'archive_timeout',
               'wal_level', 'max_wal_size', 'min_wal_size')
ORDER BY name`

// walGenRateSQL estimates current WAL generation pace. OG uses the legacy
// xlog_* naming from PG 9.2.
const walGenRateSQL = `SELECT
  pg_xlogfile_name(pg_current_xlog_location())                            AS current_wal,
  pg_current_xlog_location()::text                                        AS current_lsn,
  pg_size_pretty(pg_xlog_location_diff(pg_current_xlog_location(), '0/0')::bigint) AS wal_written_total`

// WALSummarySkill provides WAL-level archiver detail (vs /wal which is more
// of a high-level status).
type WALSummarySkill struct{ driver db.Driver }

// NewWALSummarySkill creates a WALSummarySkill.
func NewWALSummarySkill(driver db.Driver) *WALSummarySkill {
	return &WALSummarySkill{driver: driver}
}

func (s *WALSummarySkill) Name() string                       { return "walsummary" }
func (s *WALSummarySkill) Description() string                { return "WAL 归档细节" }
func (s *WALSummarySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *WALSummarySkill) Validate(_ skill.Params) error      { return nil }
func (s *WALSummarySkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/walsummary"} }

func (s *WALSummarySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "walsummary",
		Description: "Detailed WAL archiver status and current generation position",
	}
}

func (s *WALSummarySkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	arch, err := s.driver.Query(ctx, walSummarySQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	gen, _ := s.driver.Query(ctx, walGenRateSQL)

	warn := ""
	if arch != nil && len(arch.Rows) == 1 && len(arch.Rows[0]) >= 4 {
		failed := rowInt(arch.Rows[0], 3)
		if failed > 0 {
			warn = fmt.Sprintf("  ⚠ archiver 累计失败 %d 次", failed)
		}
	}
	genLine := ""
	if gen != nil && len(gen.Rows) == 1 && len(gen.Rows[0]) >= 3 {
		genLine = fmt.Sprintf("  当前 WAL: %v  LSN: %v  累计写入: %v",
			gen.Rows[0][0], gen.Rows[0][1], gen.Rows[0][2])
	}

	rendered := fmt.Sprintf("WAL 归档细节%s\n%s", warn, genLine)
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     arch,
		Rendered: rendered,
		Summary:  "WAL archiver detail",
	}, nil
}
