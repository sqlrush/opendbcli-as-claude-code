/*-------------------------------------------------------------------------
 *
 * gather.go
 *	  GatherSkill checks for tables needing ANALYZE and optionally runs
 *	  it.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/admin/gather.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const gatherCheckSQL = `SELECT
  schemaname,
  relname,
  n_live_tup,
  n_mod_since_analyze,
  CASE WHEN n_live_tup > 0
    THEN ROUND(n_mod_since_analyze::numeric / n_live_tup * 100, 1)
    ELSE 0
  END AS mod_pct,
  last_analyze::text,
  last_autoanalyze::text
FROM pg_stat_user_tables
WHERE n_mod_since_analyze > GREATEST(n_live_tup * 0.1, 50)
ORDER BY n_mod_since_analyze DESC
LIMIT 50`

// GatherSkill checks for tables needing ANALYZE and optionally runs it.
type GatherSkill struct {
	driver db.Driver
}

// NewGatherSkill creates a GatherSkill backed by the given driver.
func NewGatherSkill(driver db.Driver) *GatherSkill {
	return &GatherSkill{driver: driver}
}

func (s *GatherSkill) Name() string                       { return "gather" }
func (s *GatherSkill) Description() string                { return "分析表统计信息 (ANALYZE)" }
func (s *GatherSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelOperator }

func (s *GatherSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "gather",
		Usage:          "/gather [check|run|run <schema.table>]",
		ArgCompletions: []string{"check", "run"},
		Examples: []string{
			"/gather",
			"/gather check",
			"/gather run",
			"/gather run public.users",
		},
	}
}

func (s *GatherSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "gather",
		Description: "Check and run ANALYZE for PostgreSQL tables with stale statistics",
		Parameters: map[string]any{
			"args": map[string]any{
				"type":        "string",
				"description": "check (default) | run | run <schema.table>",
			},
		},
	}
}

func (s *GatherSkill) Validate(params skill.Params) error {
	args := strings.Fields(params.StringOr("args", "check"))
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "check", "run", "":
		return nil
	default:
		return fmt.Errorf("unknown action %q, use: check, run, run <schema.table>", args[0])
	}
}

func (s *GatherSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.Fields(params.StringOr("args", "check"))
	action := "check"
	if len(args) > 0 {
		action = args[0]
	}

	switch action {
	case "check", "":
		return s.check(ctx)
	case "run":
		if len(args) >= 2 {
			return s.analyzeOne(ctx, args[1])
		}
		return s.analyzeAll(ctx)
	default:
		return &skill.Result{Type: skill.ResultError, Summary: fmt.Sprintf("unknown action: %s", action)}, nil
	}
}

func (s *GatherSkill) check(ctx context.Context) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, gatherCheckSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "所有表统计信息均为最新",
			Summary:  "no tables need analyze",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("需 ANALYZE 的表 — %d 个 (使用 /gather run 执行)", len(result.Rows)),
		Summary:  fmt.Sprintf("%d tables need analyze, use /gather run", len(result.Rows)),
	}, nil
}

func (s *GatherSkill) analyzeOne(ctx context.Context, fullName string) (*skill.Result, error) {
	parts := strings.SplitN(fullName, ".", 2)
	if len(parts) != 2 {
		return &skill.Result{Type: skill.ResultError, Summary: "格式: /gather run schema.table"}, nil
	}
	schemaName := parts[0]
	tableName := parts[1]

	analyzeSQL := fmt.Sprintf("ANALYZE %s.%s", schemaName, tableName)
	_, err := s.driver.Exec(ctx, analyzeSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("已执行 ANALYZE %s.%s", schemaName, tableName),
		Summary:  fmt.Sprintf("analyzed %s.%s", schemaName, tableName),
	}, nil
}

func (s *GatherSkill) analyzeAll(ctx context.Context) (*skill.Result, error) {
	checkResult, err := s.driver.Query(ctx, gatherCheckSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(checkResult.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "所有表统计信息均为最新",
			Summary:  "no tables to analyze",
		}, nil
	}

	var succeeded, failed int
	var details []string

	for _, row := range checkResult.Rows {
		if len(row) < 2 {
			continue
		}
		schema := asStr(row[0])
		table := asStr(row[1])

		analyzeSQL := fmt.Sprintf("ANALYZE %s.%s", schema, table)
		_, analyzeErr := s.driver.Exec(ctx, analyzeSQL)
		if analyzeErr != nil {
			failed++
			details = append(details, fmt.Sprintf("  FAIL %s.%s: %v", schema, table, analyzeErr))
		} else {
			succeeded++
			details = append(details, fmt.Sprintf("  OK   %s.%s", schema, table))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "ANALYZE 完成: %d 成功, %d 失败\n\n", succeeded, failed)
	for _, d := range details {
		b.WriteString(d + "\n")
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("analyzed %d tables, %d failed", succeeded, failed),
	}, nil
}
