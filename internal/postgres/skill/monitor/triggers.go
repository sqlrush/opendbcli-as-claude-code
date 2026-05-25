/*-------------------------------------------------------------------------
 *
 * triggers.go
 *	  triggers — TriggersSkill plus helpers (NewTriggersSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/triggers.go
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

const triggersSQL = `SELECT
  n.nspname AS schema,
  c.relname AS table_name,
  t.tgname AS trigger_name,
  CASE t.tgtype & 66
    WHEN 2 THEN 'BEFORE'
    WHEN 64 THEN 'INSTEAD OF'
    ELSE 'AFTER'
  END AS timing,
  CASE
    WHEN t.tgtype & 28 = 28 THEN 'INSERT OR UPDATE OR DELETE'
    WHEN t.tgtype & 20 = 20 THEN 'INSERT OR UPDATE'
    WHEN t.tgtype & 24 = 24 THEN 'UPDATE OR DELETE'
    WHEN t.tgtype & 12 = 12 THEN 'INSERT OR DELETE'
    WHEN t.tgtype & 4  = 4  THEN 'INSERT'
    WHEN t.tgtype & 8  = 8  THEN 'DELETE'
    WHEN t.tgtype & 16 = 16 THEN 'UPDATE'
    WHEN t.tgtype & 32 = 32 THEN 'TRUNCATE'
    ELSE 'UNKNOWN'
  END AS events,
  CASE WHEN t.tgtype & 1 = 1 THEN 'ROW' ELSE 'STATEMENT' END AS granularity,
  p.proname AS function_name,
  CASE WHEN t.tgenabled = 'D' THEN 'DISABLED'
       WHEN t.tgenabled = 'R' THEN 'REPLICA'
       WHEN t.tgenabled = 'A' THEN 'ALWAYS'
       ELSE 'ENABLED'
  END AS status
FROM pg_trigger t
JOIN pg_class c ON t.tgrelid = c.oid
JOIN pg_namespace n ON c.relnamespace = n.oid
JOIN pg_proc p ON t.tgfoid = p.oid
WHERE NOT t.tgisinternal
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY n.nspname, c.relname, t.tgname`

type TriggersSkill struct{ driver db.Driver }

func NewTriggersSkill(driver db.Driver) *TriggersSkill {
	return &TriggersSkill{driver: driver}
}

func (s *TriggersSkill) Name() string                      { return "triggers" }
func (s *TriggersSkill) Description() string               { return "list triggers on user tables" }
func (s *TriggersSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *TriggersSkill) Validate(_ skill.Params) error     { return nil }
func (s *TriggersSkill) CLIDef() skill.CLIDef              { return skill.CLIDef{Usage: "/triggers [table]"} }
func (s *TriggersSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "triggers",
		Description: "List all triggers on user tables with timing, events, granularity (ROW/STATEMENT), and function. FOR EACH ROW triggers on high-write tables are performance risks.",
	}
}

func (s *TriggersSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, triggersSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "No user triggers found",
			Summary:  "no triggers",
		}, nil
	}
	rendered := formatTriggersPanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d triggers found", len(result.Rows)),
	}, nil
}

func formatTriggersPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No triggers"
	}

	const (
		schemaW  = 10
		tableW   = 20
		nameW    = 24
		timingW  = 10
		eventsW  = 28
		granW    = 6
		funcW    = 20
		statusW  = 8
	)

	header := " " + format.PadRight("Schema", schemaW) + " " +
		format.PadRight("Table", tableW) + " " +
		format.PadRight("Trigger", nameW) + " " +
		format.PadRight("Timing", timingW) + " " +
		format.PadRight("Events", eventsW) + " " +
		format.PadRight("Level", granW) + " " +
		format.PadRight("Function", funcW) + " " +
		format.PadRight("Status", statusW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	var rows []string
	rowTriggers := 0
	for _, row := range qr.Rows {
		if len(row) < 8 {
			continue
		}
		gran := fmtVal(row[5])
		if gran == "ROW" {
			rowTriggers++
		}
		warning := ""
		if gran == "ROW" {
			warning = " !!!"
		}
		line := " " + format.PadRight(format.TruncDisplayWidth(fmtVal(row[0]), schemaW), schemaW) + " " +
			format.PadRight(format.TruncDisplayWidth(fmtVal(row[1]), tableW), tableW) + " " +
			format.PadRight(format.TruncDisplayWidth(fmtVal(row[2]), nameW), nameW) + " " +
			format.PadRight(fmtVal(row[3]), timingW) + " " +
			format.PadRight(format.TruncDisplayWidth(fmtVal(row[4]), eventsW), eventsW) + " " +
			format.PadRight(gran+warning, granW) + " " +
			format.PadRight(format.TruncDisplayWidth(fmtVal(row[6]), funcW), funcW) + " " +
			format.PadRight(fmtVal(row[7]), statusW)
		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)
	if rowTriggers > 0 {
		lines = append(lines, fmt.Sprintf(" !!! %d FOR EACH ROW trigger(s) — performance risk on high-write tables", rowTriggers))
	}

	return format.Panel(fmt.Sprintf("Triggers (%d total)", len(qr.Rows)), []format.PanelSection{
		{Lines: lines},
	})
}
