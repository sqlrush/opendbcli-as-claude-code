/*-------------------------------------------------------------------------
 *
 * sqltune_skill.go
 *	  /sqltune skill for GaussDB Centralized — routes through neutral
 *	  sqltune.BuildTuner(DialectGaussDB) so the GaussDB-specific
 *	  planner picks up GS_PLAN_TRACE on top of the inherited og
 *	  capabilities (EXPLAIN PERFORMANCE + pg_stats).
 *
 *	  Why this exists separately from og's SQLTuneSkill:
 *	    og's skill hard-codes BuildTuner(DialectOpenGauss) which would
 *	    route a GaussDB connection through og's planner — missing the
 *	    GS_PLAN_TRACE capability. This skill exists solely to flip the
 *	    DialectKind argument; everything else (params, error handling,
 *	    rendering) mirrors og's skill.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/skill/query/sqltune_skill.go
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
	_ "github.com/sqlrush/opendb/internal/gaussdb/sqltuner" // register gaussdb planner + tuner factory
	"github.com/sqlrush/opendb/internal/model"
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

func (s *SQLTuneSkill) Name() string        { return "sqltune" }
func (s *SQLTuneSkill) Description() string { return "GaussDB SQL 性能调优 (EXPLAIN PERFORMANCE + GS_PLAN_TRACE + pg_stats)" }
func (s *SQLTuneSkill) Category() string    { return "query" }
func (s *SQLTuneSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelReadOnly
}

func (s *SQLTuneSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sqltune",
		Aliases: []string{"sqlt"},
		Usage:   "/sqltune <SQL>",
		Description: "GaussDB Centralized SQL 调优。在 og 基础上额外采集 GS_PLAN_TRACE " +
			"(真正的 CBO 决策 dump，需 sysadmin 权限且 plan_trace 已启用)。",
		Examples: []string{
			"/sqltune SELECT * FROM orders WHERE uid = 12345",
		},
	}
}

func (s *SQLTuneSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqltune",
		Description: "Tune a SQL on GaussDB Centralized. Collects EXPLAIN (FORMAT JSON, ANALYZE) plan, " +
			"EXPLAIN PERFORMANCE per-operator detail, pg_stats sidecar, and (if available) GS_PLAN_TRACE " +
			"CBO decision dump — the only true CBO trace in the PG family.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Full SQL text (no unbound $N / ? placeholders)",
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

	// LLM is required for sqltune (Round 1 mega-analysis). Same gate as og.
	if s.modelMgr == nil || s.modelMgr.Provider() == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "  /sqltune 需要配置 LLM 模型 (active_model). 当前无可用 model.",
		}, nil
	}

	mode := strings.TrimSpace(params.StringOr("mode", "quick"))
	engine, err := sqltune.BuildTuner(sqltune.DialectGaussDB, sqltune.TunerDeps{
		Driver:   s.driver,
		Provider: s.modelMgr.Provider(),
		MemStore: s.memStore,
	})
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqltune 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}
	report, err := engine.Tune(ctx, sqltune.TuneOptions{
		SQL:         sqlText,
		Verify:      true,
		QuickMode:   mode != "deep",
		SkipUpgrade: mode != "deep",
		ForceDeep:   mode == "deep",
	})
	if err != nil {
		var pe *sqltune.PlaceholderError
		if errors.As(err, &pe) {
			msg := fmt.Sprintf(
				"  ⚠️ /sqltune 输入 SQL 含 %d 个未绑定占位符 %v (kind=%s)\n"+
					"     GaussDB 归一化 SQL 来自 dbe_perf.statement (无字面量)。\n"+
					"     请从 dbe_perf.statement_history (带字面量) 取，或手动替换 $1/? 后重试。",
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

	summary := "GaussDB sqltune"
	if report.Stats != nil {
		summary = fmt.Sprintf("%d candidates, %d verified, best %.1f×, %s",
			report.Stats.CandidateCount, report.Stats.VerifiedCount,
			report.Stats.BestSpeedup, report.Stats.TotalDuration.Round(1e6))
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     report.Stats,
		Rendered: report.Markdown,
		Summary:  summary,
	}, nil
}
