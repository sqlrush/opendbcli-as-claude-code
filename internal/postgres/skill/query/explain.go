/*-------------------------------------------------------------------------
 *
 * explain.go
 *	  ExplainSkill runs EXPLAIN on a SQL statement and highlights
 *	  potential issues.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/query/explain.go
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

// ExplainSkill runs EXPLAIN on a SQL statement and highlights potential issues.
type ExplainSkill struct {
	driver db.Driver
}

// NewExplainSkill creates an ExplainSkill backed by the given driver.
func NewExplainSkill(driver db.Driver) *ExplainSkill {
	return &ExplainSkill{driver: driver}
}

func (s *ExplainSkill) Name() string                       { return "explain" }
func (s *ExplainSkill) Description() string                { return "SQL 执行计划" }
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
		Description: "Show execution plan for a SQL statement using EXPLAIN (TEXT format)",
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
	if isWriteStmt(upper) {
		return &skill.Result{
			Type:    skill.ResultError,
			Summary: "仅支持 SELECT/WITH 语句的执行计划",
		}, nil
	}

	// Strip leading EXPLAIN if user already included it.
	cleanSQL := sqlText
	if strings.HasPrefix(upper, "EXPLAIN") {
		cleanSQL = strings.TrimSpace(sqlText[7:])
		cleanUpper := strings.ToUpper(cleanSQL)
		// Remove format options
		for _, prefix := range []string{"(ANALYZE", "(FORMAT", "(BUFFERS"} {
			if strings.HasPrefix(cleanUpper, prefix) {
				idx := strings.Index(cleanSQL, ")")
				if idx >= 0 {
					cleanSQL = strings.TrimSpace(cleanSQL[idx+1:])
				}
			}
		}
	}

	explainSQL := "EXPLAIN (ANALYZE false, BUFFERS false, FORMAT TEXT) " + cleanSQL

	result, err := s.driver.Query(ctx, explainSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	planText := extractExplainText(result)
	warnings := detectExplainIssues(planText)

	rendered := buildExplainPanel(cleanSQL, planText, warnings)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  fmt.Sprintf("execution plan (%d warnings)", len(warnings)),
	}, nil
}

func isWriteStmt(upper string) bool {
	for _, prefix := range []string{"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "CREATE", "TRUNCATE"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func extractExplainText(result *db.QueryResult) string {
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

func detectExplainIssues(planText string) []string {
	var warnings []string
	lower := strings.ToLower(planText)

	if strings.Contains(lower, "seq scan") {
		warnings = append(warnings, "Seq Scan detected - consider adding an index on filter columns")
	}
	if strings.Contains(lower, "sort") && !strings.Contains(lower, "sort key") {
		warnings = append(warnings, "Sort operation detected - may benefit from an index on ORDER BY columns")
	}
	if strings.Contains(lower, "nested loop") && strings.Contains(lower, "seq scan") {
		warnings = append(warnings, "Nested Loop with Seq Scan - consider adding join index")
	}
	if strings.Contains(lower, "hash join") {
		// Not necessarily bad, but worth noting for large tables
	}

	return warnings
}

// highlightIssueLines appends a yellow Seq Scan marker to lines containing Seq Scan.
func highlightIssueLines(lines []string) []string {
	const (
		ansiYellow = "\033[33m"
		ansiReset  = "\033[0m"
	)
	result := make([]string, len(lines))
	for i, line := range lines {
		if strings.Contains(line, "Seq Scan") {
			result[i] = line + ansiYellow + "  ← ⚠ Seq Scan" + ansiReset
		} else {
			result[i] = line
		}
	}
	return result
}

func buildExplainPanel(sqlText, planText string, warnings []string) string {
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
			Header: "Execution Plan",
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
