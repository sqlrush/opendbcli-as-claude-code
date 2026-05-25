/*-------------------------------------------------------------------------
 *
 * ash.go
 *	  ASHSkill provides an Active Session History approximation using
 *	  pg_stat_activity snapshots.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/query/ash.go
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

const ashSQL = `SELECT
  COALESCE(wait_event_type, 'CPU') AS wait_type,
  COALESCE(wait_event, 'On CPU') AS wait_event,
  COUNT(*) AS sessions,
  ROUND(COUNT(*)::numeric / NULLIF(SUM(COUNT(*)) OVER (), 0) * 100, 1) AS pct
FROM pg_stat_activity
WHERE state = 'active'
  AND backend_type = 'client backend'
  AND pid != pg_backend_pid()
GROUP BY wait_event_type, wait_event
ORDER BY sessions DESC`

// ASHSkill provides an Active Session History approximation using pg_stat_activity snapshots.
type ASHSkill struct{ driver db.Driver }

// NewASHSkill creates an ASHSkill backed by the given driver.
func NewASHSkill(driver db.Driver) *ASHSkill { return &ASHSkill{driver: driver} }

func (s *ASHSkill) Name() string                       { return "ash" }
func (s *ASHSkill) Description() string                { return "活跃会话快照 (ASH 近似)" }
func (s *ASHSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ASHSkill) Validate(_ skill.Params) error      { return nil }
func (s *ASHSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/ash"} }
func (s *ASHSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "ash", Description: "Active Session History approximation: group active sessions by wait event type and event"}
}

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

	// Build Panel lines with percentage bars.
	header := fmt.Sprintf(" %-14s %-20s %8s %6s  %s", "WAIT_TYPE", "WAIT_EVENT", "SESSIONS", "PCT%", "")
	var lines []string
	lines = append(lines, header)
	for _, row := range result.Rows {
		waitType := pgASHStr(row, 0)
		waitEvent := pgASHStr(row, 1)
		if len(waitEvent) > 20 {
			waitEvent = waitEvent[:17] + "..."
		}
		sessions := pgASHStr(row, 2)
		pctStr := pgASHStr(row, 3)
		pctVal := pgASHParseFloat(pctStr)
		bar := format.ProgressBar(pctVal, 16)
		lines = append(lines, fmt.Sprintf(" %-14s %-20s %8s %5s%%  %s",
			waitType, waitEvent, sessions, pctStr, bar))
	}

	sections := []format.PanelSection{
		{Lines: lines},
	}
	rendered := format.Panel("活跃会话快照 (PostgreSQL ASH)", sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("活跃会话: %d 个等待事件分组", len(result.Rows)),
	}, nil
}

// pgASHStr safely extracts a string from a row at the given index.
func pgASHStr(row []any, idx int) string {
	if idx >= len(row) || row[idx] == nil {
		return ""
	}
	return fmt.Sprintf("%v", row[idx])
}

// pgASHParseFloat parses a percentage string to float64.
func pgASHParseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
