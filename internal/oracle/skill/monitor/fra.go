/*-------------------------------------------------------------------------
 *
 * fra.go
 *	  FRAOverview represents the FRA summary row.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/fra.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const fraOverviewSQL = `SELECT name,
       ROUND(space_limit/1048576) AS limit_mb,
       ROUND(space_used/1048576) AS used_mb,
       ROUND(space_reclaimable/1048576) AS reclaimable_mb,
       ROUND(space_used/NULLIF(space_limit,0)*100, 1) AS used_pct
FROM v$recovery_file_dest`

const fraUsageByTypeSQL = `SELECT file_type,
       ROUND(percent_space_used, 1) AS used_pct,
       ROUND(percent_space_reclaimable, 1) AS reclaimable_pct,
       number_of_files
FROM v$flash_recovery_area_usage
WHERE percent_space_used > 0
ORDER BY percent_space_used DESC`

// FRAOverview represents the FRA summary row.
type FRAOverview struct {
	Name          string  `json:"name"`
	LimitMB       int     `json:"limit_mb"`
	UsedMB        int     `json:"used_mb"`
	ReclaimableMB int     `json:"reclaimable_mb"`
	UsedPct       float64 `json:"used_pct"`
}

// FRATypeUsage represents space usage by file type.
type FRATypeUsage struct {
	FileType       string  `json:"file_type"`
	UsedPct        float64 `json:"used_pct"`
	ReclaimablePct float64 `json:"reclaimable_pct"`
	NumberOfFiles  int     `json:"number_of_files"`
}

// FRAReport is the structured output for LLM consumption.
type FRAReport struct {
	Overview  *FRAOverview  `json:"overview"`
	TypeUsage []FRATypeUsage `json:"type_usage"`
}

// FRASkill shows Flash Recovery Area usage overview.
type FRASkill struct {
	driver db.Driver
}

// NewFRASkill creates a FRASkill backed by the given driver.
func NewFRASkill(driver db.Driver) *FRASkill {
	return &FRASkill{driver: driver}
}

func (s *FRASkill) Name() string                      { return "fra" }
func (s *FRASkill) Description() string               { return "Show Flash Recovery Area (FRA) usage overview" }
func (s *FRASkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *FRASkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "fra",
		Description: "Show Flash Recovery Area (FRA) usage overview",
	}
}

func (s *FRASkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "fra",
		Usage:   "/fra",
	}
}

func (s *FRASkill) Validate(_ skill.Params) error { return nil }

func (s *FRASkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	overviewResult, err := s.driver.Query(ctx, fraOverviewSQL)
	if err != nil {
		return nil, fmt.Errorf("querying FRA overview: %w", err)
	}

	if len(overviewResult.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "未配置 Flash Recovery Area (FRA).",
			Summary:  "FRA not configured",
		}, nil
	}

	row := overviewResult.Rows[0]
	overview := FRAOverview{
		Name:          rowStr(row, 0),
		LimitMB:       rowInt(row, 1),
		UsedMB:        rowInt(row, 2),
		ReclaimableMB: rowInt(row, 3),
		UsedPct:       rowFloat(row, 4),
	}

	usageResult, err := s.driver.Query(ctx, fraUsageByTypeSQL)
	if err != nil {
		return nil, fmt.Errorf("querying FRA usage by type: %w", err)
	}

	typeUsage := make([]FRATypeUsage, 0, len(usageResult.Rows))
	for _, r := range usageResult.Rows {
		typeUsage = append(typeUsage, FRATypeUsage{
			FileType:       rowStr(r, 0),
			UsedPct:        rowFloat(r, 1),
			ReclaimablePct: rowFloat(r, 2),
			NumberOfFiles:  rowInt(r, 3),
		})
	}

	report := FRAReport{
		Overview:  &overview,
		TypeUsage: typeUsage,
	}
	rendered := fraRender(overview, typeUsage)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &report,
		Rendered: rendered,
		Summary:  fmt.Sprintf("FRA %.1f%% used, %d file types", overview.UsedPct, len(typeUsage)),
	}, nil
}

func fraRender(overview FRAOverview, typeUsage []FRATypeUsage) string {
	var b strings.Builder

	b.WriteString("FRA 使用概览:\n\n")
	fmt.Fprintf(&b, " FRA 路径: %s\n", overview.Name)

	limitGB := float64(overview.LimitMB) / 1024
	usedGB := float64(overview.UsedMB) / 1024
	reclaimGB := float64(overview.ReclaimableMB) / 1024

	fmt.Fprintf(&b, " 总大小: %.0f GB | 已用: %.0f GB (%.1f%%) | 可回收: %.0f GB\n",
		limitGB, usedGB, overview.UsedPct, reclaimGB)

	if len(typeUsage) > 0 {
		b.WriteString("\n按类型分布:\n\n")

		qr := &db.QueryResult{
			Columns: []string{"类型", "占比", "可回收", "文件数"},
			Rows:    make([][]any, 0, len(typeUsage)),
		}
		for _, t := range typeUsage {
			qr.Rows = append(qr.Rows, []any{
				t.FileType,
				fmt.Sprintf("%.1f%%", t.UsedPct),
				fmt.Sprintf("%.1f%%", t.ReclaimablePct),
				t.NumberOfFiles,
			})
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 50, TermWidth: 120}))
	}

	b.WriteString("\n\n提示: /alter db_recovery_file_dest_size 150G 扩大FRA\n")

	return b.String()
}
