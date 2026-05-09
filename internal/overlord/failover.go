/*-------------------------------------------------------------------------
 *
 * failover.go
 *	  failover.go implements failover analysis and report generation.
 *	  The analyzer collects diagnostic data via read-only queries, then
 *	  generates a detailed failover report with step-by-step
 *	  instructions for the USER to execute. OpenDB NEVER executes
 *	  database changes — the report IS the product.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/failover.go
 *
 *-------------------------------------------------------------------------
 */
// failover.go implements failover analysis and report generation.
// The analyzer collects diagnostic data via read-only queries, then generates
// a detailed failover report with step-by-step instructions for the USER to execute.
// OpenDB NEVER executes database changes — the report IS the product.
package overlord

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// FailoverReport holds the complete failover analysis and step-by-step plan.
// Immutable after creation.
type FailoverReport struct {
	Timestamp         time.Time
	PrimaryHost       string
	StandbyHost       string
	DBType            string
	SyncStatus        string // sync lag, apply status
	RiskLevel         string // low/medium/high/critical
	Steps             []FailoverStep
	PreChecks         []string // things to verify before failover
	RollbackPlan      string
	EstimatedDowntime string
}

// FailoverStep describes a single step in the failover plan.
// SQL is meant for the USER to execute, NOT for OpenDB.
type FailoverStep struct {
	Order       int
	Description string
	SQL         string // exact SQL to execute
	Risk        string // risk level for this step
	Expected    string // what should happen
	VerifySQL   string // how to verify this step worked
	Notes       string // warnings, prerequisites
}

// FailoverAnalyzer collects diagnostic info and generates failover plans.
type FailoverAnalyzer struct {
	dbAccess *DBAccessManager // for read-only diagnostic queries; may be nil
}

// NewFailoverAnalyzer creates a new FailoverAnalyzer.
// dbAccess may be nil — the analyzer falls back to template-based plans.
func NewFailoverAnalyzer(dbAccess *DBAccessManager) *FailoverAnalyzer {
	return &FailoverAnalyzer{dbAccess: dbAccess}
}

// Analyze collects diagnostic data and generates a failover plan.
// All database queries are READ-ONLY. If dbAccess is nil or queries fail,
// a generic template-based plan is generated.
func (fa *FailoverAnalyzer) Analyze(
	ctx context.Context,
	primaryWorkerID, standbyWorkerID, dbType string,
) (*FailoverReport, error) {
	if primaryWorkerID == "" || standbyWorkerID == "" {
		return nil, fmt.Errorf("both primary and standby worker IDs are required")
	}
	if dbType == "" {
		return nil, fmt.Errorf("dbType is required")
	}

	syncStatus := fa.collectSyncStatus(ctx, primaryWorkerID, standbyWorkerID, dbType)
	riskLevel := assessRisk(syncStatus)

	report := &FailoverReport{
		Timestamp:   time.Now(),
		PrimaryHost: primaryWorkerID,
		StandbyHost: standbyWorkerID,
		DBType:      dbType,
		SyncStatus:  syncStatus,
		RiskLevel:   riskLevel,
	}

	switch strings.ToLower(dbType) {
	case "oracle":
		fillOracleFailover(report)
	case "mysql":
		fillMySQLFailover(report)
	case "postgresql", "postgres":
		fillPostgreSQLFailover(report)
	default:
		fillGenericFailover(report, dbType)
	}

	return report, nil
}

// collectSyncStatus queries diagnostic views for replication status.
// Returns a human-readable status string. Falls back gracefully on error.
func (fa *FailoverAnalyzer) collectSyncStatus(
	ctx context.Context,
	primaryID, _ string,
	dbType string,
) string {
	if fa.dbAccess == nil {
		return "unknown (no database access configured)"
	}

	query := diagnosticQuery(dbType)
	if query == "" {
		return "unknown (unsupported db type for diagnostics)"
	}

	result, err := fa.dbAccess.ExecuteSQL(ctx, primaryID, query)
	if err != nil {
		return fmt.Sprintf("unknown (query failed: %v)", err)
	}

	return formatSyncResult(result)
}

// diagnosticQuery returns the read-only diagnostic query for the given db type.
func diagnosticQuery(dbType string) string {
	switch strings.ToLower(dbType) {
	case "oracle":
		return "SELECT NAME, VALUE, DATUM_TIME FROM V$DATAGUARD_STATS WHERE NAME IN ('transport lag', 'apply lag')"
	case "mysql":
		return "SHOW REPLICA STATUS"
	case "postgresql", "postgres":
		return "SELECT client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn, sync_state FROM pg_stat_replication"
	default:
		return ""
	}
}

// formatSyncResult converts query results into a human-readable sync status.
func formatSyncResult(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "no replication data returned"
	}

	var parts []string
	for _, row := range qr.Rows {
		rowStrs := make([]string, 0, len(row))
		for _, col := range row {
			rowStrs = append(rowStrs, fmt.Sprintf("%v", col))
		}
		parts = append(parts, strings.Join(rowStrs, " | "))
	}
	return strings.Join(parts, "; ")
}

// assessRisk determines the risk level based on sync status.
func assessRisk(syncStatus string) string {
	lower := strings.ToLower(syncStatus)
	switch {
	case strings.Contains(lower, "unknown") || strings.Contains(lower, "failed"):
		return "high"
	case strings.Contains(lower, "no replication"):
		return "critical"
	default:
		return "medium"
	}
}

// RenderFailoverMarkdown renders a FailoverReport to a complete markdown document.
func RenderFailoverMarkdown(report *FailoverReport) string {
	var sb strings.Builder

	renderHeader(&sb, report)
	renderPreChecks(&sb, report.PreChecks)
	renderSteps(&sb, report.Steps)
	renderRollback(&sb, report.RollbackPlan)
	renderEstimate(&sb, report.EstimatedDowntime)

	return sb.String()
}

func renderHeader(sb *strings.Builder, r *FailoverReport) {
	sb.WriteString("# Failover Report\n\n")
	sb.WriteString("| Item | Value |\n")
	sb.WriteString("|------|-------|\n")
	sb.WriteString(fmt.Sprintf("| Report Time | %s |\n", r.Timestamp.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("| Database Type | %s |\n", r.DBType))
	sb.WriteString(fmt.Sprintf("| Primary | %s |\n", r.PrimaryHost))
	sb.WriteString(fmt.Sprintf("| Standby | %s |\n", r.StandbyHost))
	sb.WriteString(fmt.Sprintf("| Sync Status | %s |\n", r.SyncStatus))
	sb.WriteString(fmt.Sprintf("| Risk Level | %s |\n", r.RiskLevel))
	sb.WriteString("\n")
}

func renderPreChecks(sb *strings.Builder, checks []string) {
	sb.WriteString("## Pre-Checks\n\n")
	sb.WriteString("Verify the following before starting failover:\n\n")
	for i, check := range checks {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, check))
	}
	sb.WriteString("\n")
}

func renderSteps(sb *strings.Builder, steps []FailoverStep) {
	sb.WriteString("## Failover Steps\n\n")
	for _, step := range steps {
		renderSingleStep(sb, step)
	}
}

func renderSingleStep(sb *strings.Builder, step FailoverStep) {
	sb.WriteString(fmt.Sprintf("### Step %d: %s\n\n", step.Order, step.Description))
	sb.WriteString(fmt.Sprintf("**Risk:** %s\n\n", step.Risk))

	if step.Notes != "" {
		sb.WriteString(fmt.Sprintf("**Notes:** %s\n\n", step.Notes))
	}

	sb.WriteString("**Execute:**\n\n```sql\n")
	sb.WriteString(step.SQL)
	sb.WriteString("\n```\n\n")

	sb.WriteString(fmt.Sprintf("**Expected:** %s\n\n", step.Expected))

	sb.WriteString("**Verify:**\n\n```sql\n")
	sb.WriteString(step.VerifySQL)
	sb.WriteString("\n```\n\n")
}

func renderRollback(sb *strings.Builder, plan string) {
	sb.WriteString("## Rollback Plan\n\n")
	sb.WriteString(plan)
	sb.WriteString("\n\n")
}

func renderEstimate(sb *strings.Builder, estimate string) {
	sb.WriteString("## Estimated Downtime\n\n")
	sb.WriteString(estimate)
	sb.WriteString("\n")
}
