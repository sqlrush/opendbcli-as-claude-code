/*-------------------------------------------------------------------------
 *
 * sga.go
 *	  SGAComponentItem represents a single SGA dynamic component row.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/sga.go
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

const sgaComponentsSQL = `SELECT component,
       ROUND(current_size/1048576) AS current_mb,
       ROUND(min_size/1048576) AS min_mb,
       ROUND(max_size/1048576) AS max_mb
FROM v$sga_dynamic_components
WHERE current_size > 0
ORDER BY current_size DESC`

const sgaTotalSQL = `SELECT 'Total SGA' AS component,
       ROUND(SUM(value)/1048576) AS mb
FROM v$sga`

// SGAComponentItem represents a single SGA dynamic component row.
type SGAComponentItem struct {
	Component string  `json:"component"`
	CurrentMB float64 `json:"current_mb"`
	MinMB     float64 `json:"min_mb"`
	MaxMB     float64 `json:"max_mb"`
}

// SGAReport is the structured output for LLM consumption.
type SGAReport struct {
	Components []SGAComponentItem `json:"components"`
	TotalMB    float64            `json:"total_mb"`
}

// SGASkill shows SGA memory overview with dynamic components and total size.
type SGASkill struct {
	driver db.Driver
}

// NewSGASkill creates a SGASkill backed by the given driver.
func NewSGASkill(driver db.Driver) *SGASkill {
	return &SGASkill{driver: driver}
}

func (s *SGASkill) Name() string                          { return "sga" }
func (s *SGASkill) Description() string                   { return "Show SGA memory overview" }
func (s *SGASkill) SecurityLevel() skill.SecurityLevel     { return skill.LevelReadOnly }

func (s *SGASkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "sga",
		Description: "Show SGA memory overview",
	}
}

func (s *SGASkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sga",
		Usage:   "/sga",
	}
}

func (s *SGASkill) Validate(_ skill.Params) error { return nil }

func (s *SGASkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	compResult, err := s.driver.Query(ctx, sgaComponentsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying SGA components: %w", err)
	}

	totalResult, err := s.driver.Query(ctx, sgaTotalSQL)
	if err != nil {
		return nil, fmt.Errorf("querying SGA total: %w", err)
	}

	components := make([]SGAComponentItem, 0, len(compResult.Rows))
	for _, row := range compResult.Rows {
		components = append(components, SGAComponentItem{
			Component: rowStr(row, 0),
			CurrentMB: rowFloat(row, 1),
			MinMB:     rowFloat(row, 2),
			MaxMB:     rowFloat(row, 3),
		})
	}

	var totalMB float64
	if len(totalResult.Rows) > 0 {
		totalMB = rowFloat(totalResult.Rows[0], 1)
	}

	report := SGAReport{Components: components, TotalMB: totalMB}
	rendered := sgaRender(components, totalMB)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &report,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d SGA components, total %.0f MB", len(components), totalMB),
	}, nil
}

func sgaRender(components []SGAComponentItem, totalMB float64) string {
	var b string

	b = "SGA 内存概览:\n\n"

	qr := &db.QueryResult{
		Columns: []string{"组件", "大小(MB)", "最小(MB)", "最大(MB)"},
		Rows:    make([][]any, 0, len(components)),
	}
	for _, c := range components {
		qr.Rows = append(qr.Rows, []any{
			c.Component,
			fmt.Sprintf("%.0f", c.CurrentMB),
			fmt.Sprintf("%.0f", c.MinMB),
			fmt.Sprintf("%.0f", c.MaxMB),
		})
	}
	b += format.FormatTableOpts(qr, format.TableOptions{MaxRows: 50, TermWidth: 120})

	b += fmt.Sprintf("\n\n SGA Total: %.0f MB\n", totalMB)

	return b
}
