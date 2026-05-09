/*-------------------------------------------------------------------------
 *
 * sql.go
 *	  SQLSkill executes arbitrary SQL statements. It is invoked by the
 *	  dispatcher when user input is classified as SQL rather than a
 *	  slash command.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/sql.go
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

// readOnlyPrefixes identifies statements that should use driver.Query.
var readOnlyPrefixes = []string{"SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "WITH"}

// SQLSkill executes arbitrary SQL statements. It is invoked by the dispatcher
// when user input is classified as SQL rather than a slash command.
type SQLSkill struct {
	driver db.Driver
}

// NewSQLSkill creates a SQLSkill backed by the given driver.
func NewSQLSkill(driver db.Driver) *SQLSkill {
	return &SQLSkill{driver: driver}
}

func (s *SQLSkill) Name() string        { return "sql" }
func (s *SQLSkill) Description() string  { return "Execute SQL statements" }

// SecurityLevel returns LevelReadOnly as the static default.
// The actual security check is performed by the dispatcher/executor
// using security.ClassifySQL before calling Execute.
func (s *SQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "sql",
		Description: "Execute SQL statements",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sql": map[string]any{
					"type":        "string",
					"description": "SQL statement(s) to execute",
				},
			},
			"required": []string{"sql"},
		},
	}
}

func (s *SQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sql",
		Usage:   "Type SQL directly (no slash command needed)",
	}
}

func (s *SQLSkill) Validate(params skill.Params) error {
	sqlText := strings.TrimSpace(params.StringOr("sql", ""))
	if sqlText == "" {
		return fmt.Errorf("sql parameter is required and must not be empty")
	}
	return nil
}

func (s *SQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	sqlText := strings.TrimSpace(params.StringOr("sql", ""))

	statements := splitSQL(sqlText)
	if len(statements) == 1 {
		return s.executeSingle(ctx, statements[0])
	}
	return s.executeMultiple(ctx, statements)
}

// executeSingle runs a single SQL statement and returns the appropriate result type.
func (s *SQLSkill) executeSingle(ctx context.Context, sqlText string) (*skill.Result, error) {
	if isReadOnly(sqlText) {
		return s.executeQuery(ctx, sqlText)
	}
	return s.executeExec(ctx, sqlText)
}

// executeQuery runs a read-only statement via driver.Query and returns a table result.
func (s *SQLSkill) executeQuery(ctx context.Context, sqlText string) (*skill.Result, error) {
	qr, err := s.driver.Query(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	summary := fmt.Sprintf("%d row(s) returned in %s", len(qr.Rows), qr.Duration)

	return &skill.Result{
		Type:    skill.ResultTable,
		Data:    qr,
		Summary: summary,
	}, nil
}

// executeExec runs a DML/DDL statement via driver.Exec and returns a text result.
func (s *SQLSkill) executeExec(ctx context.Context, sqlText string) (*skill.Result, error) {
	er, err := s.driver.Exec(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("exec failed: %w", err)
	}

	summary := fmt.Sprintf("%d row(s) affected in %s", er.RowsAffected, er.Duration)

	return &skill.Result{
		Type:    skill.ResultText,
		Data:    summary,
		Summary: summary,
	}, nil
}

// executeMultiple runs multiple statements sequentially and collects results.
func (s *SQLSkill) executeMultiple(ctx context.Context, statements []string) (*skill.Result, error) {
	var b strings.Builder
	for i, stmt := range statements {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		fmt.Fprintf(&b, "Statement %d: %s\n", i+1, truncateSQL(stmt, 80))

		result, err := s.executeSingle(ctx, stmt)
		if err != nil {
			fmt.Fprintf(&b, "Error: %s\n", err)
			continue
		}

		switch result.Type {
		case skill.ResultTable:
			qr, ok := result.Data.(*db.QueryResult)
			if ok {
				fmt.Fprintf(&b, "%d row(s) returned in %s\n", len(qr.Rows), qr.Duration)
			}
		case skill.ResultText:
			text := result.Rendered
			if text == "" {
				text, _ = result.Data.(string)
			}
			if text != "" {
				b.WriteString(text)
				b.WriteByte('\n')
			}
		}
	}

	return &skill.Result{
		Type:    skill.ResultText,
		Data:    b.String(),
		Summary: fmt.Sprintf("%d statement(s) executed", len(statements)),
	}, nil
}

// isReadOnly returns true if the SQL statement is a read-only query.
func isReadOnly(sqlText string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sqlText))
	for _, prefix := range readOnlyPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

// splitSQL splits a multi-statement SQL string on semicolons,
// filtering out empty segments.
func splitSQL(sqlText string) []string {
	parts := strings.Split(sqlText, ";")
	var result []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return []string{sqlText}
	}
	return result
}

// truncateSQL shortens a SQL string for display purposes.
func truncateSQL(sqlText string, maxLen int) string {
	// Collapse whitespace for display.
	collapsed := strings.Join(strings.Fields(sqlText), " ")
	if len(collapsed) <= maxLen {
		return collapsed
	}
	return collapsed[:maxLen-3] + "..."
}
