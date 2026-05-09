/*-------------------------------------------------------------------------
 *
 * topsql.go
 *	  TopSQLRow represents a single top SQL entry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/topsql.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

type topsqlSortInfo struct {
	col   string
	label string
}

var topsqlSortCols = map[string]topsqlSortInfo{
	"el": {"elapsed_time DESC", "总执行时间"},
	"ae": {"elapsed_time/NULLIF(executions,0) DESC", "平均执行时间"},
	"lr": {"buffer_gets DESC", "总逻辑读"},
	"pr": {"disk_reads DESC", "总物理读"},
	"al": {"buffer_gets/NULLIF(executions,0) DESC", "平均逻辑读"},
	"ap": {"disk_reads/NULLIF(executions,0) DESC", "平均物理读"},
	"ex": {"executions DESC", "执行次数"},
}

// TopSQLRow represents a single top SQL entry.
type TopSQLRow struct {
	SQLID        string  `json:"sql_id"`
	ElapsedS     float64 `json:"elapsed_s"`
	Exec         int     `json:"exec"`
	AvgElapsedMs float64 `json:"avg_elapsed_ms"`
	LogicalReads int     `json:"logical_reads"`
	AvgLogical   int     `json:"avg_logical"`
	SQLText      string  `json:"sql_text"`
}

// TopSQLReport is the structured output for LLM consumption.
type TopSQLReport struct {
	Rows    []TopSQLRow `json:"rows"`
	Minutes int         `json:"minutes"`
	SortKey string      `json:"sort_key"`
	SortBy  string      `json:"sort_by"`
}

// TopSQLSkill shows top SQL statements by various metrics.
type TopSQLSkill struct {
	driver db.Driver
}

// NewTopSQLSkill creates a TopSQLSkill backed by the given driver.
func NewTopSQLSkill(driver db.Driver) *TopSQLSkill {
	return &TopSQLSkill{driver: driver}
}

func (s *TopSQLSkill) Name() string                      { return "topsql" }
func (s *TopSQLSkill) Description() string               { return "Show top SQL by elapsed time, reads, or executions" }
func (s *TopSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TopSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "topsql",
		Description: "Show top SQL by elapsed time, reads, or executions",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "[minutes] [sort_key: el|ae|lr|pr|al|ap|ex]",
				},
			},
		},
	}
}

func (s *TopSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "topsql",
		Aliases:        []string{"top"},
		Usage:          "/topsql [minutes] [sort_key]",
		Examples:       []string{"/topsql", "/topsql 30", "/topsql lr", "/topsql 60 pr"},
		ArgCompletions: []string{"el", "ae", "lr", "pr", "al", "ap", "ex"},
	}
}

func (s *TopSQLSkill) Validate(_ skill.Params) error { return nil }

func (s *TopSQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := params.StringOr("args", "")
	parts := strings.Fields(args)
	minutes := 10
	sortKey := "el"

	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			minutes = n
		} else if _, ok := topsqlSortCols[p]; ok {
			sortKey = p
		}
	}

	sortInfo := topsqlSortCols[sortKey]

	query := fmt.Sprintf(`SELECT sql_id,
       ROUND(elapsed_time/1000000, 1) AS elapsed_s,
       executions AS exec,
       ROUND(elapsed_time/NULLIF(executions,0)/1000000, 3) AS avg_elapsed_ms,
       buffer_gets AS logical_reads,
       ROUND(buffer_gets/NULLIF(executions,0)) AS avg_logical,
       SUBSTR(sql_text, 1, 80) AS sql_text
FROM v$sql
WHERE last_active_time > SYSDATE - :1/1440
AND executions > 0
ORDER BY %s
FETCH FIRST 20 ROWS ONLY`, sortInfo.col)

	result, err := s.driver.Query(ctx, query, minutes)
	if err != nil {
		return nil, fmt.Errorf("querying top SQL: %w", err)
	}

	// Collect sql_ids for batch FTS check.
	ftsSet := queryFTSSQLIDs(ctx, s.driver, result)

	// Build enhanced rendered output with Plan column and HumanNumber formatting.
	rendered := renderTopSQLWithPlan(result, ftsSet, minutes, sortInfo.label)

	summary := fmt.Sprintf("Top SQL (最近 %d 分钟, 按%s排序) — %d 条",
		minutes, sortInfo.label, len(result.Rows))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

// queryFTSSQLIDs performs a batch query against v$sql_plan to find sql_ids with full table scans.
// Returns a set of sql_id strings that have FTS.
func queryFTSSQLIDs(ctx context.Context, driver db.Driver, result *db.QueryResult) map[string]bool {
	ftsSet := make(map[string]bool)
	if result == nil || len(result.Rows) == 0 {
		return ftsSet
	}

	// Column 0 is sql_id.
	var sqlIDs []string
	for _, row := range result.Rows {
		if len(row) > 0 {
			if id, ok := row[0].(string); ok && id != "" {
				sqlIDs = append(sqlIDs, "'"+id+"'")
			}
		}
	}
	if len(sqlIDs) == 0 {
		return ftsSet
	}

	ftsQuery := fmt.Sprintf(
		`SELECT DISTINCT sql_id FROM v$sql_plan WHERE sql_id IN (%s) AND operation = 'TABLE ACCESS' AND options LIKE '%%FULL%%'`,
		strings.Join(sqlIDs, ","),
	)

	ftsResult, err := driver.Query(ctx, ftsQuery)
	if err != nil {
		return ftsSet // silently degrade — Plan column just shows IDX for all
	}
	for _, row := range ftsResult.Rows {
		if len(row) > 0 {
			if id, ok := row[0].(string); ok {
				ftsSet[id] = true
			}
		}
	}
	return ftsSet
}

// renderTopSQLWithPlan builds the enhanced rendered string with Plan column and human-formatted numbers.
func renderTopSQLWithPlan(result *db.QueryResult, ftsSet map[string]bool, minutes int, sortLabel string) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("Top SQL (最近 %d 分钟, 按%s) — 0 条", minutes, sortLabel)
	}

	// Build table lines: #, sql_id, elapsed_s, exec, avg_ms, logical_reads, avg_logical, Plan, sql_text
	var lines []string
	var ftsWarnings []string

	for i, row := range result.Rows {
		if len(row) < 7 {
			continue
		}
		sqlID := cellStr(row[0])
		elapsedS := cellStr(row[1])
		exec := cellStr(row[2])
		avgMs := cellStr(row[3])
		logicalReads := format.HumanNumber(cellFloat(row[4]))
		avgLogical := format.HumanNumber(cellFloat(row[5]))
		sqlText := cellStr(row[6])

		planText := "IDX   "
		if ftsSet[sqlID] {
			planText = "\033[33mFTS \u26a0\033[0m  "
			ftsWarnings = append(ftsWarnings, fmt.Sprintf("#%d %s", i+1, sqlID))
		}

		line := fmt.Sprintf(" %2d  %-14s %8s %8s %10s %10s %8s  %s%s",
			i+1, sqlID, elapsedS, exec, avgMs, logicalReads, avgLogical, planText, sqlText)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("Top SQL (最近 %d 分钟, 按%s) — %d 条", minutes, sortLabel, len(result.Rows))

	header := fmt.Sprintf(" %2s  %-14s %8s %8s %10s %10s %8s  %-6s %s",
		"#", "SQL_ID", "ELAPSED", "EXEC", "AVG_MS", "LOGICAL", "AVG_LR", "PLAN", "SQL_TEXT")

	sections := []format.PanelSection{
		{Lines: append([]string{header}, lines...)},
	}

	if len(ftsWarnings) > 0 {
		footer := fmt.Sprintf("\033[33m⚠ 全表扫描: %s\033[0m \033[90m— /explain <sql_id> 查看执行计划\033[0m",
			strings.Join(ftsWarnings, ", "))
		sections = append(sections, format.PanelSection{
			Header: "提示",
			Lines:  []string{footer},
		})
	}

	return format.Panel(title, sections)
}

// cellStr converts a row cell value to string.
func cellStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// cellFloat converts a row cell value to float64.
func cellFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	default:
		f, _ := strconv.ParseFloat(fmt.Sprintf("%v", v), 64)
		return f
	}
}
