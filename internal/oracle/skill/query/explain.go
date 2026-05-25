/*-------------------------------------------------------------------------
 *
 * explain.go
 *	  ExplainData is the structured output of the explain skill for LLM
 *	  consumption.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/explain.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const displayPlanSQL = `SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY())`
const displayCursorSQL = `SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR(:1))`

// sqlKeywords used to detect whether args is a SQL statement rather than a SQL ID.
var sqlKeywords = []string{
	"SELECT", "INSERT", "UPDATE", "DELETE", "MERGE",
	"CREATE", "ALTER", "DROP", "WITH", "EXPLAIN",
}

// ExplainData is the structured output of the explain skill for LLM consumption.
type ExplainData struct {
	Mode      string   `json:"mode"`                // "sql" or "sql_id"
	SQLID     string   `json:"sql_id,omitempty"`
	PlanLines []string `json:"plan_lines"`
}

// ExplainSkill shows execution plans for SQL statements or SQL IDs.
type ExplainSkill struct {
	driver db.Driver
}

// NewExplainSkill creates an ExplainSkill backed by the given driver.
func NewExplainSkill(driver db.Driver) *ExplainSkill {
	return &ExplainSkill{driver: driver}
}

func (s *ExplainSkill) Name() string        { return "explain" }
func (s *ExplainSkill) Description() string  { return "Show execution plan for a SQL statement or SQL ID" }
func (s *ExplainSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ExplainSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "explain",
		Description: "Show execution plan for a SQL statement or SQL ID",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "SQL text or SQL ID to explain",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *ExplainSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "explain",
		Aliases:  []string{"xplan"},
		Usage:    "/explain <sql_text | sql_id>",
		Examples: []string{"/explain SELECT * FROM dual", "/explain abc123def"},
	}
}

func (s *ExplainSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("explain requires a SQL statement or SQL ID")
	}
	return nil
}

func (s *ExplainSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	if looksLikeSQL(args) {
		return s.explainSQL(ctx, args)
	}
	return s.explainSQLID(ctx, args)
}

// explainSQL runs EXPLAIN PLAN FOR the given SQL, then fetches the plan.
func (s *ExplainSkill) explainSQL(ctx context.Context, sqlText string) (*skill.Result, error) {
	explainStmt := "EXPLAIN PLAN FOR " + sqlText
	if _, err := s.driver.Exec(ctx, explainStmt); err != nil {
		return nil, fmt.Errorf("explain plan failed: %w", err)
	}

	planResult, err := s.driver.Query(ctx, displayPlanSQL)
	if err != nil {
		return nil, fmt.Errorf("fetching plan display failed: %w", err)
	}

	planLines := extractLines(planResult)
	planLines = highlightFTSLines(planLines)
	planText := strings.Join(planLines, "\n")
	planText += ftsWarning(planLines)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &ExplainData{Mode: "sql", PlanLines: planLines},
		Rendered: planText,
		Summary:  "Execution plan via EXPLAIN PLAN FOR",
		Metadata: map[string]string{
			"mode": "sql",
		},
	}, nil
}

// explainSQLID fetches the cursor plan for a given SQL ID.
func (s *ExplainSkill) explainSQLID(ctx context.Context, sqlID string) (*skill.Result, error) {
	planResult, err := s.driver.Query(ctx, displayCursorSQL, sqlID)
	if err != nil {
		return nil, fmt.Errorf("fetching cursor plan failed: %w", err)
	}

	planLines := extractLines(planResult)
	planLines = highlightFTSLines(planLines)
	planText := strings.Join(planLines, "\n")
	planText += ftsWarning(planLines)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &ExplainData{Mode: "sql_id", SQLID: sqlID, PlanLines: planLines},
		Rendered: planText,
		Summary:  fmt.Sprintf("Execution plan for SQL ID %s", sqlID),
		Metadata: map[string]string{
			"mode":   "sql_id",
			"sql_id": sqlID,
		},
	}, nil
}

// looksLikeSQL returns true if the input appears to be a SQL statement
// (contains spaces and starts with a known keyword).
func looksLikeSQL(input string) bool {
	if !strings.Contains(input, " ") {
		return false
	}
	upper := strings.ToUpper(strings.TrimSpace(input))
	for _, kw := range sqlKeywords {
		if strings.HasPrefix(upper, kw) {
			return true
		}
	}
	return false
}

// detectFTS scans plan lines for TABLE ACCESS FULL operations and returns table names.
func detectFTS(lines []string) []string {
	var tables []string
	seen := make(map[string]bool)
	for _, line := range lines {
		if !strings.Contains(line, "TABLE ACCESS FULL") {
			continue
		}
		// DBMS_XPLAN format: | Id | Operation | Name | ...
		// TABLE ACCESS FULL is in Operation column, Name is next column.
		parts := strings.Split(line, "|")
		for i, part := range parts {
			if strings.Contains(part, "TABLE ACCESS FULL") && i+1 < len(parts) {
				name := strings.TrimSpace(parts[i+1])
				if name != "" && !seen[name] {
					tables = append(tables, name)
					seen[name] = true
				}
				break
			}
		}
	}
	return tables
}

// ftsWarning returns a warning string if full table scans are detected, empty otherwise.
func ftsWarning(planLines []string) string {
	tables := detectFTS(planLines)
	if len(tables) == 0 {
		return ""
	}

	const (
		ansiReset  = "\033[0m"
		ansiYellow = "\033[33m"
		ansiDim    = "\033[90m"
	)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s⚠ 检测到全表扫描 (TABLE ACCESS FULL):%s\n", ansiYellow, ansiReset))
	for _, t := range tables {
		b.WriteString(fmt.Sprintf("  %s• %s%s\n", ansiYellow, t, ansiReset))
	}
	b.WriteString(fmt.Sprintf("  %s提示: /indexadvise %s  查看索引建议%s\n", ansiDim, tables[0], ansiReset))
	return b.String()
}


// highlightFTSLines appends a yellow FTS marker to any line containing TABLE ACCESS FULL.
func highlightFTSLines(lines []string) []string {
	const (
		ansiYellow = "\033[33m"
		ansiReset  = "\033[0m"
	)
	result := make([]string, len(lines))
	for i, line := range lines {
		if strings.Contains(line, "TABLE ACCESS FULL") {
			result[i] = line + ansiYellow + "  ← ⚠ FTS" + ansiReset
		} else {
			result[i] = line
		}
	}
	return result
}

// extractLines converts a QueryResult into a slice of strings from the first column.
func extractLines(qr *db.QueryResult) []string {
	if qr == nil || len(qr.Rows) == 0 {
		return []string{"(no plan output)"}
	}
	lines := make([]string, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		if len(row) > 0 && row[0] != nil {
			lines = append(lines, fmt.Sprint(row[0]))
		} else {
			lines = append(lines, "")
		}
	}
	return lines
}
