/*-------------------------------------------------------------------------
 *
 * sqltune_skill.go
 *	  SQLTuneSkill provides complex SQL performance tuning for
 *	  OpenGauss. See docs/sqltune/design-og-sqltuner.md for design.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/sqltune_skill.go
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
	"github.com/sqlrush/opendb/internal/opengauss/sqltuner"
	"github.com/sqlrush/opendb/internal/skill"
)

// SQLTuneSkill provides complex SQL performance tuning for OpenGauss.
// See docs/sqltune/design-og-sqltuner.md for design.
type SQLTuneSkill struct {
	driver   db.Driver
	modelMgr *model.Manager
	memStore *memory.Store
}

// NewSQLTuneSkill constructs a /sqltune skill.
// memStore may be nil — tuner gracefully skips memory injection.
func NewSQLTuneSkill(driver db.Driver, modelMgr *model.Manager, memStore *memory.Store) *SQLTuneSkill {
	return &SQLTuneSkill{
		driver:   driver,
		modelMgr: modelMgr,
		memStore: memStore,
	}
}

func (s *SQLTuneSkill) Name() string                       { return "sqltune" }
func (s *SQLTuneSkill) Description() string                { return "复杂 SQL 性能调优 (LLM + 真实数据 + 验证闭环)" }
func (s *SQLTuneSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SQLTuneSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqltune",
		Description: "Analyze a SQL with EXPLAIN + schema + dialect + runtime context, run LLM-driven tuning to produce 5-dimension optimization proposals (rewrite/index/hint/schema/stats), each verified by EXPLAIN cost diff and equivalence check. " +
			"Default mode is 'quick' (Round 1 + verify only, ~30-90s) which is what most LLMs should use. " +
			"Use mode='deep' only when quick mode's report is insufficient — deep adds Round 2 LLM polish + auto-upgrade iterative refinement, costs 5-15min.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Full SQL text to tune (must be EXPLAIN-able — no unbound ? placeholders)",
				},
				"mode": map[string]any{
					"type":        "string",
					"enum":        []string{"quick", "deep"},
					"description": "Optional. 'quick' (default, ~30-90s) skips Round 2 LLM and auto-upgrade. 'deep' (~5-15min) runs full pipeline. Prefer 'quick' for first attempt.",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *SQLTuneSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "sqltune",
		Aliases:     []string{"sqlt"},
		Usage:       "/sqltune <SQL>",
		Description: "复杂 SQL 性能调优 (5 维度方案 + EXPLAIN 验证 + 等价性检查)",
		Examples: []string{
			"/sqltune SELECT count(*) FROM orders WHERE uid = 12345",
			"/sqltune <粘贴你的复杂 SQL>",
		},
	}
}

func (s *SQLTuneSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
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

	// 检查 LLM 可用性 — sqltune 强依赖 LLM
	if s.modelMgr == nil || s.modelMgr.Provider() == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "  /sqltune 需要配置 LLM 模型 (active_model). 当前无可用 model.\n  请用 `dbaa configure` 配置 model 后重试.",
		}, nil
	}

	// v1.1.31: default to QuickMode + SkipUpgrade. Real-world trace showed
	// Round 2 LLM markdown gen + auto-upgrade deep mode pushed sqltune to
	// 5-15min on 10-table SQL, which then timed out the engine's per-round
	// LLM call (>600s) when accumulated context grew large. Quick mode skips
	// Round 2 (uses deterministic fallback render — same 5-dim structure,
	// just no LLM prose polish) and skips deep mode auto-upgrade. Brings
	// typical complex-SQL sqltune call from ~10min to 30-90s. Users wanting
	// full mode pass mode=deep via tool params.
	mode := strings.TrimSpace(params.StringOr("mode", "quick"))
	tuner := sqltuner.NewTuner(s.driver, s.modelMgr.Provider(), s.memStore)
	report, err := tuner.Tune(ctx, sqltuner.TuneOptions{
		SQL:         sqlText,
		Verify:      true,
		QuickMode:   mode != "deep",
		SkipUpgrade: mode != "deep",
		ForceDeep:   mode == "deep",
	})
	if err != nil {
		// Surface placeholder errors specifically — the LLM (or user) can
		// then fetch the literal-bearing SQL from dbe_perf.statement_history
		// and retry. Without this branch the error gets wrapped as the
		// opaque "phase A: plan collection failed" which is what made three
		// different LLMs (35B / Opus / GLM-5.1) all fail to recover.
		var pe *sqltuner.PlaceholderSQLError
		if errors.As(err, &pe) {
			msg := fmt.Sprintf(
				"  ⚠️ /sqltune 输入 SQL 含 %d 个未绑定占位符 %v\n"+
					"     这通常是从 dbe_perf.statement (归一化视图) 拿来的 SQL，无法直接 EXPLAIN。\n"+
					"     请改从 dbe_perf.statement_history (带字面量) 取，或把 ? / $N 替换成实际值后重试。\n"+
					"     示例查询带字面量原始 SQL：\n"+
					"       SELECT query FROM dbe_perf.statement_history\n"+
					"        WHERE unique_query LIKE '%%<前缀>%%'\n"+
					"        ORDER BY start_time DESC LIMIT 1;",
				pe.Count, pe.Samples)
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

	summary := fmt.Sprintf("%d candidates, %d verified, best %.1f×, %s",
		report.Stats.CandidateCount, report.Stats.VerifiedCount,
		report.Stats.BestSpeedup, report.Stats.TotalDuration.Round(1e6))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     report.Stats,
		Rendered: report.Markdown,
		Summary:  summary,
	}, nil
}
