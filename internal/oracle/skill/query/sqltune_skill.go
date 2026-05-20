/*-------------------------------------------------------------------------
 *
 * sqltune_skill.go
 *	  /sqltune skill for Oracle — bridges through neutral
 *	  sqltune.BuildTuner(DialectOracle). The Oracle planner + tuner
 *	  factory register via internal/oracle/sqltuner init().
 *
 *	  M5 MVP scope: deterministic Phase A output (no LLM orchestration
 *	  yet). Headline value: 10053 CBO decision trace — gold-standard
 *	  CBO reasoning dump.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/sqltune_skill.go
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
	_ "github.com/sqlrush/opendb/internal/oracle/sqltuner" // register oracle planner + tuner factory
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
func (s *SQLTuneSkill) Description() string { return "Oracle SQL 性能调优 (EXPLAIN PLAN + 10053 CBO trace)" }
func (s *SQLTuneSkill) Category() string    { return "query" }
func (s *SQLTuneSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelReadOnly
}

func (s *SQLTuneSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sqltune",
		Aliases: []string{"sqlt"},
		Usage:   "/sqltune <SQL>",
		Description: "采集 Oracle EXPLAIN PLAN 结构化 + 10053 CBO 决策跟踪 + 关键 V$PARAMETER. " +
			"10053 是 Oracle 唯一的 CBO 决策 dump (路径候选/cost 推导/join 顺序枚举). " +
			"需 ALTER SESSION 权限 + 19c+ V$DIAG_TRACE_FILE_CONTENTS 访问. M5 MVP 不含 LLM 综合.",
		Examples: []string{
			"/sqltune SELECT * FROM emp WHERE deptno = 10",
			"/sqltune <粘贴你的复杂 SQL>",
		},
	}
}

func (s *SQLTuneSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "sqltune",
		Description: "Tune a SQL on Oracle. Collects EXPLAIN PLAN via PLAN_TABLE structured rows + " +
			"10053 CBO decision trace (the gold-standard CBO reasoning dump in SQL world) + " +
			"ALL_TABLES/ALL_INDEXES/ALL_TAB_COL_STATISTICS + key V$PARAMETER (optimizer_mode, " +
			"db_file_multiblock_read_count, etc.). Returns markdown report.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Full SQL text (no unbound :N bind variables)",
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

	engine, err := sqltune.BuildTuner(sqltune.DialectOracle, deps)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /sqltune 失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}
	report, err := engine.Tune(ctx, sqltune.TuneOptions{SQL: sqlText, Verify: false})
	if err != nil {
		// Placeholder error guides user to V$SQL_BIND_CAPTURE.
		var pe *sqltune.PlaceholderError
		if errors.As(err, &pe) {
			msg := fmt.Sprintf(
				"  ⚠️ /sqltune 输入 SQL 含 %d 个未绑定占位符 %v (kind=%s)\n"+
					"     Oracle 归一化 SQL 来自 V$SQL (字面量被剥离)。\n"+
					"     可选恢复路径：\n"+
					"       1. SELECT name, value_string FROM V$SQL_BIND_CAPTURE WHERE sql_id = '<id>' — 拿历史绑定值\n"+
					"       2. 从应用 trace 日志 (event 10046) 拉实际绑定 SQL\n"+
					"       3. 手动把 :1/:B1 替换成代表值后重试",
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

	summary := "MVP report (EXPLAIN+10053)"
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
