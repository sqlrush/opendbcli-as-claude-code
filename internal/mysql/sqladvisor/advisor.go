/*-------------------------------------------------------------------------
 *
 * advisor.go
 *	  Package sqladvisor provides MySQL SQL execution plan analysis and
 *	  optimization recommendations.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqladvisor/advisor.go
 *
 *-------------------------------------------------------------------------
 */
// Package sqladvisor provides MySQL SQL execution plan analysis and optimization recommendations.
package sqladvisor

import (
	"context"
	"fmt"
	"sort"

	"github.com/sqlrush/opendb/internal/db"
	sadv "github.com/sqlrush/opendb/internal/sqladvisor"

	"github.com/sqlrush/opendb/internal/mysql/sqladvisor/analyzers"
)

// Advisor orchestrates SQL analysis using multiple analyzers.
type Advisor struct {
	driver    db.Driver
	analyzers []sadv.Analyzer
}

// New creates a MySQL SQL Advisor with all built-in analyzers.
func New(driver db.Driver) *Advisor {
	return &Advisor{
		driver: driver,
		analyzers: []sadv.Analyzer{
			analyzers.NewAccessPath(),
			analyzers.NewPredicate(),
			analyzers.NewJoin(),
			analyzers.NewStatistics(),
			analyzers.NewResource(),
			analyzers.NewRewrite(),
		},
	}
}

// Analyze performs full analysis on a SQL identified by its digest.
func (a *Advisor) Analyze(ctx context.Context, digest string) (*sadv.SQLReport, error) {
	report, err := Collect(ctx, a.driver, digest)
	if err != nil {
		return nil, fmt.Errorf("collect: %w", err)
	}

	actx := &sadv.AnalyzeContext{
		Report:     report,
		TableStats: report.TableStats,
		IndexMap:   report.IndexMap,
		PlanTree:   report.PlanTree,
		DataDepth:  report.DataDepth,
	}

	for _, analyzer := range a.analyzers {
		func() {
			defer func() { recover() }() // protect against nil pointer in analyzers
			findings := analyzer.Analyze(actx)
			report.Findings = append(report.Findings, findings...)
		}()
	}

	// Sort findings by severity (P1 > P2 > P3)
	sort.Slice(report.Findings, func(i, j int) bool {
		return severityRank(report.Findings[i].Severity) > severityRank(report.Findings[j].Severity)
	})

	return report, nil
}

// FindAndAnalyze searches for SQL by text fragment and analyzes matches.
func (a *Advisor) FindAndAnalyze(ctx context.Context, text string) ([]*sadv.SQLReport, error) {
	digests, err := FindSQLByText(ctx, a.driver, text)
	if err != nil {
		return nil, fmt.Errorf("find sql: %w", err)
	}
	if len(digests) == 0 {
		return nil, fmt.Errorf("未找到包含 \"%s\" 的 SQL", text)
	}

	var reports []*sadv.SQLReport
	for _, d := range digests {
		report, err := a.Analyze(ctx, d)
		if err != nil {
			continue
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// AnalyzeTopSQLs analyzes the top N slowest recently active SQLs.
func (a *Advisor) AnalyzeTopSQLs(ctx context.Context) ([]*sadv.SQLReport, error) {
	digests, err := FindTopSQLs(ctx, a.driver, 5)
	if err != nil {
		return nil, fmt.Errorf("find top sqls: %w", err)
	}
	if len(digests) == 0 {
		return nil, fmt.Errorf("无近期活跃的 SQL")
	}

	var reports []*sadv.SQLReport
	for _, d := range digests {
		report, err := a.Analyze(ctx, d)
		if err != nil {
			continue
		}
		if len(report.Findings) > 0 {
			reports = append(reports, report)
		}
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
