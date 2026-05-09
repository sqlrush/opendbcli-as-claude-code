/*-------------------------------------------------------------------------
 *
 * analyzer.go
 *	  Analyzer inspects an execution plan and produces findings.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqladvisor/analyzer.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

// Analyzer inspects an execution plan and produces findings.
type Analyzer interface {
	Name() string
	Analyze(ctx *AnalyzeContext) []Finding
}
