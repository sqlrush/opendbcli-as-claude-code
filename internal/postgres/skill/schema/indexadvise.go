/*-------------------------------------------------------------------------
 *
 * indexadvise.go
 *	  IndexAdviseSkill provides index recommendations based on EXPLAIN
 *	  output.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/schema/indexadvise.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// IndexAdviseSkill provides index recommendations based on EXPLAIN output.
type IndexAdviseSkill struct {
	driver db.Driver
}

// NewIndexAdviseSkill creates an IndexAdviseSkill backed by the given driver.
func NewIndexAdviseSkill(driver db.Driver) *IndexAdviseSkill {
	return &IndexAdviseSkill{driver: driver}
}

func (s *IndexAdviseSkill) Name() string                       { return "indexadvise" }
func (s *IndexAdviseSkill) Description() string                { return "索引推荐" }
func (s *IndexAdviseSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *IndexAdviseSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/indexadvise <sql>",
		Examples: []string{"/indexadvise SELECT * FROM orders WHERE user_id = 1 AND status = 'open'"},
	}
}

func (s *IndexAdviseSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "indexadvise",
		Description: "Suggest index improvements based on EXPLAIN (FORMAT JSON) analysis",
		Parameters: map[string]any{
			"sql": map[string]any{
				"type":        "string",
				"description": "SQL SELECT statement to analyze",
			},
		},
	}
}

func (s *IndexAdviseSkill) Validate(params skill.Params) error {
	if params.StringOr("args", "") != "" || params.StringOr("sql", "") != "" {
		return nil
	}
	return fmt.Errorf("请提供 SQL 语句, 如: /indexadvise SELECT * FROM t WHERE col = 1")
}

func (s *IndexAdviseSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	sqlText := strings.TrimSpace(params.StringOr("args", ""))
	if sqlText == "" {
		sqlText = strings.TrimSpace(params.StringOr("sql", ""))
	}

	// If args is a bare table name (no SQL keyword), wrap it in a SELECT.
	upper := strings.ToUpper(sqlText)
	if !strings.HasPrefix(upper, "SELECT") &&
		!strings.HasPrefix(upper, "WITH") &&
		!strings.HasPrefix(upper, "INSERT") &&
		!strings.HasPrefix(upper, "UPDATE") &&
		!strings.HasPrefix(upper, "DELETE") {
		sqlText = "SELECT * FROM " + sqlText + " LIMIT 1"
	}

	// Run EXPLAIN (FORMAT JSON) for detailed plan info
	explainSQL := "EXPLAIN (FORMAT JSON) " + sqlText
	jsonResult, err := s.driver.Query(ctx, explainSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Data: err.Error(), Summary: err.Error()}, nil
	}

	jsonText := extractJSON(jsonResult)

	// Also run regular EXPLAIN for text-based analysis
	textResult, err := s.driver.Query(ctx, "EXPLAIN "+sqlText)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Data: err.Error(), Summary: err.Error()}, nil
	}

	advice := analyzeExplain(textResult, jsonText)

	rendered := buildAdvicePanel(sqlText, advice)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  fmt.Sprintf("index advice: %d suggestions", len(advice)),
	}, nil
}

type adviceItem struct {
	table   string
	issue   string
	suggest string
}

func extractJSON(result *db.QueryResult) string {
	if len(result.Rows) == 0 {
		return ""
	}
	var parts []string
	for _, row := range result.Rows {
		for _, col := range row {
			s := asStr(col)
			if s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func analyzeExplain(result *db.QueryResult, jsonText string) []adviceItem {
	var advice []adviceItem

	// Parse text EXPLAIN output for Seq Scan patterns
	for _, row := range result.Rows {
		for _, col := range row {
			line := asStr(col)
			lower := strings.ToLower(line)

			if strings.Contains(lower, "seq scan") {
				table := extractTableFromPlan(line)
				advice = append(advice, adviceItem{
					table:   table,
					issue:   "Seq Scan detected",
					suggest: fmt.Sprintf("Add index on filter columns for table %s", table),
				})
			}
		}
	}

	// Check JSON for additional hints
	jsonLower := strings.ToLower(jsonText)
	if strings.Contains(jsonLower, "\"node type\": \"seq scan\"") && len(advice) == 0 {
		advice = append(advice, adviceItem{
			table:   "(from JSON plan)",
			issue:   "Seq Scan detected in JSON plan",
			suggest: "Add index on columns used in WHERE clause",
		})
	}

	if strings.Contains(jsonLower, "\"sort key\"") {
		advice = append(advice, adviceItem{
			table:   "(sort)",
			issue:   "Sort operation in plan",
			suggest: "Consider adding index on ORDER BY columns to avoid sort",
		})
	}

	if len(advice) == 0 {
		advice = append(advice, adviceItem{
			table:   "-",
			issue:   "No obvious issues found",
			suggest: "Query plan looks reasonable; no index changes recommended",
		})
	}

	return advice
}

func extractTableFromPlan(line string) string {
	// Try to extract table name from "Seq Scan on tablename" pattern
	lower := strings.ToLower(line)
	idx := strings.Index(lower, "seq scan on ")
	if idx < 0 {
		return "(unknown)"
	}
	rest := strings.TrimSpace(line[idx+len("seq scan on "):])
	parts := strings.Fields(rest)
	if len(parts) > 0 {
		return parts[0]
	}
	return "(unknown)"
}

func buildAdvicePanel(sqlText string, advice []adviceItem) string {
	displaySQL := sqlText
	if len(displaySQL) > 100 {
		displaySQL = displaySQL[:97] + "..."
	}

	sections := []format.PanelSection{
		{
			Header: "SQL",
			Lines:  []string{displaySQL},
		},
	}

	var adviceLines []string
	for i, a := range advice {
		adviceLines = append(adviceLines,
			fmt.Sprintf("[%d] Table: %s", i+1, a.table),
			fmt.Sprintf("    Issue:   %s", a.issue),
			fmt.Sprintf("    Advice:  %s", a.suggest),
			"",
		)
	}

	sections = append(sections, format.PanelSection{
		Header: "Recommendations",
		Lines:  adviceLines,
	})

	return format.Panel("Index Advice", sections)
}
