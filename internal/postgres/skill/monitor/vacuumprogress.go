/*-------------------------------------------------------------------------
 *
 * vacuumprogress.go
 *	  vacuumprogress — VacuumProgressSkill plus helpers
 *	  (NewVacuumProgressSkill) used by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/vacuumprogress.go
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

const vacuumProgressSQL = `SELECT
  p.pid,
  a.usename,
  p.datname,
  n.nspname || '.' || c.relname AS table_name,
  p.phase,
  p.heap_blks_total,
  p.heap_blks_scanned,
  p.heap_blks_vacuumed,
  p.index_vacuum_count,
  p.max_dead_tuples,
  p.num_dead_tuples,
  CASE WHEN p.heap_blks_total > 0
    THEN ROUND((p.heap_blks_vacuumed::numeric / p.heap_blks_total * 100), 1)
    ELSE 0 END AS pct_done,
  EXTRACT(EPOCH FROM clock_timestamp() - a.query_start)::numeric(10,0) AS elapsed_sec
FROM pg_stat_progress_vacuum p
JOIN pg_stat_activity a ON p.pid = a.pid
JOIN pg_class c ON p.relid = c.oid
JOIN pg_namespace n ON c.relnamespace = n.oid
ORDER BY elapsed_sec DESC`

type VacuumProgressSkill struct{ driver db.Driver }

func NewVacuumProgressSkill(driver db.Driver) *VacuumProgressSkill {
	return &VacuumProgressSkill{driver: driver}
}

func (s *VacuumProgressSkill) Name() string                      { return "vacuumprogress" }
func (s *VacuumProgressSkill) Description() string               { return "running VACUUM progress" }
func (s *VacuumProgressSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *VacuumProgressSkill) Validate(_ skill.Params) error     { return nil }
func (s *VacuumProgressSkill) CLIDef() skill.CLIDef              { return skill.CLIDef{Usage: "/vacuumprogress"} }
func (s *VacuumProgressSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "vacuumprogress",
		Description: "Show progress of running VACUUM operations including phase, heap blocks processed, dead tuples found, and completion percentage. Warns if stuck in index cleanup phase.",
	}
}

func (s *VacuumProgressSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, vacuumProgressSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "No VACUUM operations currently running",
			Summary:  "no active vacuum",
		}, nil
	}
	rendered := formatVacuumProgressPanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d VACUUM operation(s) in progress", len(result.Rows)),
	}, nil
}

func formatVacuumProgressPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No active VACUUM operations"
	}

	const (
		pidW     = 7
		userW    = 10
		tableW   = 24
		phaseW   = 22
		blksTW   = 10
		blksVW   = 10
		deadW    = 10
		pctW     = 6
		elapsedW = 8
	)

	header := " " + format.PadRight("PID", pidW) + " " +
		format.PadRight("User", userW) + " " +
		format.PadRight("Table", tableW) + " " +
		format.PadRight("Phase", phaseW) + " " +
		format.PadLeft("BlksTotal", blksTW) + " " +
		format.PadLeft("Vacuumed", blksVW) + " " +
		format.PadLeft("DeadTup", deadW) + " " +
		format.PadLeft("Done%", pctW) + " " +
		format.PadLeft("Elapsed", elapsedW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	var rows []string
	stuckInIndex := false
	for _, row := range qr.Rows {
		if len(row) < 13 {
			continue
		}
		phase := fmtVal(row[4])
		pct := toFloat64(row[11])
		elapsed := toFloat64(row[12])

		warning := ""
		if phase == "vacuuming indexes" && elapsed > 300 {
			warning = " !!!"
			stuckInIndex = true
		}

		line := " " + format.PadRight(fmtVal(row[0]), pidW) + " " +
			format.PadRight(format.TruncDisplayWidth(fmtVal(row[1]), userW), userW) + " " +
			format.PadRight(format.TruncDisplayWidth(fmtVal(row[3]), tableW), tableW) + " " +
			format.PadRight(format.TruncDisplayWidth(phase+warning, phaseW), phaseW) + " " +
			format.PadLeft(fmtVal(row[5]), blksTW) + " " +
			format.PadLeft(fmtVal(row[7]), blksVW) + " " +
			format.PadLeft(fmtVal(row[10]), deadW) + " " +
			format.PadLeft(fmt.Sprintf("%.1f", pct), pctW) + " " +
			format.PadLeft(fmtWaitTime(elapsed), elapsedW)

		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)
	if stuckInIndex {
		lines = append(lines, " !!! VACUUM stuck in index cleanup — consider VACUUM (INDEX_CLEANUP false) to skip index cleaning")
	}

	return format.Panel(fmt.Sprintf("VACUUM Progress (%d running)", len(qr.Rows)), []format.PanelSection{
		{Lines: lines},
	})
}
