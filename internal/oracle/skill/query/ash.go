/*-------------------------------------------------------------------------
 *
 * ash.go
 *	  ASHReport is the structured output for LLM consumption.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/ash.go
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

const ashTopSQLQuery = `SELECT sql_id, COUNT(*) AS samples,
       ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) AS pct,
       MAX(sql_plan_hash_value) AS plan_hash,
       TO_CHAR(MIN(sample_time), 'HH24:MI:SS') AS first_seen,
       TO_CHAR(MAX(sample_time), 'HH24:MI:SS') AS last_seen
FROM v$active_session_history
WHERE sample_time > SYSDATE - :1/1440 AND sql_id IS NOT NULL
GROUP BY sql_id ORDER BY COUNT(*) DESC
FETCH FIRST 15 ROWS ONLY`

const ashTopWaitsQuery = `SELECT NVL(event, 'ON CPU') AS event, wait_class,
       COUNT(*) AS samples,
       ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) AS pct
FROM v$active_session_history
WHERE sample_time > SYSDATE - :1/1440
GROUP BY event, wait_class ORDER BY COUNT(*) DESC
FETCH FIRST 15 ROWS ONLY`

const ashSQLProfileQuery = `SELECT NVL(event, 'ON CPU') AS event, COUNT(*) AS samples,
       ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) AS pct,
       COUNT(DISTINCT session_id) AS sessions
FROM v$active_session_history
WHERE sql_id = :1 AND sample_time > SYSDATE - 30/1440
GROUP BY event ORDER BY COUNT(*) DESC`

type ASHTopSQLItem struct {
	SQLID     string `json:"sql_id"`
	Samples   int    `json:"samples"`
	Pct       string `json:"pct"`
	PlanHash  string `json:"plan_hash"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

type ASHTopWaitItem struct {
	Event     string `json:"event"`
	WaitClass string `json:"wait_class"`
	Samples   int    `json:"samples"`
	Pct       string `json:"pct"`
}

type ASHSQLProfileItem struct {
	Event    string `json:"event"`
	Samples  int    `json:"samples"`
	Pct      string `json:"pct"`
	Sessions int    `json:"sessions"`
}

// ASHReport is the structured output for LLM consumption.
type ASHReport struct {
	Mode     string              `json:"mode"`
	Minutes  int                 `json:"minutes,omitempty"`
	SQLID    string              `json:"sql_id,omitempty"`
	TopSQL   []ASHTopSQLItem     `json:"top_sql,omitempty"`
	TopWaits []ASHTopWaitItem    `json:"top_waits,omitempty"`
	Profile  []ASHSQLProfileItem `json:"profile,omitempty"`
}

// ASHSkill shows ASH (Active Session History) analysis.
type ASHSkill struct{ driver db.Driver }

func NewASHSkill(driver db.Driver) *ASHSkill { return &ASHSkill{driver: driver} }

func (s *ASHSkill) Name() string                      { return "ash" }
func (s *ASHSkill) Description() string               { return "Show ASH analysis — top SQL and wait events" }
func (s *ASHSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ASHSkill) Validate(_ skill.Params) error      { return nil }

func (s *ASHSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "ash",
		Description: "Show ASH analysis — top SQL and wait events from active session history",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "[minutes] or sql <sql_id>",
				},
			},
		},
	}
}

func (s *ASHSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "ash",
		Usage:          "/ash [minutes] | /ash sql <sql_id>",
		Examples:       []string{"/ash", "/ash 30", "/ash sql abc123def"},
		ArgCompletions: []string{"sql"},
	}
}

func (s *ASHSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := params.StringOr("args", "")
	parts := strings.Fields(args)

	if len(parts) >= 2 && strings.EqualFold(parts[0], "sql") {
		return s.executeSQLProfile(ctx, parts[1])
	}

	minutes := 5
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			minutes = n
		}
	}
	return s.executeOverview(ctx, minutes)
}

func (s *ASHSkill) executeOverview(ctx context.Context, minutes int) (*skill.Result, error) {
	sqlResult, err := s.driver.Query(ctx, ashTopSQLQuery, minutes)
	if err != nil {
		return nil, fmt.Errorf("querying ASH top SQL: %w", err)
	}
	waitResult, err := s.driver.Query(ctx, ashTopWaitsQuery, minutes)
	if err != nil {
		return nil, fmt.Errorf("querying ASH top waits: %w", err)
	}

	topSQL := make([]ASHTopSQLItem, 0, len(sqlResult.Rows))
	for _, row := range sqlResult.Rows {
		topSQL = append(topSQL, ASHTopSQLItem{
			SQLID: asString(row[0]), Samples: ashInt(row[1]), Pct: asString(row[2]),
			PlanHash: asString(row[3]), FirstSeen: asString(row[4]), LastSeen: asString(row[5]),
		})
	}
	topWaits := make([]ASHTopWaitItem, 0, len(waitResult.Rows))
	for _, row := range waitResult.Rows {
		topWaits = append(topWaits, ASHTopWaitItem{
			Event: asString(row[0]), WaitClass: asString(row[1]),
			Samples: ashInt(row[2]), Pct: asString(row[3]),
		})
	}

	report := ASHReport{Mode: "overview", Minutes: minutes, TopSQL: topSQL, TopWaits: topWaits}
	rendered := ashRenderOverview(topSQL, topWaits, minutes)
	return &skill.Result{
		Type: skill.ResultText, Data: &report, Rendered: rendered,
		Summary: fmt.Sprintf("ASH 分析 (最近 %d 分钟) — %d SQL, %d 等待事件", minutes, len(topSQL), len(topWaits)),
	}, nil
}

func (s *ASHSkill) executeSQLProfile(ctx context.Context, sqlID string) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, ashSQLProfileQuery, sqlID)
	if err != nil {
		return nil, fmt.Errorf("querying ASH SQL profile: %w", err)
	}
	profile := make([]ASHSQLProfileItem, 0, len(result.Rows))
	for _, row := range result.Rows {
		profile = append(profile, ASHSQLProfileItem{
			Event: asString(row[0]), Samples: ashInt(row[1]),
			Pct: asString(row[2]), Sessions: ashInt(row[3]),
		})
	}

	report := ASHReport{Mode: "sql_profile", SQLID: sqlID, Profile: profile}
	rendered := ashRenderSQLProfile(profile, sqlID)
	return &skill.Result{
		Type: skill.ResultText, Data: &report, Rendered: rendered,
		Summary: fmt.Sprintf("ASH SQL Profile: %s — %d 等待事件", sqlID, len(profile)),
	}, nil
}

func ashRenderOverview(topSQL []ASHTopSQLItem, topWaits []ASHTopWaitItem, minutes int) string {
	// Build Top SQL section lines.
	var sqlLines []string
	if len(topSQL) == 0 {
		sqlLines = []string{" (无数据)"}
	} else {
		sqlHeader := fmt.Sprintf(" %-14s %8s %6s  %-12s  %s", "SQL_ID", "SAMPLES", "PCT%", "PLAN_HASH", "LAST_SEEN")
		sqlLines = append(sqlLines, sqlHeader)
		for _, r := range topSQL {
			sqlLines = append(sqlLines, fmt.Sprintf(" %-14s %8d %5s%%  %-12s  %s",
				r.SQLID, r.Samples, r.Pct, r.PlanHash, r.LastSeen))
		}
	}

	// Build Top Wait Events section lines with percentage bars.
	var waitLines []string
	if len(topWaits) == 0 {
		waitLines = []string{" (无数据)"}
	} else {
		waitHeader := fmt.Sprintf(" %-30s %-14s %8s %6s  %s", "EVENT", "WAIT_CLASS", "SAMPLES", "PCT%", "")
		waitLines = append(waitLines, waitHeader)
		for _, r := range topWaits {
			event := r.Event
			if len(event) > 30 {
				event = event[:27] + "..."
			}
			pctVal := ashParseFloat(r.Pct)
			bar := format.ProgressBar(pctVal, 16)
			waitLines = append(waitLines, fmt.Sprintf(" %-30s %-14s %8d %5s%%  %s",
				event, r.WaitClass, r.Samples, r.Pct, bar))
		}
	}

	// Compute total samples for summary.
	totalSamples := 0
	for _, r := range topSQL {
		totalSamples += r.Samples
	}

	sections := []format.PanelSection{
		{Header: "Top SQL (by samples)", Lines: sqlLines},
		{Header: "Top Wait Events (by samples)", Lines: waitLines},
	}

	title := fmt.Sprintf("ASH 分析 (最近 %d 分钟)", minutes)
	return format.Panel(title, sections)
}

func ashRenderSQLProfile(profile []ASHSQLProfileItem, sqlID string) string {
	if len(profile) == 0 {
		sections := []format.PanelSection{
			{Lines: []string{" (无数据)"}},
		}
		return format.Panel(fmt.Sprintf("ASH SQL Profile: %s (最近 30 分钟)", sqlID), sections)
	}

	header := fmt.Sprintf(" %-30s %8s %6s  %8s  %s", "EVENT", "SAMPLES", "PCT%", "SESSIONS", "")
	var lines []string
	lines = append(lines, header)
	for _, r := range profile {
		event := r.Event
		if len(event) > 30 {
			event = event[:27] + "..."
		}
		pctVal := ashParseFloat(r.Pct)
		bar := format.ProgressBar(pctVal, 16)
		lines = append(lines, fmt.Sprintf(" %-30s %8d %5s%%  %8d  %s",
			event, r.Samples, r.Pct, r.Sessions, bar))
	}

	sections := []format.PanelSection{
		{Lines: lines},
	}
	return format.Panel(fmt.Sprintf("ASH SQL Profile: %s (最近 30 分钟)", sqlID), sections)
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func ashInt(v any) int {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return int(n)
	case float64:
		return int(n)
	case int:
		return n
	default:
		i, _ := strconv.Atoi(fmt.Sprintf("%v", v))
		return i
	}
}

// ashParseFloat parses a percentage string like "45.2" to float64.
func ashParseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
