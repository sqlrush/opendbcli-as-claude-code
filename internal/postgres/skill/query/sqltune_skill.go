/*-------------------------------------------------------------------------
 *
 * sqltune_skill.go
 *	  /sqltune skill for PostgreSQL — bridges through neutral
 *	  sqltune.BuildTuner(DialectPostgreSQL). The PG planner + tuner
 *	  factory register via internal/postgres/sqltuner init().
 *
 *	  M3 MVP scope mirrors MySQL M2: deterministic Phase A output (no
 *	  LLM orchestration yet). Headline value for PG vs other dialects:
 *	  the report includes the **pg_stats sidecar** for every involved
 *	  column. Since PG has no CBO trace, this is the best raw data we
 *	  can give a DBA (or LLM) to understand the planner's decisions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/query/sqltune_skill.go
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
	_ "github.com/sqlrush/opendb/internal/postgres/sqltuner" // register pg planner + tuner factory
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
func (s *SQLTuneSkill) Description() string                { return "PostgreSQL SQL 性能调优 (EXPLAIN JSON + pg_stats 旁路)" }
func (s *SQLTuneSkill) Category() string                   { return "query" }
func (s *SQLTuneSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLTuneSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sqltune",
		Aliases: []string{"sqlt"},
		Usage:   "/sqltune <SQL>",
		Description: "采集 PG EXPLAIN FORMAT=JSON 计划 + pg_stats 列统计旁路 + 关键 GUC. " +
			"PG 开源无 CBO trace, pg_stats 是 LLM 推断 planner 决策的核心数据. M3 MVP 不含 LLM 综合分析.",
		Examples: []string{
			"/sqltune SELECT * FROM orders WHERE uid = 12345",
			"/sqltune <粘贴你的复杂 SQL>",
		},
	}
}

func (s *SQLTuneSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqltune",
		Description: "Collect PostgreSQL EXPLAIN (FORMAT JSON, ANALYZE, BUFFERS, VERBOSE) plan + " +
			"pg_stats sidecar (n_distinct/null_frac/correlation/most_common_vals) for each involved column + " +
			"key CBO GUC (work_mem/random_page_cost/effective_cache_size). " +
			"PG has no CBO decision dump (no equivalent to Oracle 10053 / MySQL optimizer_trace) — pg_stats is " +
			"how the LLM can infer planner reasoning. Returns markdown report.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Full SQL text (must be EXPLAIN-able — no unbound $N or ? placeholders)",
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

	engine, err := sqltune.BuildTuner(sqltune.DialectPostgreSQL, deps)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqltune 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}
	report, err := engine.Tune(ctx, sqltune.TuneOptions{SQL: sqlText, Verify: false})
	if err != nil {
		var pe *sqltune.PlaceholderError
		if errors.As(err, &pe) {
			// PG-specific guidance: pg_stat_statements gives normalized
			// (no literal) form. Real literal SQL usually only via
			// auto_explain log or application log.
			msg := fmt.Sprintf(
				"  ⚠️ /sqltune 输入 SQL 含 %d 个未绑定占位符 %v (kind=%s)\n"+
					"     PG 归一化 SQL 来自 pg_stat_statements 没有字面量。\n"+
					"     可选恢复路径：\n"+
					"       1. 从应用日志拉真实 SQL\n"+
					"       2. 开启 auto_explain.log_min_duration + log_format=json，从 server log 拉\n"+
					"       3. 手动把 $1/? 替换成代表值后重试\n",
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

	summary := "MVP report (EXPLAIN+pg_stats)"
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
