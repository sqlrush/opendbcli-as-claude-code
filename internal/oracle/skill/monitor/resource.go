/*-------------------------------------------------------------------------
 *
 * resource.go
 *	  ResourceData is the structured output for LLM consumption.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/resource.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const resourceSQL = `SELECT resource_name, current_utilization, max_utilization,
       initial_allocation, limit_value
FROM v$resource_limit
WHERE limit_value != ' UNLIMITED'
AND max_utilization > 0
ORDER BY resource_name`

// ResourceData is the structured output for LLM consumption.
type ResourceData struct {
	Resources []ResourceRow `json:"resources"`
	Warnings  []string      `json:"warnings"`
}

// ResourceRow represents a single resource limit entry.
type ResourceRow struct {
	Name       string  `json:"name"`
	Current    int     `json:"current"`
	Max        int     `json:"max"`
	Limit      int     `json:"limit"`
	UsagePct   float64 `json:"usage_pct"`
	IsWarning  bool    `json:"is_warning"`
}

// ResourceSkill shows v$resource_limit usage with warnings.
type ResourceSkill struct {
	driver db.Driver
}

// NewResourceSkill creates a ResourceSkill backed by the given driver.
func NewResourceSkill(driver db.Driver) *ResourceSkill {
	return &ResourceSkill{driver: driver}
}

func (s *ResourceSkill) Name() string        { return "resource" }
func (s *ResourceSkill) Description() string  { return "Show resource limit usage with warnings" }
func (s *ResourceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ResourceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "resource",
		Description: "Show resource limit usage with warnings",
	}
}

func (s *ResourceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "resource",
		Aliases: []string{"rlimit"},
		Usage:   "/resource",
	}
}

func (s *ResourceSkill) Validate(_ skill.Params) error { return nil }

func (s *ResourceSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, resourceSQL)
	if err != nil {
		return nil, fmt.Errorf("querying resource limits: %w", err)
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无资源限制数据.",
			Summary:  "无资源限制数据",
		}, nil
	}

	rows := make([]ResourceRow, 0, len(result.Rows))
	var warnings []string

	for _, row := range result.Rows {
		name := rowStr(row, 0)
		current := rowInt(row, 1)
		max := rowInt(row, 2)
		limitVal := resParseLimit(rowStr(row, 4))

		var usagePct float64
		isWarning := false
		if limitVal > 0 {
			usagePct = float64(max) / float64(limitVal) * 100
			if usagePct > 80 {
				isWarning = true
				warnings = append(warnings, fmt.Sprintf("%s 使用率 %.1f%% > 80%% 预警线", name, usagePct))
			}
		}

		rows = append(rows, ResourceRow{
			Name:      name,
			Current:   current,
			Max:       max,
			Limit:     limitVal,
			UsagePct:  usagePct,
			IsWarning: isWarning,
		})
	}

	data := ResourceData{
		Resources: rows,
		Warnings:  warnings,
	}

	rendered := formatResource(rows, warnings)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &data,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d resources, %d warnings", len(rows), len(warnings)),
	}, nil
}

// formatResource builds the pre-rendered output table.
func formatResource(rows []ResourceRow, warnings []string) string {
	var b strings.Builder

	b.WriteString("资源限制使用:\n\n")

	qr := &db.QueryResult{
		Columns: []string{"资源名", "当前", "最大", "上限", "使用率"},
		Rows:    make([][]any, 0, len(rows)),
	}
	for _, r := range rows {
		pctStr := ""
		if r.Limit > 0 {
			pctStr = fmt.Sprintf("%.1f%%", r.UsagePct)
		}
		qr.Rows = append(qr.Rows, []any{
			r.Name, r.Current, r.Max, r.Limit, pctStr,
		})
	}
	b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 50, TermWidth: 120}))

	b.WriteString("\n\n")
	if len(warnings) == 0 {
		b.WriteString("✓ 所有资源使用率正常")
	} else {
		for _, w := range warnings {
			fmt.Fprintf(&b, "⚠ %s\n", w)
		}
	}

	return b.String()
}

// resParseLimit parses the limit_value string from v$resource_limit to an int.
func resParseLimit(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "UNLIMITED") {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
