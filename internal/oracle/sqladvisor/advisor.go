/*-------------------------------------------------------------------------
 *
 * advisor.go
 *	  Advisor coordinates SQL analysis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/advisor.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"context"
	"fmt"
	"sort"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/oracle/sqladvisor/analyzers"
	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Advisor coordinates SQL analysis.
type Advisor struct {
	driver    db.Driver
	analyzers []sadv.Analyzer
}

// New creates an Advisor with all 7 analyzers.
func New(driver db.Driver) *Advisor {
	return &Advisor{
		driver: driver,
		analyzers: []sadv.Analyzer{
			analyzers.NewAccessPath(),
			analyzers.NewPredicate(),
			analyzers.NewJoin(),
			analyzers.NewStatistics(),
			analyzers.NewPlanStability(),
			analyzers.NewResource(),
			analyzers.NewRewrite(),
		},
	}
}

// Analyze performs full analysis on a single SQL by sql_id.
func (a *Advisor) Analyze(ctx context.Context, sqlID string) (*sadv.SQLReport, error) {
	// 1. Collect all data
	report, err := Collect(ctx, a.driver, sqlID)
	if err != nil {
		return nil, fmt.Errorf("collect data for %s: %w", sqlID, err)
	}

	// 2. Build analyze context
	actx := &sadv.AnalyzeContext{
		Report:     report,
		TableStats: report.TableStats,
		IndexMap:   report.IndexMap,
		PlanTree:   report.PlanTree,
		DataDepth:  report.DataDepth,
	}

	// 3. Run all analyzers
	for _, az := range a.analyzers {
		findings := az.Analyze(actx)
		report.Findings = append(report.Findings, findings...)
	}

	// 4. Sort by severity (P1 > P2 > P3)
	sort.Slice(report.Findings, func(i, j int) bool {
		return severityRank(report.Findings[i].Severity) > severityRank(report.Findings[j].Severity)
	})

	// 5. Set upgrade hint if data is basic and findings have uncertainty
	if report.DataDepth == sadv.DataDepthBasic && hasUncertainFindings(report.Findings) {
		report.UpgradeHint = "建议: ALTER SYSTEM SET STATISTICS_LEVEL=ALL 后重新执行该 SQL，\n  再次运行 /sqladvisor " + sqlID + " 可获得实际行数(A-Rows)对比分析"
	}

	return report, nil
}

// FindAndAnalyze searches v$sql by text fragment and analyzes matches.
func (a *Advisor) FindAndAnalyze(ctx context.Context, sqlText string) ([]*sadv.SQLReport, error) {
	sqlIDs, err := FindSQLByText(ctx, a.driver, sqlText)
	if err != nil {
		return nil, fmt.Errorf("find SQL by text: %w", err)
	}
	if len(sqlIDs) == 0 {
		return nil, fmt.Errorf("未在 v$sql 中找到匹配的 SQL")
	}
	var reports []*sadv.SQLReport
	for _, id := range sqlIDs {
		r, err := a.Analyze(ctx, id)
		if err != nil {
			continue
		}
		reports = append(reports, r)
	}
	if len(reports) == 0 {
		return nil, fmt.Errorf("匹配的 SQL 均无法分析")
	}
	return reports, nil
}

// AnalyzeTopSQLs queries current active sessions for top slow SQLs and analyzes them.
func (a *Advisor) AnalyzeTopSQLs(ctx context.Context) ([]*sadv.SQLReport, error) {
	// Query top 5 active SQL by elapsed time
	topSQL := `SELECT sql_id, ROUND(elapsed_time/GREATEST(executions,1)/1e6, 3) avg_sec
FROM v$sql
WHERE executions > 0 AND elapsed_time > 0 AND sql_text NOT LIKE '%v$sql%'
ORDER BY elapsed_time/GREATEST(executions,1) DESC
FETCH FIRST 5 ROWS ONLY`

	result, err := a.driver.Query(ctx, topSQL)
	if err != nil {
		return nil, fmt.Errorf("query top SQLs: %w", err)
	}

	var reports []*sadv.SQLReport
	for _, row := range result.Rows {
		sqlID := toStr(row[0])
		if sqlID == "" {
			continue
		}
		r, err := a.Analyze(ctx, sqlID)
		if err != nil {
			continue
		}
		if len(r.Findings) > 0 {
			reports = append(reports, r)
		}
	}
	if len(reports) == 0 {
		return nil, fmt.Errorf("当前无可优化的 Top SQL")
	}
	return reports, nil
}

func severityRank(s string) int {
	switch s {
	case "P1":
		return 3
	case "P2":
		return 2
	case "P3":
		return 1
	default:
		return 0
	}
}

func hasUncertainFindings(findings []sadv.Finding) bool {
	for _, f := range findings {
		if f.Category == "join" || f.Category == "access_path" || f.Category == "statistics" {
			return true
		}
	}
	return false
}
