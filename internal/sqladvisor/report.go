/*-------------------------------------------------------------------------
 *
 * report.go
 *	  RenderReport formats a SQLReport into a human-readable string.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqladvisor/report.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"fmt"
	"strings"
)

// RenderReport formats a SQLReport into a human-readable string.
func RenderReport(r *SQLReport) string {
	var sb strings.Builder

	renderHeader(&sb, r)
	renderPlanTree(&sb, r)
	renderMultiPlan(&sb, r)
	renderFindings(&sb, r)
	renderDataDepth(&sb, r)

	return sb.String()
}

func renderHeader(sb *strings.Builder, r *SQLReport) {
	sb.WriteString("=== SQL Advisor Report ===\n")
	fmt.Fprintf(sb, "SQL ID        : %s\n", r.SQLID)

	sqlText := r.SQLText
	if len(sqlText) > 200 {
		sqlText = sqlText[:200] + "..."
	}
	fmt.Fprintf(sb, "SQL Text      : %s\n", sqlText)
	fmt.Fprintf(sb, "Executions    : %d\n", r.ExecCount)
	fmt.Fprintf(sb, "Avg Elapsed   : %.3f s\n", r.AvgElapsedSec)
	fmt.Fprintf(sb, "Avg Buffer Get: %d\n", r.AvgBufferGets)
	fmt.Fprintf(sb, "Avg Disk Reads: %d\n", r.AvgDiskReads)
	sb.WriteString("\n")
}

func renderPlanTree(sb *strings.Builder, r *SQLReport) {
	if r.PlanTree == nil {
		return
	}
	sb.WriteString("--- Execution Plan ---\n")
	fmt.Fprintf(sb, "%-4s %-6s %-40s %-20s %-12s %-12s %-10s %s\n",
		"Id", "Par", "Operation", "Object", "E-Rows", "A-Rows", "Cost", "Filter")
	sb.WriteString(strings.Repeat("-", 120) + "\n")
	renderPlanNode(sb, r.PlanTree, 0)
	sb.WriteString("\n")
}

func renderPlanNode(sb *strings.Builder, node *PlanNode, depth int) {
	if node == nil {
		return
	}

	indent := strings.Repeat("  ", depth)
	operation := indent + node.Operation
	if node.Options != "" {
		operation += " " + node.Options
	}
	if len(operation) > 40 {
		operation = operation[:37] + "..."
	}

	objectName := node.ObjectName
	if node.ObjectOwner != "" && node.ObjectName != "" {
		objectName = node.ObjectOwner + "." + node.ObjectName
	}
	if len(objectName) > 20 {
		objectName = objectName[:17] + "..."
	}

	eRows := fmt.Sprintf("%d", node.Rows)

	aRows := "-"
	if node.ActualRows != nil {
		aRows = fmt.Sprintf("%d", *node.ActualRows)
	}

	filterPred := node.FilterPred
	if len(filterPred) > 40 {
		filterPred = filterPred[:37] + "..."
	}

	fmt.Fprintf(sb, "%-4d %-6d %-40s %-20s %-12s %-12s %-10d %s\n",
		node.ID, node.ParentID, operation, objectName, eRows, aRows, node.Cost, filterPred)

	for _, child := range node.Children {
		renderPlanNode(sb, child, depth+1)
	}
}

func renderMultiPlan(sb *strings.Builder, r *SQLReport) {
	if len(r.Plans) <= 1 {
		return
	}
	sb.WriteString("--- Plan Variants ---\n")
	fmt.Fprintf(sb, "%-20s %-12s %-12s %-14s\n", "Plan Hash", "Executions", "Avg Sec", "Avg Buffer Get")
	sb.WriteString(strings.Repeat("-", 62) + "\n")
	for _, p := range r.Plans {
		fmt.Fprintf(sb, "%-20d %-12d %-12.3f %-14d\n",
			p.PlanHashValue, p.Executions, p.AvgElapsedSec, p.AvgBufferGets)
	}
	sb.WriteString("\n")
}

func renderFindings(sb *strings.Builder, r *SQLReport) {
	if len(r.Findings) == 0 {
		return
	}
	sb.WriteString("--- Findings ---\n")
	for i, f := range r.Findings {
		icon := "ℹ"
		if f.Severity == "P1" || f.Severity == "P2" {
			icon = "⚠"
		}
		fmt.Fprintf(sb, "%s [%s] %s — %s\n", icon, f.Severity, f.Summary, f.Category)
		if f.Detail != "" {
			fmt.Fprintf(sb, "   %s\n", f.Detail)
		}
		for j, s := range f.Suggestions {
			fmt.Fprintf(sb, "   Suggestion %d: %s\n", j+1, s.Action)
			if s.Risk != "" {
				fmt.Fprintf(sb, "   Risk  : %s\n", s.Risk)
			}
			if s.Impact != "" {
				fmt.Fprintf(sb, "   Impact: %s\n", s.Impact)
			}
			if s.SQL != "" {
				fmt.Fprintf(sb, "   SQL   :\n%s\n", s.SQL)
			}
		}
		if i < len(r.Findings)-1 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")
}

func renderDataDepth(sb *strings.Builder, r *SQLReport) {
	fmt.Fprintf(sb, "--- Data Source: %s ---\n", r.DataDepth.String())
	if r.UpgradeHint != "" {
		fmt.Fprintf(sb, "Hint: %s\n", r.UpgradeHint)
	}
}
