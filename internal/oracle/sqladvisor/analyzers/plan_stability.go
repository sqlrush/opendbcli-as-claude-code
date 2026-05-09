/*-------------------------------------------------------------------------
 *
 * plan_stability.go
 *	  PlanStability detects execution plan regression and instability.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/analyzers/plan_stability.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// PlanStability detects execution plan regression and instability.
type PlanStability struct{}

// NewPlanStability creates a plan-stability analyzer.
func NewPlanStability() *PlanStability { return &PlanStability{} }

func (p *PlanStability) Name() string { return "plan_stability" }

func (p *PlanStability) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	findings = append(findings, p.checkPlanRegression(ctx)...)
	findings = append(findings, p.checkMultipleVersions(ctx)...)
	return findings
}

func (p *PlanStability) checkPlanRegression(ctx *sadv.AnalyzeContext) []sadv.Finding {
	plans := ctx.Report.Plans
	if len(plans) <= 1 {
		return nil
	}

	// Find best plan (lowest avg_sec)
	bestIdx := 0
	for i, pl := range plans {
		if pl.AvgElapsedSec < plans[bestIdx].AvgElapsedSec {
			bestIdx = i
		}
	}
	best := plans[bestIdx]
	current := plans[len(plans)-1] // last = current

	if best.AvgElapsedSec <= 0 {
		return nil
	}
	ratio := current.AvgElapsedSec / best.AvgElapsedSec

	if ratio <= 1.5 {
		return nil
	}

	sev := "P2"
	summary := fmt.Sprintf("执行计划存在波动(当前比最优慢%.1f倍)", ratio)
	if ratio > 3 {
		sev = "P1"
		summary = fmt.Sprintf("当前执行计划比最优计划慢%.1f倍", ratio)
	}

	return []sadv.Finding{{
		Severity: sev,
		Category: "plan_stability",
		Summary:  summary,
		Detail: fmt.Sprintf("最优计划 %d (%.3fs), 当前计划 %d (%.3fs)",
			best.PlanHashValue, best.AvgElapsedSec,
			current.PlanHashValue, current.AvgElapsedSec),
		Suggestions: []sadv.Suggestion{{
			Action: "使用SQL Plan Baseline锁定最优计划",
			SQL: fmt.Sprintf("EXEC DBMS_SPM.LOAD_PLANS_FROM_CURSOR_CACHE(sql_id=>'%s', plan_hash_value=>%d);",
				ctx.Report.SQLID, best.PlanHashValue),
			Risk:   "低 — SPM不影响其他SQL",
			Impact: "高 — 锁定最优计划避免性能回退",
		}},
	}}
}

func (p *PlanStability) checkMultipleVersions(ctx *sadv.AnalyzeContext) []sadv.Finding {
	count := len(ctx.Report.Plans)
	if count <= 3 {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P2",
		Category: "plan_stability",
		Summary:  fmt.Sprintf("SQL有%d个子游标版本,可能存在ACS或绑定变量问题", count),
		Detail:   fmt.Sprintf("SQL ID %s 有 %d 个不同的执行计划", ctx.Report.SQLID, count),
		Suggestions: []sadv.Suggestion{{
			Action: "检查是否存在自适应游标共享或绑定变量窥视问题",
			SQL: fmt.Sprintf("SELECT plan_hash_value, executions, buffer_gets/NULLIF(executions,0) avg_gets\nFROM v$sql WHERE sql_id='%s' ORDER BY last_active_time;",
				ctx.Report.SQLID),
			Risk: "低", Impact: "中",
		}},
	}}
}
