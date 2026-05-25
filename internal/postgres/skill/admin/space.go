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
 *	  internal/postgres/skill/admin/space.go
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
  datname,
  pg_database_size(datname) / 1048576 AS size_mb
FROM pg_database
WHERE datname NOT IN ('template0', 'template1')
ORDER BY pg_database_size(datname) DESC`

type SpaceSkill struct{ driver db.Driver }

func NewSpaceSkill(driver db.Driver) *SpaceSkill { return &SpaceSkill{driver: driver} }

func (s *SpaceSkill) Name() string                      { return "space" }
func (s *SpaceSkill) Description() string                { return "数据库空间使用" }
func (s *SpaceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SpaceSkill) Validate(_ skill.Params) error      { return nil }
func (s *SpaceSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/space"} }
func (s *SpaceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "space", Description: "Show database sizes"}
}

// Space panel column widths.
const (
	pgSpNameW = 20
	pgSpSizeW = 10
	pgSpBarW  = 18
)

func (s *SpaceSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, spaceSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := formatPGSpacePanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("数据库空间 — %d 个", len(result.Rows)),
	}, nil
}

func formatPGSpacePanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No databases found"
	}

	// Find max size for percentage calculation.
	var maxMB float64
	for _, row := range qr.Rows {
		if len(row) < 2 {
			continue
		}
		mb := shared.ToFloat64(row[1])
		if mb > maxMB {
			maxMB = mb
		}
	}
	if maxMB == 0 {
		maxMB = 1
	}

	header := " " + format.PadRight("Database", pgSpNameW) + " " +
		format.PadRight("Size", pgSpSizeW) + "  " +
		format.PadRight("Bar", pgSpBarW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	var rows []string
	for _, row := range qr.Rows {
		if len(row) < 2 {
			continue
		}
		name := fmt.Sprintf("%v", row[0])
		mb := shared.ToFloat64(row[1])
		pct := mb / maxMB * 100

		sizeStr := format.FormatBytes(mb)
		bar := format.ProgressBar(pct, pgSpBarW)

		line := " " + format.PadRight(format.TruncDisplayWidth(name, pgSpNameW), pgSpNameW) + " " +
			format.PadRight(sizeStr, pgSpSizeW) + "  " + bar

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
