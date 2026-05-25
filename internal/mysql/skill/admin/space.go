/*-------------------------------------------------------------------------
 *
 * space.go
 *	  space — SpaceSkill plus helpers (NewSpaceSkill) used by the
 *	  admin package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/admin/space.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
)

const spaceSQL = `SELECT
  TABLE_SCHEMA AS db_name,
  COUNT(*) AS table_count,
  ROUND(SUM(DATA_LENGTH + INDEX_LENGTH) / 1048576, 2) AS used_mb,
  ROUND(SUM(DATA_FREE) / 1048576, 2) AS free_mb
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
GROUP BY TABLE_SCHEMA
ORDER BY used_mb DESC`

type SpaceSkill struct {
	driver db.Driver
}

func NewSpaceSkill(driver db.Driver) *SpaceSkill {
	return &SpaceSkill{driver: driver}
}

func (s *SpaceSkill) Name() string        { return "space" }
func (s *SpaceSkill) Description() string  { return "数据库空间使用" }
func (s *SpaceSkill) Category() string     { return "admin" }
func (s *SpaceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SpaceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/space", Examples: []string{"/space"}}
}

func (s *SpaceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "space",
		Description: "Show database space usage per schema",
	}
}

func (s *SpaceSkill) Validate(_ skill.Params) error { return nil }

// Space panel column widths.
const (
	mySpNameW = 20
	mySpSizeW = 12
	mySpBarW  = 18
)

func (s *SpaceSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, spaceSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := formatMySQLSpacePanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("数据库空间 — %d 个", len(result.Rows)),
	}, nil
}

func formatMySQLSpacePanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No databases found"
	}

	// Find max used_mb for bar calculation.
	var maxMB float64
	for _, row := range qr.Rows {
		if len(row) < 3 {
			continue
		}
		mb := shared.ToFloat64(row[2])
		if mb > maxMB {
			maxMB = mb
		}
	}
	if maxMB == 0 {
		maxMB = 1
	}

	header := " " + format.PadRight("Database", mySpNameW) + " " +
		format.PadRight("Used/Free", mySpSizeW) + " " +
		format.PadRight("Tables", 6) + "  " +
		format.PadRight("Bar", mySpBarW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	var rows []string
	for _, row := range qr.Rows {
		if len(row) < 4 {
			continue
		}
		name := fmt.Sprintf("%v", row[0])
		tables := fmt.Sprintf("%v", row[1])
		usedMB := shared.ToFloat64(row[2])
		freeMB := shared.ToFloat64(row[3])
		pct := usedMB / maxMB * 100

		sizeStr := fmt.Sprintf("%s / %s", format.FormatBytes(usedMB), format.FormatBytes(freeMB))
		bar := format.ProgressBar(pct, mySpBarW)

		line := " " + format.PadRight(format.TruncDisplayWidth(name, mySpNameW), mySpNameW) + " " +
			format.PadRight(sizeStr, mySpSizeW) + " " +
			format.PadRight(tables, 6) + "  " + bar

		rows = append(rows, line)
	}

	summary := fmt.Sprintf(" 共 %d 个数据库 (按大小降序)", len(qr.Rows))

	var lines []string
	lines = append(lines, summary)
	lines = append(lines, header, sep)
	lines = append(lines, rows...)

	return format.Panel("数据库空间", []format.PanelSection{
		{Lines: lines},
	})
}
