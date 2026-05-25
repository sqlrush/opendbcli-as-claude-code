/*-------------------------------------------------------------------------
 *
 * sqltune_skill.go
 *	  /sqltune skill for MySQL — bridges through neutral
 *	  sqltune.BuildTuner(DialectMySQL). The MySQL planner + tuner
 *	  factory register via internal/mysql/sqltuner package init.
 *
 *	  M2.1 scope: this skill is intentionally MVP. The MySQL tuner
 *	  currently runs Phase A (collect EXPLAIN JSON + optimizer_trace
 *	  + dialect snapshot + schema basics) and returns the data as
 *	  deterministic markdown. LLM-driven Round 1/Round 2 orchestration
 *	  lands when og's tuner moves to the neutral package.
 *
 *	  Headline capability for MVP: optimizer_trace capture. Users
 *	  paste a SQL, get back the full CBO decision JSON which is the
 *	  closest thing to Oracle 10053 anywhere in the SQL world.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/query/sqltune_skill.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/model"
	_ "github.com/sqlrush/opendb/internal/mysql/sqltuner" // register mysql planner + tuner factory
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/sqltune"
)

type SQLTuneSkill struct {
	driver   db.Driver
	modelMgr *model.Manager
	memStore *memory.Store
}

func NewSQLTuneSkill(driver db.Driver, modelMgr *model.Manager, memStore *memory.Store) *SQLTuneSkill {
	return &SQLTuneSkill{driver: driver, modelMgr: modelMgr, memStore: memStore}
}

func (s *SQLTuneSkill) Name() string                       { return "sqltune" }
func (s *SQLTuneSkill) Description() string                { return "MySQL SQL 性能调优 (EXPLAIN JSON + optimizer_trace 采集)" }
func (s *SQLTuneSkill) Category() string                   { return "query" }
func (s *SQLTuneSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLTuneSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sqltune",
		Aliases: []string{"sqlt"},
		Usage:   "/sqltune <SQL>",
		Description: "采集 MySQL CBO 决策跟踪 (optimizer_trace) + EXPLAIN FORMAT=JSON, " +
			"输出原始 trace + 执行计划 + 关键参数. M2 MVP 不含 LLM 综合分析.",
		Examples: []string{
			"/sqltune SELECT * FROM orders WHERE uid = 12345",
			"/sqltune <粘贴你的复杂 SQL>",
		},
	}
}

func (s *SQLTuneSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqltune",
		Description: "Collect MySQL optimizer_trace (CBO decision JSON, closest analog to Oracle 10053) " +
			"plus EXPLAIN FORMAT=JSON plan for a given SQL. Returns raw trace + structured plan + " +
			"dialect parameters. Useful when you need to understand WHY the optimizer chose a particular plan. " +
			"Returns markdown report; LLM consumers can read the trace JSON inline.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Full SQL text (must be EXPLAIN-able — no unbound ? placeholders)",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *SQLTuneSkill) Validate(params skill.Params) error {
	if strings.TrimSpace(params.StringOr("args", "")) == "" {
		return fmt.Errorf("需要提供 SQL")
	}
	return nil
}

func (s *SQLTuneSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	sqlText := strings.TrimSpace(params.StringOr("args", ""))
	if sqlText == "" {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "  /sqltune 需要提供 SQL — 用法: /sqltune <SQL>",
		}, nil
	}

	deps := sqltune.TunerDeps{Driver: s.driver}
	if s.modelMgr != nil {
		deps.Provider = s.modelMgr.Provider()
	}
	if s.memStore != nil {
		deps.MemStore = s.memStore
	}

	engine, err := sqltune.BuildTuner(sqltune.DialectMySQL, deps)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqltune 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}
	report, err := engine.Tune(ctx, sqltune.TuneOptions{SQL: sqlText, Verify: false})
	if err != nil {
		// Placeholder error gets a targeted hint pointing at MySQL's
		// performance_schema (not og's dbe_perf — wrong DB!).
		var pe *sqltune.PlaceholderError
		if errors.As(err, &pe) {
			msg := fmt.Sprintf(
				"  ⚠️ /sqltune 输入 SQL 含 %d 个未绑定占位符 %v (kind=%s)\n"+
					"     MySQL 归一化 SQL 来自 performance_schema.events_statements_summary_by_digest，无字面量无法 EXPLAIN。\n"+
					"     请从 events_statements_history_long 拉带字面量的样本：\n"+
					"       SELECT SQL_TEXT FROM performance_schema.events_statements_history_long\n"+
					"        WHERE DIGEST = '<digest>' AND SQL_TEXT IS NOT NULL\n"+
					"        ORDER BY TIMER_END DESC LIMIT 1;",
				len(pe.Placeholders), pe.Placeholders, pe.DetectedKind)
			return &skill.Result{
				Type:     skill.ResultError,
				Rendered: msg,
				Summary:  pe.Error(),
			}, nil
		}
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqltune 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}

	summary := "MVP report (trace+EXPLAIN)"
	if report.Stats != nil {
		summary = fmt.Sprintf("%s (%s)", summary, report.Stats.TotalDuration.Round(1e6))
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     report.Stats,
		Rendered: report.Markdown,
		Summary:  summary,
	}, nil
}
