/*-------------------------------------------------------------------------
 *
 * rule_skill.go
 *	  RuleSkill runs the deterministic rule engine on sentinel reports.
 *	  Works without LLM — provides root-cause analysis, evidence
 *	  chains, and remediation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/ai/rule_skill.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/opengauss/ruleengine"
	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// RuleSkill runs the deterministic rule engine on sentinel reports.
// Works without LLM — provides root-cause analysis, evidence chains, and remediation.
type RuleSkill struct {
	engine        *ruleengine.Engine
	sentinelSkill *SentinelSkill
	driver        db.Driver
}

// NewRuleSkill creates a RuleSkill backed by the community rule engine + JSON rules.
func NewRuleSkill(sentinelSkill *SentinelSkill, driver db.Driver) *RuleSkill {
	community := &ruleengine.CommunityProvider{}

	// Load JSON rules from embedded filesystem
	jsonProvider := ruleengine.NewJSONRuleProviderFromEmbedded("1.0.0")

	// Combine: JSON rules override community rules on ID conflict
	provider := ruleengine.NewCombinedProvider(community, jsonProvider)

	cfg := ruleengine.Config{
		OutputMode:      ruleengine.OutputSkill,
		MaxQueryTimeout: 10,
		MaxTreeDepth:    20, // JSON rules need 8+ levels
	}

	// Create QueryExecutor backed by real database connection
	var executor ruleengine.QueryExecutor
	if driver != nil {
		executor = ruleengine.NewDynamicQueryExecutor(
			driver,
			provider.QueryRegistry(),
			cfg.MaxQueryTimeout,
		)
	}

	engine := ruleengine.New(provider, executor, cfg)
	return &RuleSkill{
		engine:        engine,
		sentinelSkill: sentinelSkill,
		driver:        driver,
	}
}

func (s *RuleSkill) Name() string                       { return "rule" }
func (s *RuleSkill) Description() string                { return "Rule-based OG diagnosis (no LLM)" }
func (s *RuleSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *RuleSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "rule",
		Description: "Rule-based OpenGauss diagnosis (no LLM required)",
	}
}

func (s *RuleSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "rule",
		Aliases:  []string{"rulecheck", "rc"},
		Usage:    "/rule [<编号>|live]",
		Examples: []string{"/rule", "/rule 1", "/rule live"},
	}
}

func (s *RuleSkill) Validate(_ skill.Params) error { return nil }

func (s *RuleSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	var report *sentinel.BurstReport
	var reportLabel string

	if args == "live" || args == "now" || args == "current" {
		r, err := s.buildLiveReport(ctx)
		if err != nil {
			return nil, fmt.Errorf("构建实时报告: %w", err)
		}
		report = r
		reportLabel = "实时采集"
	} else if args != "" {
		n, err := strconv.Atoi(args)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("用法: /rule [编号|live], 如 /rule 1 或 /rule live")
		}
		if s.sentinelSkill == nil || s.sentinelSkill.ReportCount() == 0 {
			return &skill.Result{
				Type:     skill.ResultText,
				Rendered: "当前无异常报告。使用 /rule live 进行实时分析",
				Summary:  "无报告",
			}, nil
		}
		report = s.sentinelSkill.ReportAt(n)
		if report == nil {
			return &skill.Result{
				Type:     skill.ResultText,
				Rendered: fmt.Sprintf("编号 %d 超出范围, 当前共 %d 条", n, s.sentinelSkill.ReportCount()),
				Summary:  "out of range",
			}, nil
		}
		reportLabel = fmt.Sprintf("报告 #%d", n)
	} else {
		if s.sentinelSkill != nil && s.sentinelSkill.ReportCount() > 0 {
			count := s.sentinelSkill.ReportCount()
			report = s.sentinelSkill.ReportAt(count)
			reportLabel = fmt.Sprintf("报告 #%d", count)
		} else {
			r, err := s.buildLiveReport(ctx)
			if err != nil {
				return nil, fmt.Errorf("构建实时报告: %w", err)
			}
			report = r
			reportLabel = "实时采集"
		}
	}

	// Classify (sentinel-level classification for the monitoring summary).
	classification := sentinel.Classify(*report)
	report.Classification = classification

	// Run rule engine.
	input := &ruleengine.DiagInput{
		Type:   ruleengine.InputBurstReport,
		Report: report,
	}
	output := s.engine.Diagnose(input)

	// Build rendered output: monitoring summary + rule engine diagnosis.
	rendered := s.formatRuleOutput(reportLabel, report)
	rendered += ruleengine.FormatDiagOutput(output, ruleengine.Config{
		OutputMode: ruleengine.OutputSkill,
	})

	// Build summary.
	summary := "无规则匹配"
	if output.Primary != nil {
		summary = fmt.Sprintf("根因: %s (置信度 %d%%)",
			output.Primary.Cause, int(output.Primary.Confidence*100))
	} else if classification.Cause != sentinel.CauseUnknown {
		summary = fmt.Sprintf("根因: %s (置信度 %d%%)",
			classification.Cause.String(), int(classification.Confidence*100))
	}

	// Hint about record count.
	if s.sentinelSkill != nil {
		count := s.sentinelSkill.ReportCount()
		if count > 1 {
			rendered += fmt.Sprintf("\n  共 %d 条异常记录, /rule 选择分析", count)
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("规则诊断 — %s", summary),
	}, nil
}

func (s *RuleSkill) formatRuleOutput(label string, report *sentinel.BurstReport) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("── %s ──\n\n", label))

	// Trigger info.
	if report.TriggerEvent.Metric != "" {
		metricLabel := sentinel.MetricLabel(sentinel.MetricName(report.TriggerEvent.Metric))
		if report.TriggerEvent.Baseline > 0 {
			b.WriteString(fmt.Sprintf("  触发: %s  %.1f -> %.1f\n",
				metricLabel, report.TriggerEvent.Baseline, report.TriggerEvent.Current))
		} else {
			b.WriteString(fmt.Sprintf("  当前: %s = %.0f\n",
				metricLabel, report.TriggerEvent.Current))
		}
	}
	if report.DurationSec > 0 {
		b.WriteString(fmt.Sprintf("  持续: %.1fs\n", report.DurationSec))
	}

	// Classification.
	c := report.Classification
	if c.Cause != "" && c.Cause != sentinel.CauseUnknown {
		b.WriteString(fmt.Sprintf("\n  根因分类: %s (置信度 %.0f%%)\n",
			c.Cause.String(), c.Confidence*100))
		for _, ev := range c.Evidence {
			b.WriteString(fmt.Sprintf("    - %s\n", ev))
		}
	}

	return b.String()
}

// SQL for building live report from current OpenGauss state.
const (
	liveActiveSQL = `SELECT
  COUNT(*) FILTER (WHERE state = 'active') AS active,
  COUNT(*) FILTER (WHERE state = 'idle in transaction') AS idle_in_tx,
  COUNT(*) FILTER (WHERE waiting = true) AS lock_waits,
  COUNT(*) FILTER (WHERE state = 'active' AND EXTRACT(EPOCH FROM clock_timestamp()-query_start) > 30) AS long_queries
FROM pg_stat_activity WHERE pid != pg_backend_pid()`

	liveConnPctSQL = `SELECT
  COUNT(*) * 100.0 / NULLIF(current_setting('max_connections')::int, 0)
FROM pg_stat_activity`

	// OpenGauss: use pg_locks self-join instead of pg_blocking_pids.
	liveBlockerSQL = `SELECT COUNT(DISTINCT kl.pid)
FROM pg_locks bl
JOIN pg_locks kl ON kl.transactionid = bl.transactionid AND kl.pid != bl.pid
WHERE NOT bl.granted`
)

// buildLiveReport queries current OpenGauss state and constructs a BurstReport.
func (s *RuleSkill) buildLiveReport(ctx context.Context) (*sentinel.BurstReport, error) {
	report := &sentinel.BurstReport{
		Metrics: make(map[string]sentinel.MetricSummary),
	}

	// Active session counts.
	activeResult, err := s.driver.Query(ctx, liveActiveSQL)
	if err == nil && len(activeResult.Rows) > 0 && len(activeResult.Rows[0]) >= 4 {
		row := activeResult.Rows[0]
		active := toFloat(row[0])
		idleInTx := toFloat(row[1])
		lockWaits := toFloat(row[2])
		longQueries := toFloat(row[3])

		report.Metrics[string(sentinel.MetricActiveSessions)] = sentinel.MetricSummary{Avg: active, Max: active}
		report.Metrics[string(sentinel.MetricIdleInTransaction)] = sentinel.MetricSummary{Avg: idleInTx, Max: idleInTx}
		report.Metrics[string(sentinel.MetricLockWaits)] = sentinel.MetricSummary{Avg: lockWaits, Max: lockWaits}
		report.Metrics[string(sentinel.MetricLongQueries)] = sentinel.MetricSummary{Avg: longQueries, Max: longQueries}

		report.TriggerEvent = sentinel.TriggerEvent{
			Metric:  string(sentinel.MetricActiveSessions),
			Current: active,
		}
		report.PeakActive = int(active)
	}

	// Connection percentage.
	connResult, err := s.driver.Query(ctx, liveConnPctSQL)
	if err == nil && len(connResult.Rows) > 0 && len(connResult.Rows[0]) >= 1 {
		connPct := toFloat(connResult.Rows[0][0])
		report.Metrics[string(sentinel.MetricConnectionsPct)] = sentinel.MetricSummary{Avg: connPct, Max: connPct}
	}

	// Blocker count.
	blockerResult, err := s.driver.Query(ctx, liveBlockerSQL)
	if err == nil && len(blockerResult.Rows) > 0 && len(blockerResult.Rows[0]) >= 1 {
		blockers := toFloat(blockerResult.Rows[0][0])
		report.Metrics[string(sentinel.MetricBlockerCount)] = sentinel.MetricSummary{Avg: blockers, Max: blockers}
	}

	// Top SQLs (graceful: nil on error).
	report.TopSQLs = sentinel.CollectTopSQLs(ctx, s.driver)

	// Wait profile (graceful: nil on error).
	report.WaitProfile = sentinel.CollectWaitProfile(ctx, s.driver)

	// Blocking chains (graceful: nil on error).
	report.BlockingChains = sentinel.CollectBlockingChains(ctx, s.driver)

	// Extended metrics for JSON rule matching.
	s.collectExtendedMetrics(ctx, report)

	return report, nil
}

// collectExtendedMetrics gathers additional metrics that JSON rules reference.
func (s *RuleSkill) collectExtendedMetrics(ctx context.Context, report *sentinel.BurstReport) {
	dbStatsSQL := `SELECT
  COALESCE(ROUND(SUM(n_dead_tup)::numeric * 100 / NULLIF(SUM(n_live_tup + n_dead_tup), 0), 2), 0) AS dead_tuple_ratio,
  (SELECT ROUND(COALESCE(SUM(heap_blks_hit)::numeric * 100 / NULLIF(SUM(heap_blks_hit + heap_blks_read), 0), 100), 2) FROM pg_statio_user_tables) AS cache_hit_pct,
  (SELECT ROUND(100.0 * age(datfrozenxid) / 2000000000, 2) FROM pg_database WHERE datname = current_database()) AS xid_age_pct,
  (SELECT COUNT(*) FROM pg_stat_user_tables WHERE n_dead_tup > 10000 AND n_dead_tup > 0.1 * (n_live_tup + n_dead_tup)) AS bloated_tables
FROM pg_stat_user_tables`

	result, err := s.driver.Query(ctx, dbStatsSQL)
	if err == nil && len(result.Rows) > 0 && len(result.Rows[0]) >= 4 {
		row := result.Rows[0]
		report.Metrics["dead_tuple_ratio"] = sentinel.MetricSummary{Avg: toFloat(row[0]), Max: toFloat(row[0])}
		report.Metrics[string(sentinel.MetricCacheHitPct)] = sentinel.MetricSummary{Avg: toFloat(row[1]), Max: toFloat(row[1])}
		report.Metrics[string(sentinel.MetricXIDAgeRatio)] = sentinel.MetricSummary{Avg: toFloat(row[2]), Max: toFloat(row[2])}
		report.Metrics["bloated_tables"] = sentinel.MetricSummary{Avg: toFloat(row[3]), Max: toFloat(row[3])}
	}
}

func toFloat(v any) float64 {
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
	case uint64:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		// Fallback: convert to string then parse (handles *big.Float, decimal types, etc.)
		var f float64
		fmt.Sscanf(fmt.Sprint(n), "%f", &f)
		return f
	}
}
