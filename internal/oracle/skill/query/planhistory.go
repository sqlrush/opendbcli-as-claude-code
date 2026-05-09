/*-------------------------------------------------------------------------
 *
 * planhistory.go
 *	  PlanHistoryRow represents a single AWR plan history entry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/planhistory.go
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

const planHistorySQL = `SELECT DISTINCT
       s.snap_id,
       TO_CHAR(sn.begin_interval_time, 'MM-DD HH24:MI') AS snap_time,
       s.plan_hash_value,
       ROUND(s.elapsed_time_delta/GREATEST(s.executions_delta,1)/1e6, 4) AS avg_elapsed_sec,
       s.executions_delta AS execs,
       ROUND(s.buffer_gets_delta/GREATEST(s.executions_delta,1)) AS avg_gets
FROM dba_hist_sqlstat s
JOIN dba_hist_snapshot sn ON sn.snap_id = s.snap_id AND sn.dbid = s.dbid AND sn.instance_number = s.instance_number
WHERE s.sql_id = :1
ORDER BY s.snap_id DESC
FETCH FIRST 30 ROWS ONLY`

const planTextSQL = `SELECT plan_table_output
FROM TABLE(DBMS_XPLAN.DISPLAY_AWR(:1, :2, NULL, 'BASIC'))`

// PlanHistoryRow represents a single AWR plan history entry.
type PlanHistoryRow struct {
	SnapID        string  `json:"snap_id"`
	SnapTime      string  `json:"snap_time"`
	PlanHashValue string  `json:"plan_hash_value"`
	AvgElapsedSec float64 `json:"avg_elapsed_sec"`
	Execs         int     `json:"execs"`
	AvgGets       int     `json:"avg_gets"`
}

// PlanHistoryReport is the structured output for LLM consumption.
type PlanHistoryReport struct {
	SQLID string           `json:"sql_id"`
	Rows  []PlanHistoryRow `json:"rows"`
}

// PlanHistorySkill shows SQL execution plan history from AWR snapshots.
type PlanHistorySkill struct {
	driver db.Driver
}

// NewPlanHistorySkill creates a PlanHistorySkill backed by the given driver.
func NewPlanHistorySkill(driver db.Driver) *PlanHistorySkill {
	return &PlanHistorySkill{driver: driver}
}

func (s *PlanHistorySkill) Name() string        { return "planhistory" }
func (s *PlanHistorySkill) Description() string  { return "Show SQL execution plan history from AWR" }
func (s *PlanHistorySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *PlanHistorySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "planhistory",
		Description: "Show SQL execution plan history from AWR snapshots to detect plan regressions",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "SQL ID to show plan history for",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *PlanHistorySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "planhistory",
		Aliases:  []string{"ph"},
		Usage:    "/planhistory <sql_id>",
		Examples: []string{"/planhistory abc123def", "/ph abc123def"},
	}
}

func (s *PlanHistorySkill) Validate(params skill.Params) error {
	sqlID := strings.TrimSpace(params.StringOr("args", ""))
	if sqlID == "" {
		return fmt.Errorf("planhistory requires a sql_id")
	}
	return nil
}

func (s *PlanHistorySkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	sqlID := strings.TrimSpace(params.StringOr("args", ""))

	result, err := s.driver.Query(ctx, planHistorySQL, sqlID)
	if err != nil {
		return nil, fmt.Errorf("querying plan history: %w", err)
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("SQL ID %s 无 AWR 执行计划历史", sqlID),
			Summary:  fmt.Sprintf("no history for %s", sqlID),
		}, nil
	}

	// Detect distinct plan hash values for regression summary.
	plans := make(map[string]struct{})
	for _, row := range result.Rows {
		if len(row) >= 3 {
			plans[phAsString(row[2])] = struct{}{}
		}
	}

	summary := fmt.Sprintf("执行计划历史: %s — %d 条, %d 个不同计划",
		sqlID, len(result.Rows), len(plans))

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("执行计划历史: %s — %d 个不同计划", sqlID, len(plans)),
		Summary:  summary,
		Metadata: map[string]string{
			"sql_id":         sqlID,
			"distinct_plans": fmt.Sprintf("%d", len(plans)),
		},
	}, nil
}

// phAsString safely converts an any value to string.
func phAsString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
