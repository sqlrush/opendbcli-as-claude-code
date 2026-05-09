/*-------------------------------------------------------------------------
 *
 * explain.go
 *	  ExplainSkill runs EXPLAIN FORMAT=TREE on a SQL statement.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/query/explain.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// ExplainSkill runs EXPLAIN FORMAT=TREE on a SQL statement.
type ExplainSkill struct {
	driver db.Driver
}

// NewExplainSkill creates an ExplainSkill backed by the given driver.
func NewExplainSkill(driver db.Driver) *ExplainSkill {
	return &ExplainSkill{driver: driver}
}

func (s *ExplainSkill) Name() string                       { return "explain" }
func (s *ExplainSkill) Description() string                { return "SQL 执行计划" }
func (s *ExplainSkill) Category() string                   { return "query" }
func (s *ExplainSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ExplainSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/explain <sql>",
		Examples: []string{"/explain SELECT * FROM orders WHERE id = 1"},
	}
}

func (s *ExplainSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "explain",
		Description: "Show execution plan for a SQL statement using EXPLAIN FORMAT=TREE",
		Parameters: map[string]any{
			"sql": map[string]any{
				"type":        "string",
				"description": "SQL statement to explain",
			},
		},
	}
}

func (s *ExplainSkill) Validate(params skill.Params) error {
	sqlText := strings.TrimSpace(params.StringOr("args", ""))
	if sqlText == "" {
		sqlText = strings.TrimSpace(params.StringOr("sql", ""))
	}
	if sqlText == "" {
		return fmt.Errorf("请提供 SQL 语句, 如: /explain SELECT * FROM t")
	}
	return nil
}

func (s *ExplainSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	sqlText := strings.TrimSpace(params.StringOr("args", ""))
	if sqlText == "" {
		sqlText = strings.TrimSpace(params.StringOr("sql", ""))
	}

	upper := strings.ToUpper(sqlText)
	if isWriteStatement(upper) {
		return &skill.Result{
			Type:    skill.ResultError,
			Summary: "仅支持 SELECT/WITH 语句的执行计划, 写操作请先转换为等效 SELECT",
		}, nil
	}

	// Strip leading EXPLAIN if user already included it.
	cleanSQL := sqlText
	if strings.HasPrefix(upper, "EXPLAIN") {
		cleanSQL = strings.TrimSpace(sqlText[7:])
		cleanUpper := strings.ToUpper(cleanSQL)
		if strings.HasPrefix(cleanUpper, "FORMAT") {
			// Remove FORMAT=xxx part
			idx := strings.Index(cleanUpper, "SELECT")
			if idx < 0 {
				idx = strings.Index(cleanUpper, "WITH")
			}
			if idx >= 0 {
				cleanSQL = cleanSQL[idx:]
			}
		}
	}

	explainSQL := "EXPLAIN FORMAT=TREE " + cleanSQL

	result, err := s.driver.Query(ctx, explainSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	planText := extractPlanText(result)
	warnings := detectIssues(planText)

	rendered := buildExplainPanel(cleanSQL, planText, warnings)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  fmt.Sprintf("execution plan (%d warnings)", len(warnings)),
	}, nil
}

func isWriteStatement(upper string) bool {
	for _, prefix := range []string{"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "CREATE", "TRUNCATE"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func extractPlanText(result *db.QueryResult) string {
	if len(result.Rows) == 0 {
		return "(empty plan)"
	}
	var lines []string
	for _, row := range result.Rows {
		for _, col := range row {
			s := asStr(col)
			if s != "" {
				lines = append(lines, s)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func detectIssues(planText string) []string {
	var warnings []string
	lower := strings.ToLower(planText)

	if strings.Contains(lower, "table scan") || strings.Contains(lower, "full scan") {
		warnings = append(warnings, "Full Table Scan detected - consider adding an index")
	}
	if strings.Contains(lower, "filesort") {
		warnings = append(warnings, "Filesort detected - consider optimizing ORDER BY")
	}
	if strings.Contains(lower, "temporary") {
		warnings = append(warnings, "Temporary table used - check GROUP BY / DISTINCT")
	}
	if strings.Contains(lower, "nested loop") {
		// Only warn for large nested loops
		if strings.Contains(lower, "full scan") {
			warnings = append(warnings, "Nested Loop with full scan - may need join index")
		}
	}

	return warnings
}

// highlightIssueLines appends a yellow FTS marker to lines containing Table scan or Full scan.
func highlightIssueLines(lines []string) []string {
	const (
		ansiYellow = "\033[33m"
		ansiReset  = "\033[0m"
	)
	result := make([]string, len(lines))
	for i, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "table scan") || strings.Contains(lower, "full scan") {
			result[i] = line + ansiYellow + "  ← ⚠ FTS" + ansiReset
		} else {
			result[i] = line
		}
	}
	return result
}

func buildExplainPanel(sqlText, planText string, warnings []string) string {
	// Truncate SQL for display
	displaySQL := sqlText
	if len(displaySQL) > 100 {
		displaySQL = displaySQL[:97] + "..."
	}

	planLines := strings.Split(planText, "\n")
	planLines = highlightIssueLines(planLines)

	sections := []format.PanelSection{
		{
			Header: "SQL",
			Lines:  []string{displaySQL},
		},
		{
			Header: "Execution Plan (TREE)",
			Lines:  planLines,
		},
	}

	if len(warnings) > 0 {
		var warnLines []string
		for _, w := range warnings {
			warnLines = append(warnLines, fmt.Sprintf("%s %s", format.StatusIcon("WARN"), w))
		}
		sections = append(sections, format.PanelSection{
			Header: "Warnings",
			Lines:  warnLines,
		})
	}

	return format.Panel("EXPLAIN", sections)
}

// asStr safely converts any value to string (local to query package).
func asStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
