/*-------------------------------------------------------------------------
 *
 * space.go
 *	  SpaceSkill shows tablespace usage metrics.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/space.go
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

const spaceSQL = `SELECT tablespace_name,
       ROUND(used_space * 8192 / 1024/1024, 2) as used_mb,
       ROUND(tablespace_size * 8192 / 1024/1024, 2) as total_mb,
       ROUND(used_percent, 2) as used_pct
FROM dba_tablespace_usage_metrics
ORDER BY used_percent DESC`

// SpaceSkill shows tablespace usage metrics.
type SpaceSkill struct {
	driver db.Driver
}

// NewSpaceSkill creates a SpaceSkill backed by the given driver.
func NewSpaceSkill(driver db.Driver) *SpaceSkill {
	return &SpaceSkill{driver: driver}
}

func (s *SpaceSkill) Name() string        { return "space" }
func (s *SpaceSkill) Description() string { return "Show tablespace usage" }
func (s *SpaceSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelReadOnly
}

func (s *SpaceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "space",
		Description: "Show tablespace usage",
	}
}

func (s *SpaceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "space",
		Usage:    "/space",
		Examples: []string{"/space"},
	}
}

func (s *SpaceSkill) Validate(_ skill.Params) error { return nil }

func (s *SpaceSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, spaceSQL)
	if err != nil {
		return nil, fmt.Errorf("querying tablespace usage: %w", err)
	}

	rendered := formatSpacePanel(result)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("表空间使用率 — %d 个", len(result.Rows)),
	}, nil
}

// Space panel column widths.
const (
	spNameW   = 14
	spSizeW   = 15
	spPctW    = 5
	spBarW    = 18
)

func formatSpacePanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No tablespaces found"
	}

	header := " " + format.PadRight("Name", spNameW) + " " +
		format.PadRight("Used/Total", spSizeW) + " " +
		format.PadLeft("Pct", spPctW) + "  " +
		format.PadRight("Bar", spBarW) + "  " +
		"Status"

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	var rows []string
	var alerts []string
	for _, row := range qr.Rows {
		if len(row) < 4 {
			continue
		}
		name := fmt.Sprintf("%v", row[0])
		usedMB := shared.ToFloat64(row[1])
		totalMB := shared.ToFloat64(row[2])
		pct := shared.ToFloat64(row[3])

		sizeStr := fmt.Sprintf("%s / %s", format.FormatBytes(usedMB), format.FormatBytes(totalMB))
		pctStr := fmt.Sprintf("%.0f%%", pct)
		bar := format.ProgressBar(pct, spBarW)

		status := "OK"
		if pct > 95 {
			status = "FAIL"
		} else if pct > 85 {
			status = "WARN"
		}
		icon := format.StatusIcon(status)
		statusStr := icon + " " + status

		line := " " + format.PadRight(format.TruncDisplayWidth(name, spNameW), spNameW) + " " +
			format.PadRight(sizeStr, spSizeW) + " " +
			format.PadLeft(pctStr, spPctW) + "  " +
			bar + "  " + statusStr

		rows = append(rows, line)

		if status != "OK" {
			alerts = append(alerts, fmt.Sprintf(" %s %s at %.0f%% — expand with: /resize %s",
				icon, name, pct, name))
		}
	}

	// Count status distribution.
	warnCount := 0
	failCount := 0
	for _, row := range qr.Rows {
		if len(row) < 4 {
			continue
		}
		pct := shared.ToFloat64(row[3])
		if pct > 95 {
			failCount++
		} else if pct > 85 {
			warnCount++
		}
	}

	// Summary line.
	summary := fmt.Sprintf(" 共 %d 个表空间 (按使用率降序)", len(qr.Rows))
	if failCount > 0 || warnCount > 0 {
		summary += " —"
		if failCount > 0 {
			summary += fmt.Sprintf(" %d 个超 95%%", failCount)
		}
		if warnCount > 0 {
			summary += fmt.Sprintf(" %d 个超 85%%", warnCount)
		}
	}

	var lines []string
	lines = append(lines, summary)
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	if len(alerts) > 0 {
		lines = append(lines, sep)
		lines = append(lines, alerts...)
	}

	return format.Panel("表空间使用率", []format.PanelSection{
		{Lines: lines},
	})
}
