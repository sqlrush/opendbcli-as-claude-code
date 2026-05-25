/*-------------------------------------------------------------------------
 *
 * ash.go
 *	  ASHSkill provides an Active Session History approximation for
 *	  MySQL by sampling SHOW PROCESSLIST grouped by wait state.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/query/ash.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const ashSQL = `SELECT
  COMMAND,
  STATE,
  COUNT(*) AS sessions,
  GROUP_CONCAT(DISTINCT LEFT(INFO, 50) SEPARATOR '; ') AS sample_sql
FROM information_schema.PROCESSLIST
WHERE COMMAND != 'Sleep'
GROUP BY COMMAND, STATE
ORDER BY sessions DESC`

// ASHSkill provides an Active Session History approximation for MySQL
// by sampling SHOW PROCESSLIST grouped by wait state.
type ASHSkill struct {
	driver db.Driver
}

// NewASHSkill creates an ASHSkill backed by the given driver.
func NewASHSkill(driver db.Driver) *ASHSkill {
	return &ASHSkill{driver: driver}
}

func (s *ASHSkill) Name() string                       { return "ash" }
func (s *ASHSkill) Description() string                { return "活跃会话概览 (ASH)" }
func (s *ASHSkill) Category() string                   { return "query" }
func (s *ASHSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ASHSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/ash", Examples: []string{"/ash"}}
}

func (s *ASHSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "ash",
		Description: "Show active session snapshot grouped by command and state (MySQL ASH approximation)",
	}
}

func (s *ASHSkill) Validate(_ skill.Params) error { return nil }

func (s *ASHSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, ashSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无活跃会话",
			Summary:  "no active sessions",
		}, nil
	}

	// Build Panel lines.
	header := fmt.Sprintf(" %-12s %-20s %8s  %s", "COMMAND", "STATE", "SESSIONS", "SAMPLE_SQL")
	var lines []string
	lines = append(lines, header)
	for _, row := range result.Rows {
		cmd := mysqlASHStr(row, 0)
		state := mysqlASHStr(row, 1)
		if len(state) > 20 {
			state = state[:17] + "..."
		}
		sessions := mysqlASHStr(row, 2)
		sqlText := mysqlASHStr(row, 3)
		if len(sqlText) > 50 {
			sqlText = sqlText[:47] + "..."
		}
		lines = append(lines, fmt.Sprintf(" %-12s %-20s %8s  %s", cmd, state, sessions, sqlText))
	}

	sections := []format.PanelSection{
		{Lines: lines},
	}
	rendered := format.Panel("活跃会话概览 (MySQL ASH)", sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("活跃会话: %d 个状态分组", len(result.Rows)),
	}, nil
}

// mysqlASHStr safely extracts a string from a row at the given index.
func mysqlASHStr(row []any, idx int) string {
	if idx >= len(row) || row[idx] == nil {
		return ""
	}
	return fmt.Sprintf("%v", row[idx])
}
