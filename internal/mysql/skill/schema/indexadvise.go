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
 *	  internal/mysql/skill/schema/indexadvise.go
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

const explainJSONTemplate = "EXPLAIN FORMAT=JSON %s"

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
func (s *IndexAdviseSkill) Category() string                   { return "schema" }
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
		Description: "Suggest index improvements based on EXPLAIN analysis of a SQL query",
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

	// Run EXPLAIN FORMAT=JSON for detailed plan info.
	explainSQL := fmt.Sprintf(explainJSONTemplate, sqlText)
	jsonResult, err := s.driver.Query(ctx, explainSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Data: err.Error(), Summary: err.Error()}, nil
	}

	jsonText := extractJSONText(jsonResult)

	// Also run regular EXPLAIN to get access_type info.
	regularResult, err := s.driver.Query(ctx, "EXPLAIN "+sqlText)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Data: err.Error(), Summary: err.Error()}, nil
	}

	advice := analyzeExplainResult(regularResult, jsonText)

	rendered := buildAdvicePanel(sqlText, advice)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  fmt.Sprintf("index advice: %d suggestions", len(advice)),
	}, nil
}

// adviceItem represents a single index recommendation.
type adviceItem struct {
	table   string
	issue   string
	suggest string
}

func extractJSONText(result *db.QueryResult) string {
	if len(result.Rows) == 0 {
		return ""
	}
	for _, row := range result.Rows {
		for _, col := range row {
			s := asStr(col)
			if strings.Contains(s, "query_block") {
				return s
			}
		}
	}
	return ""
}

func analyzeExplainResult(result *db.QueryResult, jsonText string) []adviceItem {
	var advice []adviceItem

	// Find column indexes for regular EXPLAIN output.
	colIdx := make(map[string]int)
	for i, col := range result.Columns {
		colIdx[strings.ToLower(col)] = i
	}

	for _, row := range result.Rows {
		table := getCol(row, colIdx, "table")
		accessType := strings.ToUpper(getCol(row, colIdx, "type"))
		possibleKeys := getCol(row, colIdx, "possible_keys")
		key := getCol(row, colIdx, "key")
		extra := getCol(row, colIdx, "extra")
		rowsExamined := getCol(row, colIdx, "rows")

		switch accessType {
		case "ALL":
			suggestion := fmt.Sprintf("Consider adding an index on `%s`", table)
			if possibleKeys == "" {
				suggestion += " - no candidate keys found; add index on WHERE/JOIN columns"
			} else {
				suggestion += fmt.Sprintf(" (candidates: %s, but not used)", possibleKeys)
			}
			advice = append(advice, adviceItem{
				table:   table,
				issue:   fmt.Sprintf("Full Table Scan (type=ALL, rows=%s)", rowsExamined),
				suggest: suggestion,
			})
		case "INDEX":
			advice = append(advice, adviceItem{
				table:   table,
				issue:   "Full Index Scan (type=INDEX) - scanning entire index",
				suggest: "Add more selective index or add WHERE clause columns to existing index",
			})
		}

		if key == "" && possibleKeys != "" {
			advice = append(advice, adviceItem{
				table:   table,
				issue:   fmt.Sprintf("Available keys not used: %s", possibleKeys),
				suggest: "Check if optimizer hints or query rewrite would help use existing indexes",
			})
		}

		if strings.Contains(extra, "Using filesort") {
			advice = append(advice, adviceItem{
				table:   table,
				issue:   "Filesort detected (extra: Using filesort)",
				suggest: "Add ORDER BY columns to index to avoid filesort",
			})
		}

		if strings.Contains(extra, "Using temporary") {
			advice = append(advice, adviceItem{
				table:   table,
				issue:   "Temporary table used (extra: Using temporary)",
				suggest: "Optimize GROUP BY / DISTINCT columns; consider composite index",
			})
		}
	}

	// Check JSON plan for additional hints.
	if strings.Contains(jsonText, "\"access_type\": \"ALL\"") && len(advice) == 0 {
		advice = append(advice, adviceItem{
			table:   "(from JSON plan)",
			issue:   "Full table scan detected in JSON plan",
			suggest: "Add index on columns used in WHERE clause",
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

func getCol(row []any, colIdx map[string]int, name string) string {
	idx, ok := colIdx[name]
	if !ok || idx >= len(row) {
		return ""
	}
	return asStr(row[idx])
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
