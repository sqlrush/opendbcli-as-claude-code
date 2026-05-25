/*-------------------------------------------------------------------------
 *
 * health.go
 *	  HealthSkill provides a PostgreSQL database health overview.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/health.go
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

const healthInstanceSQL = `SELECT
  current_setting('server_version') AS version,
  pg_postmaster_start_time()::text AS start_time,
  EXTRACT(EPOCH FROM clock_timestamp() - pg_postmaster_start_time())::bigint AS uptime_sec,
  current_database() AS db_name,
  inet_server_addr()::text AS listen_addr`

const healthConnSQL = `SELECT
  (SELECT setting::int FROM pg_settings WHERE name = 'max_connections') AS max_conn,
  (SELECT count(*) FROM pg_stat_activity) AS current_conn,
  (SELECT count(*) FROM pg_stat_activity WHERE state = 'active') AS active_conn,
  (SELECT count(*) FROM pg_stat_activity WHERE state = 'idle') AS idle_conn,
  (SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction') AS idle_in_tx`

const healthMemSQL = `SELECT
  current_setting('shared_buffers') AS shared_buffers,
  current_setting('effective_cache_size') AS effective_cache_size,
  current_setting('work_mem') AS work_mem,
  current_setting('maintenance_work_mem') AS maintenance_work_mem`

const healthCacheSQL = `SELECT
  COALESCE(sum(blks_hit), 0) AS blks_hit,
  COALESCE(sum(blks_read), 0) AS blks_read
FROM pg_stat_database`

const healthTxSQL = `SELECT
  COALESCE(sum(xact_commit), 0) AS commits,
  COALESCE(sum(xact_rollback), 0) AS rollbacks
FROM pg_stat_database`

const healthMaintSQL = `SELECT
  COALESCE(sum(n_dead_tup), 0) AS total_dead_tup,
  max(age(datfrozenxid)) AS max_xid_age
FROM pg_stat_user_tables, pg_database
WHERE pg_database.datname = current_database()`

const healthReplSQL = `SELECT count(*) AS replicas FROM pg_stat_replication`

// HealthSkill provides a PostgreSQL database health overview.
type HealthSkill struct {
	driver db.Driver
}

// NewHealthSkill creates a HealthSkill backed by the given driver.
func NewHealthSkill(driver db.Driver) *HealthSkill {
	return &HealthSkill{driver: driver}
}

func (s *HealthSkill) Name() string                       { return "health" }
func (s *HealthSkill) Description() string                { return "数据库健康总览" }
func (s *HealthSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *HealthSkill) Validate(_ skill.Params) error      { return nil }

func (s *HealthSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/health", Examples: []string{"/health"}}
}

func (s *HealthSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "health",
		Description: "Show PostgreSQL database health overview: connections, cache hit ratio, transaction rate, dead tuples, replication, XID age",
	}
}

// checkResult holds the outcome of a single health check.
type checkResult struct {
	name    string
	status  string // "OK", "WARN"
	message string
}

func (s *HealthSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	var checks []checkResult
	sections := []format.PanelSection{}

	// Instance info
	if sec, instChecks, err := s.instanceSection(ctx); err == nil {
		checks = append(checks, instChecks...)
		sections = append(sections, sec)
	} else {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	// Memory settings
	if sec, memChecks, err := s.memorySection(ctx); err == nil {
		checks = append(checks, memChecks...)
		sections = append(sections, sec)
	}

	// Session info
	if sec, sessChecks, err := s.sessionSection(ctx); err == nil {
		checks = append(checks, sessChecks...)
		sections = append(sections, sec)
	}

	// Maintenance info
	if sec, maintChecks, err := s.maintenanceSection(ctx); err == nil {
		checks = append(checks, maintChecks...)
		sections = append(sections, sec)
	}

	// Replication info
	if sec, replChecks, err := s.replicationSection(ctx); err == nil {
		checks = append(checks, replChecks...)
		sections = append(sections, sec)
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

	allSections := []format.PanelSection{
		{Lines: []string{
			fmt.Sprintf(" Overall: %s %s  %s", overallIcon, overallLabel, overallDetail),
		}},
	}
	allSections = append(allSections, sections...)

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
		allSections = append(allSections, format.PanelSection{
			Header: "Alerts",
			Lines:  alerts,
		})
	}

	rendered := format.Panel("PostgreSQL Health", allSections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d OK, %d WARN", okCount, warnCount),
	}, nil
}

func (s *HealthSkill) instanceSection(ctx context.Context) (format.PanelSection, []checkResult, error) {
	result, err := s.driver.Query(ctx, healthInstanceSQL)
	if err != nil {
		return format.PanelSection{}, nil, err
	}
	if len(result.Rows) == 0 {
		return format.PanelSection{}, nil, fmt.Errorf("no instance info")
	}

	row := result.Rows[0]
	version := asStr(row[0])
	startTime := asStr(row[1])
	uptimeSec := toFloat64Val(row[2])
	dbName := asStr(row[3])
	listenAddr := asStr(row[4])

	// Cache hit ratio
	cacheResult, _ := s.driver.Query(ctx, healthCacheSQL)
	hitRatio := 0.0
	if cacheResult != nil && len(cacheResult.Rows) > 0 {
		hit := toFloat64Val(cacheResult.Rows[0][0])
		read := toFloat64Val(cacheResult.Rows[0][1])
		if hit+read > 0 {
			hitRatio = hit / (hit + read) * 100
		}
	}

	// TPS
	txResult, _ := s.driver.Query(ctx, healthTxSQL)
	tps := 0.0
	commits := 0.0
	rollbacks := 0.0
	if txResult != nil && len(txResult.Rows) > 0 {
		commits = toFloat64Val(txResult.Rows[0][0])
		rollbacks = toFloat64Val(txResult.Rows[0][1])
		if uptimeSec > 0 {
			tps = (commits + rollbacks) / uptimeSec
		}
	}

	checks := []checkResult{
		{"Version", "OK", version},
		{"Database", "OK", dbName},
		{"Listen", "OK", listenAddr},
		{"Start Time", "OK", startTime},
		{"Uptime", boolStatus(uptimeSec >= 3600), formatUptime(uptimeSec)},
		{"Cache Hit Ratio", boolStatus(hitRatio >= 99), fmt.Sprintf("%.2f%%", hitRatio)},
		{"TPS", "OK", fmt.Sprintf("%.1f (commits=%.0f, rollbacks=%.0f)", tps, commits, rollbacks)},
	}

	lines := make([]string, len(checks))
	for i, c := range checks {
		lines[i] = fmtCheck(c)
	}

	return format.PanelSection{
		Header: "Instance",
		Lines:  lines,
	}, checks, nil
}

func (s *HealthSkill) memorySection(ctx context.Context) (format.PanelSection, []checkResult, error) {
	result, err := s.driver.Query(ctx, healthMemSQL)
	if err != nil {
		return format.PanelSection{}, nil, err
	}
	if len(result.Rows) == 0 {
		return format.PanelSection{}, nil, fmt.Errorf("no memory info")
	}

	row := result.Rows[0]
	checks := []checkResult{
		{"shared_buffers", "OK", asStr(row[0])},
		{"effective_cache_size", "OK", asStr(row[1])},
		{"work_mem", "OK", asStr(row[2])},
		{"maintenance_work_mem", "OK", asStr(row[3])},
	}

	lines := make([]string, len(checks))
	for i, c := range checks {
		lines[i] = fmtCheck(c)
	}

	return format.PanelSection{
		Header: "Memory",
		Lines:  lines,
	}, checks, nil
}

func (s *HealthSkill) sessionSection(ctx context.Context) (format.PanelSection, []checkResult, error) {
	result, err := s.driver.Query(ctx, healthConnSQL)
	if err != nil {
		return format.PanelSection{}, nil, err
	}
	if len(result.Rows) == 0 {
		return format.PanelSection{}, nil, fmt.Errorf("no connection info")
	}

	row := result.Rows[0]
	maxConn := toFloat64Val(row[0])
	curConn := toFloat64Val(row[1])
	active := toFloat64Val(row[2])
	idle := toFloat64Val(row[3])
	idleInTx := toFloat64Val(row[4])
	connPct := safePct(curConn, maxConn)

	checks := []checkResult{
		{"Connections", boolStatus(connPct < 80), fmt.Sprintf("%.0f / %.0f (%.1f%%)", curConn, maxConn, connPct)},
		{"Active", boolStatus(active < 50), fmt.Sprintf("%.0f", active)},
		{"Idle", "OK", fmt.Sprintf("%.0f", idle)},
		{"Idle in TX", boolStatus(idleInTx < 10), fmt.Sprintf("%.0f", idleInTx)},
	}

	lines := make([]string, len(checks))
	for i, c := range checks {
		lines[i] = fmtCheck(c)
	}
	lines = append(lines, fmt.Sprintf(" %-16s  %s", "", format.ProgressBar(connPct, 20)))

	return format.PanelSection{
		Header: "Session",
		Lines:  lines,
	}, checks, nil
}

func (s *HealthSkill) maintenanceSection(ctx context.Context) (format.PanelSection, []checkResult, error) {
	result, err := s.driver.Query(ctx, healthMaintSQL)
	if err != nil {
		return format.PanelSection{}, nil, err
	}
	if len(result.Rows) == 0 {
		return format.PanelSection{}, nil, fmt.Errorf("no maintenance info")
	}

	row := result.Rows[0]
	deadTup := toFloat64Val(row[0])
	xidAge := toFloat64Val(row[1])
	xidPct := safePct(xidAge, 2147483647)

	checks := []checkResult{
		{"Dead Tuples", boolStatus(deadTup < 100000), fmt.Sprintf("%.0f", deadTup)},
		{"XID Age", boolStatus(xidPct < 50), fmt.Sprintf("%.0f (%.2f%% to wraparound)", xidAge, xidPct)},
	}

	lines := make([]string, len(checks))
	for i, c := range checks {
		lines[i] = fmtCheck(c)
	}

	return format.PanelSection{
		Header: "Maintenance",
		Lines:  lines,
	}, checks, nil
}

func (s *HealthSkill) replicationSection(ctx context.Context) (format.PanelSection, []checkResult, error) {
	result, err := s.driver.Query(ctx, healthReplSQL)
	if err != nil {
		return format.PanelSection{}, nil, err
	}

	replicas := "0"
	if len(result.Rows) > 0 {
		replicas = asStr(result.Rows[0][0])
	}

	role := "standalone"
	if replicas != "0" {
		role = "primary"
	}

	// Check if we are a standby
	isRecoveryResult, _ := s.driver.Query(ctx, "SELECT pg_is_in_recovery()")
	if isRecoveryResult != nil && len(isRecoveryResult.Rows) > 0 {
		if asStr(isRecoveryResult.Rows[0][0]) == "true" {
			role = "standby"
		}
	}

	checks := []checkResult{
		{"Role", "OK", role},
		{"Replicas", "OK", replicas},
	}

	lines := make([]string, len(checks))
	for i, c := range checks {
		lines[i] = fmtCheck(c)
	}

	return format.PanelSection{
		Header: "Replication",
		Lines:  lines,
	}, checks, nil
}

// asStr safely converts any value to string.
func asStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// toFloat64Val converts a value to float64.
func toFloat64Val(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}

// safePct computes a/b * 100, returning 0 when b is zero.
func safePct(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a / b * 100
}

// fmtCheck formats a check result as " Name : Value  Icon".
func fmtCheck(c checkResult) string {
	icon := format.StatusIcon(c.status)
	return fmt.Sprintf(" %-16s: %s  %s", c.name, c.message, icon)
}

// boolStatus returns "OK" when the condition is true, "WARN" otherwise.
func boolStatus(ok bool) string {
	if ok {
		return "OK"
	}
	return "WARN"
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
