/*-------------------------------------------------------------------------
 *
 * sqladvisor_skill.go
 *	  SQLAdvisorSkill provides SQL performance diagnosis and
 *	  optimization suggestions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/sqladvisor_skill.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	orasqladv "github.com/sqlrush/opendb/internal/oracle/sqladvisor"
	"github.com/sqlrush/opendb/internal/skill"
	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// SQLAdvisorSkill provides SQL performance diagnosis and optimization suggestions.
type SQLAdvisorSkill struct {
	driver db.Driver
}

// NewSQLAdvisorSkill creates an SQLAdvisorSkill backed by the given driver.
func NewSQLAdvisorSkill(driver db.Driver) *SQLAdvisorSkill {
	return &SQLAdvisorSkill{driver: driver}
}

func (s *SQLAdvisorSkill) Name() string                       { return "sqladvisor" }
func (s *SQLAdvisorSkill) Description() string                { return "SQL 性能诊断与优化建议" }
func (s *SQLAdvisorSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLAdvisorSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "sqladvisor",
		Description: "Analyze SQL execution plan and provide optimization suggestions",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "SQL ID, SQL text fragment, or empty for auto top SQL analysis",
				},
			},
		},
	}
}

func (s *SQLAdvisorSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sqladvisor",
		Aliases: []string{"sqladv", "sqladvise"},
		Usage:   "/sqladvisor [sql_id | \"SQL text\"]",
		Examples: []string{
			"/sqladvisor",
			"/sqladvisor 8rd2y271f37v8",
			"/sqladvisor \"SELECT * FROM orders WHERE status\"",
		},
	}
}

func (s *SQLAdvisorSkill) Validate(params skill.Params) error {
	return nil // all modes are valid, including no args
}

func (s *SQLAdvisorSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	advisor := orasqladv.New(s.driver)

	// Mode 1: no args -> analyze top SQLs
	if args == "" {
		reports, err := advisor.AnalyzeTopSQLs(ctx)
		if err != nil {
			return &skill.Result{Type: skill.ResultText, Rendered: fmt.Sprintf("无法分析 Top SQL: %s", err)}, nil
		}
		return s.renderMulti(reports), nil
	}

	// Mode 2: quoted string -> SQL text search
	if strings.HasPrefix(args, "\"") || strings.HasPrefix(args, "'") {
		text := strings.Trim(args, "\"'")
		reports, err := advisor.FindAndAnalyze(ctx, text)
		if err != nil {
			return &skill.Result{Type: skill.ResultText, Rendered: fmt.Sprintf("未找到匹配的 SQL: %s", err)}, nil
		}
		return s.renderMulti(reports), nil
	}

	// Mode 3: sql_id
	report, err := advisor.Analyze(ctx, args)
	if err != nil {
		return &skill.Result{Type: skill.ResultText, Rendered: fmt.Sprintf("分析失败: %s", err)}, nil
	}
	rendered := sadv.RenderReport(report)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("SQL %s: %d 个问题", report.SQLID, len(report.Findings)),
	}, nil
}

func (s *SQLAdvisorSkill) renderMulti(reports []*sadv.SQLReport) *skill.Result {
	var sb strings.Builder
	for i, r := range reports {
		if i > 0 {
			sb.WriteString("\n" + strings.Repeat("─", 60) + "\n\n")
		}
		sb.WriteString(sadv.RenderReport(r))
	}
	total := 0
	for _, r := range reports {
		total += len(r.Findings)
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("分析了 %d 条 SQL，共 %d 个问题", len(reports), total),
	}
}
