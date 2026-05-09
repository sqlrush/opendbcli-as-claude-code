/*-------------------------------------------------------------------------
 *
 * health.go
 *	  HealthSkill provides a MySQL database health overview (~15 items).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/health.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// HealthSkill provides a MySQL database health overview (~15 items).
type HealthSkill struct {
	driver db.Driver
}

// NewHealthSkill creates a HealthSkill backed by the given driver.
func NewHealthSkill(driver db.Driver) *HealthSkill {
	return &HealthSkill{driver: driver}
}

func (s *HealthSkill) Name() string                       { return "health" }
func (s *HealthSkill) Description() string                { return "数据库健康总览" }
func (s *HealthSkill) Category() string                   { return "monitor" }
func (s *HealthSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *HealthSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/health", Examples: []string{"/health"}}
}

func (s *HealthSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "health",
		Description: "Show MySQL database health overview: connections, buffer pool, replication, threads, uptime",
	}
}

func (s *HealthSkill) Validate(_ skill.Params) error { return nil }

func (s *HealthSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	statusMap, err := s.queryGlobalStatus(ctx)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	varsMap, err := s.queryGlobalVariables(ctx)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := s.buildPanel(statusMap, varsMap)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "MySQL health overview",
	}, nil
}

func (s *HealthSkill) queryGlobalStatus(ctx context.Context) (map[string]string, error) {
	result, err := s.driver.Query(ctx, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, fmt.Errorf("SHOW GLOBAL STATUS: %w", err)
	}
	return rowsToMap(result), nil
}

func (s *HealthSkill) queryGlobalVariables(ctx context.Context) (map[string]string, error) {
	result, err := s.driver.Query(ctx, "SHOW GLOBAL VARIABLES")
	if err != nil {
		return nil, fmt.Errorf("SHOW GLOBAL VARIABLES: %w", err)
	}
	return rowsToMap(result), nil
}

// checkResult holds the outcome of a single health check.
type checkResult struct {
	name    string
	status  string // "OK", "WARN"
	message string
}

func (s *HealthSkill) buildPanel(status, vars map[string]string) string {
	uptime := parseFloat(status["Uptime"])
	uptimeStr := formatUptime(uptime)

	maxConn := parseFloat(vars["max_connections"])
	curConn := parseFloat(status["Threads_connected"])
	connPct := safePct(curConn, maxConn)

	threadsRunning := parseFloat(status["Threads_running"])
	threadsCreated := status["Threads_created"]
	threadsCached := status["Threads_cached"]

	// Buffer pool hit ratio
	readReq := parseFloat(status["Innodb_buffer_pool_read_requests"])
	reads := parseFloat(status["Innodb_buffer_pool_reads"])
	bpHitPct := 0.0
	if readReq > 0 {
		bpHitPct = (1 - reads/readReq) * 100
	}

	bpSizeBytes := parseFloat(vars["innodb_buffer_pool_size"])
	bpSizeMB := bpSizeBytes / 1048576

	bpPagesFree := parseFloat(status["Innodb_buffer_pool_pages_free"])
	bpPagesTotal := parseFloat(status["Innodb_buffer_pool_pages_total"])
	bpUsagePct := safePct(bpPagesTotal-bpPagesFree, bpPagesTotal)

	// QPS / TPS
	questions := parseFloat(status["Questions"])
	comCommit := parseFloat(status["Com_commit"])
	comRollback := parseFloat(status["Com_rollback"])

	qps := 0.0
	tps := 0.0
	if uptime > 0 {
		qps = questions / uptime
		tps = (comCommit + comRollback) / uptime
	}

	// Replication
	slaveRunning := status["Slave_running"]
	if slaveRunning == "" {
		slaveRunning = "N/A"
	}

	// Aborted
	abortedConn := parseFloat(status["Aborted_connects"])
	abortedClients := parseFloat(status["Aborted_clients"])

	// Slow queries
	slowQueries := parseFloat(status["Slow_queries"])

	// Build check results with thresholds
	checks := []checkResult{
		// Instance
		{"Uptime", boolStatus(uptime >= 3600), uptimeStr},
		{"QPS", "OK", fmt.Sprintf("%.1f", qps)},
		{"TPS", "OK", fmt.Sprintf("%.1f", tps)},
		{"Slow Queries", boolStatus(slowQueries < 100), fmt.Sprintf("%.0f", slowQueries)},
		// Memory
		{"Buffer Pool", "OK", fmt.Sprintf("%.0f MB", bpSizeMB)},
		{"BP Hit Ratio", boolStatus(bpHitPct >= 99), fmt.Sprintf("%.2f%%", bpHitPct)},
		{"BP Usage", boolStatus(bpUsagePct < 95), fmt.Sprintf("%.1f%%", bpUsagePct)},
		// Session
		{"Connections", boolStatus(connPct < 80), fmt.Sprintf("%.0f / %.0f (%.1f%%)", curConn, maxConn, connPct)},
		{"Threads Running", boolStatus(threadsRunning < 50), fmt.Sprintf("%.0f", threadsRunning)},
		{"Threads Created", "OK", threadsCreated},
		{"Threads Cached", "OK", threadsCached},
		{"Aborted Conn", boolStatus(abortedConn < 100), fmt.Sprintf("%.0f", abortedConn)},
		{"Aborted Clients", boolStatus(abortedClients < 100), fmt.Sprintf("%.0f", abortedClients)},
		// Replication
		{"Slave Running", replicationStatus(slaveRunning), slaveRunning},
	}

	// Count statuses
	var warnCount int
	for _, c := range checks {
		if c.status == "WARN" {
			warnCount++
		}
	}
	okCount := len(checks) - warnCount

	// Overall section
	var overallIcon, overallLabel, overallDetail string
	if warnCount > 0 {
		overallIcon = format.StatusIcon("WARN")
		overallLabel = format.StatusLabel("WARN")
		overallDetail = fmt.Sprintf("(%d issues found)", warnCount)
	} else {
		overallIcon = format.StatusIcon("OK")
		overallLabel = format.StatusLabel("OK")
		overallDetail = fmt.Sprintf("(%d checks passed)", okCount)
	}

	sections := []format.PanelSection{
		{Lines: []string{
			fmt.Sprintf(" Overall: %s %s  %s", overallIcon, overallLabel, overallDetail),
		}},
		{
			Header: "Instance",
			Lines: []string{
				fmtCheck(checks[0]),
				fmtCheck(checks[1]),
				fmtCheck(checks[2]),
				fmtCheck(checks[3]),
			},
		},
		{
			Header: "Memory",
			Lines: []string{
				fmtCheck(checks[4]),
				fmtCheck(checks[5]),
				fmtCheckWithBar(checks[6], bpUsagePct),
			},
		},
		{
			Header: "Session",
			Lines: []string{
				fmtCheck(checks[7]),
				fmtCheck(checks[8]),
				fmtCheck(checks[9]),
				fmtCheck(checks[10]),
				fmtCheck(checks[11]),
				fmtCheck(checks[12]),
			},
		},
		{
			Header: "Replication",
			Lines: []string{
				fmtCheck(checks[13]),
			},
		},
	}

	// Alerts section for non-OK checks
	var alerts []string
	for _, c := range checks {
		if c.status == "OK" {
			continue
		}
		icon := format.StatusIcon(c.status)
		alerts = append(alerts, fmt.Sprintf(" %s %s: %s", icon, c.name, c.message))
	}
	if len(alerts) > 0 {
		sections = append(sections, format.PanelSection{
			Header: "Alerts",
			Lines:  alerts,
		})
	}

	return format.Panel("MySQL Health", sections)
}

// fmtCheck formats a check result as " Name : Value  Icon".
func fmtCheck(c checkResult) string {
	icon := format.StatusIcon(c.status)
	return fmt.Sprintf(" %-16s: %s  %s", c.name, c.message, icon)
}

// fmtCheckWithBar formats a check result with a progress bar appended.
func fmtCheckWithBar(c checkResult, pct float64) string {
	icon := format.StatusIcon(c.status)
	return fmt.Sprintf(" %-16s: %s  %s  %s", c.name, c.message, format.ProgressBar(pct, 20), icon)
}

// boolStatus returns "OK" when the condition is true, "WARN" otherwise.
func boolStatus(ok bool) string {
	if ok {
		return "OK"
	}
	return "WARN"
}

// replicationStatus returns status for replication field.
func replicationStatus(val string) string {
	if val == "ON" || val == "N/A" {
		return "OK"
	}
	return "WARN"
}

// rowsToMap converts a 2-column QueryResult (name, value) into a map.
func rowsToMap(qr *db.QueryResult) map[string]string {
	m := make(map[string]string, len(qr.Rows))
	for _, row := range qr.Rows {
		if len(row) < 2 {
			continue
		}
		key := asStr(row[0])
		val := asStr(row[1])
		m[key] = val
	}
	return m
}

// asStr safely converts any value to string.
func asStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// parseFloat parses a string as float64, returning 0 on failure.
func parseFloat(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

// safePct computes a/b * 100, returning 0 when b is zero.
func safePct(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a / b * 100
}

// formatUptime formats seconds into a human-readable string.
func formatUptime(seconds float64) string {
	s := int(seconds)
	days := s / 86400
	hours := (s % 86400) / 3600
	mins := (s % 3600) / 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	parts = append(parts, fmt.Sprintf("%dm", mins))
	return strings.Join(parts, " ")
}
