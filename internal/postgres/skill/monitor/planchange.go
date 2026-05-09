/*-------------------------------------------------------------------------
 *
 * planchange.go
 *	  planchange — PlanChangeSkill plus helpers (NewPlanChangeSkill)
 *	  used by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/planchange.go
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

const planChangeSQL = `SELECT
  queryid,
  LEFT(query, 100) AS query,
  calls,
  ROUND(mean_exec_time::numeric, 2) AS mean_ms,
  ROUND(max_exec_time::numeric, 2) AS max_ms,
  ROUND(stddev_exec_time::numeric, 2) AS stddev_ms,
  CASE WHEN mean_exec_time > 0
    THEN ROUND((max_exec_time / mean_exec_time)::numeric, 1)
    ELSE 0 END AS spike_ratio
FROM pg_stat_statements
WHERE calls > 5
  AND max_exec_time > mean_exec_time * 3
  AND mean_exec_time > 1
ORDER BY (max_exec_time / NULLIF(mean_exec_time, 0)) DESC
LIMIT 20`

type PlanChangeSkill struct{ driver db.Driver }

func NewPlanChangeSkill(driver db.Driver) *PlanChangeSkill {
	return &PlanChangeSkill{driver: driver}
}

func (s *PlanChangeSkill) Name() string                      { return "planchange" }
func (s *PlanChangeSkill) Description() string               { return "detect execution plan regression (queries with high spike ratio)" }
func (s *PlanChangeSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *PlanChangeSkill) Validate(_ skill.Params) error     { return nil }
func (s *PlanChangeSkill) CLIDef() skill.CLIDef              { return skill.CLIDef{Usage: "/planchange"} }
func (s *PlanChangeSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "planchange",
		Description: "Detect possible execution plan regressions by finding queries whose max_exec_time is much higher than mean_exec_time (spike ratio > 3x)",
	}
}

func (s *PlanChangeSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, planChangeSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "No queries with significant execution time spikes detected",
			Summary:  "no plan regressions detected",
		}, nil
	}
	rendered := formatPlanChangePanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d queries with possible plan regression (spike ratio > 3x)", len(result.Rows)),
	}, nil
}

func formatPlanChangePanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No plan regressions detected"
	}

	const (
		qidW   = 12
		queryW = 40
		callsW = 8
		meanW  = 10
		maxW   = 10
		stdW   = 10
		spikeW = 8
	)

	header := " " + format.PadRight("QueryID", qidW) + " " +
		format.PadRight("Query", queryW) + " " +
		format.PadLeft("Calls", callsW) + " " +
		format.PadLeft("Mean(ms)", meanW) + " " +
		format.PadLeft("Max(ms)", maxW) + " " +
		format.PadLeft("StdDev", stdW) + " " +
		format.PadLeft("Spike", spikeW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	var rows []string
	for _, row := range qr.Rows {
		if len(row) < 7 {
			continue
		}
		spike := toFloat64(row[6])
		warning := ""
		if spike > 10 {
			warning = " !!!"
		} else if spike > 5 {
			warning = " !!"
		}
		line := " " + format.PadRight(fmtVal(row[0]), qidW) + " " +
			format.PadRight(format.TruncDisplayWidth(fmtVal(row[1]), queryW), queryW) + " " +
			format.PadLeft(fmtVal(row[2]), callsW) + " " +
			format.PadLeft(fmtVal(row[3]), meanW) + " " +
			format.PadLeft(fmtVal(row[4]), maxW) + " " +
			format.PadLeft(fmtVal(row[5]), stdW) + " " +
			format.PadLeft(fmt.Sprintf("%.1fx%s", spike, warning), spikeW)
		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)
	lines = append(lines, " Spike = max_exec_time / mean_exec_time (>3x suggests plan regression)")
	lines = append(lines, " Tip: Use /explain <queryid> to check current execution plan")

	return format.Panel(fmt.Sprintf("Plan Regression Detection (%d suspicious queries)", len(qr.Rows)), []format.PanelSection{
		{Lines: lines},
	})
}
