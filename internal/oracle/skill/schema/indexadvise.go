/*-------------------------------------------------------------------------
 *
 * indexadvise.go
 *	  IndexAdvicePredicate holds filter/access predicate info for a full
 *	  scan.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/schema/indexadvise.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const displayPlanSQL = `SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY())`
const displayCursorSQL = `SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR(:1))`

const fullScanPredicatesSQL = `SELECT filter_predicates, access_predicates
FROM v$sql_plan
WHERE sql_id = :1
AND operation = 'TABLE ACCESS'
AND options = 'FULL'`

// IndexAdvicePredicate holds filter/access predicate info for a full scan.
type IndexAdvicePredicate struct {
	FilterPredicate string `json:"filter_predicate,omitempty"`
	AccessPredicate string `json:"access_predicate,omitempty"`
}

// IndexAdviceData is the structured output of the indexadvise skill for LLM consumption.
type IndexAdviceData struct {
	Mode       string                 `json:"mode"`       // "sql" or "sql_id"
	SQLID      string                 `json:"sql_id,omitempty"`
	PlanLines  []string               `json:"plan_lines"`
	FullScans  []string               `json:"full_scans"`
	Predicates []IndexAdvicePredicate `json:"predicates,omitempty"`
}

// IndexAdviseSkill analyzes SQL statements and recommends indexes
// by identifying full table scans in execution plans.
type IndexAdviseSkill struct {
	driver db.Driver
}

// NewIndexAdviseSkill creates an IndexAdviseSkill backed by the given driver.
func NewIndexAdviseSkill(driver db.Driver) *IndexAdviseSkill {
	return &IndexAdviseSkill{driver: driver}
}

func (s *IndexAdviseSkill) Name() string                       { return "indexadvise" }
func (s *IndexAdviseSkill) Description() string                { return "Analyze SQL and recommend indexes" }
func (s *IndexAdviseSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *IndexAdviseSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "indexadvise",
		Description: "Analyze SQL and recommend indexes based on execution plan",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "SQL text or SQL ID to analyze",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *IndexAdviseSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "indexadvise",
		Aliases:  []string{"ia"},
		Usage:    "/indexadvise <sql_text | sql_id>",
		Examples: []string{"/indexadvise SELECT * FROM orders WHERE status = 'OPEN'", "/indexadvise abc123def"},
	}
}

func (s *IndexAdviseSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("indexadvise requires SQL text or a SQL ID")
	}
	return nil
}

func (s *IndexAdviseSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	if isSQLText(args) {
		return s.adviseBySQLText(ctx, args)
	}
	return s.adviseBySQLID(ctx, args)
}

// isSQLText returns true if the input contains spaces, indicating it is
// a SQL statement rather than a SQL ID.
func isSQLText(input string) bool {
	return strings.Contains(input, " ")
}

// adviseBySQLText runs EXPLAIN PLAN for the SQL text, retrieves the plan,
// and analyzes it for full table scans.
func (s *IndexAdviseSkill) adviseBySQLText(ctx context.Context, sqlText string) (*skill.Result, error) {
	explainStmt := "EXPLAIN PLAN FOR " + sqlText
	if _, err := s.driver.Exec(ctx, explainStmt); err != nil {
		return nil, fmt.Errorf("explain plan failed: %w", err)
	}

	planResult, err := s.driver.Query(ctx, displayPlanSQL)
	if err != nil {
		return nil, fmt.Errorf("fetching plan display failed: %w", err)
	}

	planLines := extractPlanLines(planResult)
	fullScans := findFullScans(planLines)

	var b strings.Builder
	b.WriteString("=== Execution Plan ===\n")
	b.WriteString(strings.Join(planLines, "\n"))
	b.WriteString("\n\n")

	writeAdvice(&b, fullScans)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &IndexAdviceData{Mode: "sql", PlanLines: planLines, FullScans: fullScans},
		Rendered: b.String(),
		Summary:  fmt.Sprintf("Index advice: %d full table scan(s) detected", len(fullScans)),
		Metadata: map[string]string{
			"mode":       "sql",
			"full_scans": fmt.Sprintf("%d", len(fullScans)),
		},
	}, nil
}

// adviseBySQLID retrieves the cursor plan for a SQL ID and analyzes it.
func (s *IndexAdviseSkill) adviseBySQLID(ctx context.Context, sqlID string) (*skill.Result, error) {
	planResult, err := s.driver.Query(ctx, displayCursorSQL, sqlID)
	if err != nil {
		return nil, fmt.Errorf("fetching cursor plan failed: %w", err)
	}

	planLines := extractPlanLines(planResult)
	fullScans := findFullScans(planLines)

	var b strings.Builder
	b.WriteString("=== Execution Plan ===\n")
	b.WriteString(strings.Join(planLines, "\n"))
	b.WriteString("\n\n")

	// Attempt to get predicate info for more specific advice.
	var predicates []IndexAdvicePredicate
	predResult, predErr := s.driver.Query(ctx, fullScanPredicatesSQL, sqlID)
	if predErr == nil && len(predResult.Rows) > 0 {
		b.WriteString("=== Full Scan Predicates ===\n")
		for _, row := range predResult.Rows {
			filter := anyToString(row, 0)
			access := anyToString(row, 1)
			predicates = append(predicates, IndexAdvicePredicate{
				FilterPredicate: filter,
				AccessPredicate: access,
			})
			if filter != "" {
				b.WriteString(fmt.Sprintf("  Filter: %s\n", filter))
			}
			if access != "" {
				b.WriteString(fmt.Sprintf("  Access: %s\n", access))
			}
		}
		b.WriteByte('\n')
	}

	writeAdvice(&b, fullScans)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &IndexAdviceData{Mode: "sql_id", SQLID: sqlID, PlanLines: planLines, FullScans: fullScans, Predicates: predicates},
		Rendered: b.String(),
		Summary:  fmt.Sprintf("Index advice for SQL ID %s: %d full table scan(s) detected", sqlID, len(fullScans)),
		Metadata: map[string]string{
			"mode":       "sql_id",
			"sql_id":     sqlID,
			"full_scans": fmt.Sprintf("%d", len(fullScans)),
		},
	}, nil
}

// extractPlanLines converts a QueryResult into a slice of strings,
// using the first column of each row (standard DBMS_XPLAN output).
func extractPlanLines(qr *db.QueryResult) []string {
	if qr == nil || len(qr.Rows) == 0 {
		return []string{"(no plan output)"}
	}
	lines := make([]string, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		if len(row) > 0 && row[0] != nil {
			lines = append(lines, fmt.Sprint(row[0]))
		}
	}
	if len(lines) == 0 {
		return []string{"(no plan output)"}
	}
	return lines
}

// findFullScans searches plan lines for TABLE ACCESS FULL operations
// and returns the table names found.
func findFullScans(lines []string) []string {
	tables := make([]string, 0)
	for _, line := range lines {
		upper := strings.ToUpper(line)
		if !strings.Contains(upper, "TABLE ACCESS") || !strings.Contains(upper, "FULL") {
			continue
		}
		// Typical DBMS_XPLAN line:
		//   |   3 |  TABLE ACCESS FULL| ORDERS  |  1000 | ...
		// Extract the table name after the second pipe following "FULL".
		tableName := extractTableName(line)
		if tableName != "" {
			tables = append(tables, tableName)
		}
	}
	return tables
}

// extractTableName tries to pull a table name from a DBMS_XPLAN output line
// that contains TABLE ACCESS FULL. It looks for the segment after the
// "TABLE ACCESS FULL" text between pipe delimiters.
func extractTableName(line string) string {
	// Split by pipe and look for the field after TABLE ACCESS FULL.
	parts := strings.Split(line, "|")
	for i, p := range parts {
		upper := strings.ToUpper(strings.TrimSpace(p))
		if strings.Contains(upper, "TABLE ACCESS") && strings.Contains(upper, "FULL") {
			// The table name is typically in the next pipe-delimited field.
			if i+1 < len(parts) {
				name := strings.TrimSpace(parts[i+1])
				if name != "" {
					return name
				}
			}
		}
	}
	return ""
}

// writeAdvice outputs index recommendations based on the detected full scans.
func writeAdvice(b *strings.Builder, fullScans []string) {
	b.WriteString("=== Recommendations ===\n")
	if len(fullScans) == 0 {
		b.WriteString("No full table scans detected. The query plan looks efficient.\n")
		return
	}
	b.WriteString(fmt.Sprintf("%d full table scan(s) detected:\n", len(fullScans)))
	for _, table := range fullScans {
		b.WriteString(fmt.Sprintf("  - Table %s: Consider adding an index on columns used in WHERE/JOIN clauses\n", table))
	}
}

// anyToString safely extracts a string from a row at the given index.
func anyToString(row []any, idx int) string {
	if idx >= len(row) || row[idx] == nil {
		return ""
	}
	return fmt.Sprint(row[idx])
}
