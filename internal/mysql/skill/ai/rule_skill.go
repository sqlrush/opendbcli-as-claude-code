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
 *	  internal/mysql/skill/ai/rule_skill.go
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
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/mysql/ruleengine"
	"github.com/sqlrush/opendb/internal/mysql/sentinel"
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
func (s *RuleSkill) Description() string                { return "Rule-based diagnosis (no LLM)" }
func (s *RuleSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *RuleSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "rule",
		Description: "Rule-based diagnosis using sentinel classification (no LLM required)",
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

	// Run rule engine.
	input := &ruleengine.DiagInput{
		Type:   ruleengine.InputBurstReport,
		Report: report,
	}
	output := s.engine.Diagnose(input)

	// Build beautiful rendered output.
	rendered := s.formatRuleOutput(reportLabel, report, output)

	// Build summary.
	summary := "无规则匹配"
	if output.Primary != nil {
		summary = fmt.Sprintf("根因: %s (置信度 %d%%)",
			output.Primary.Cause, int(output.Primary.Confidence*100))
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

// formatRuleOutput builds the complete rule diagnosis output with monitoring summary + diagnosis.
func (s *RuleSkill) formatRuleOutput(label string, report *sentinel.BurstReport, output *ruleengine.DiagOutput) string {
	var b strings.Builder

	// ── Section 1: 监控概要 ──
	b.WriteString(fmt.Sprintf("── %s ──\n\n", label))

	// Trigger info.
	if report.TriggerEvent.Metric != "" {
		metricLabel := sentinel.MetricLabel(sentinel.MetricName(report.TriggerEvent.Metric))
		unit := sentinel.MetricUnit(sentinel.MetricName(report.TriggerEvent.Metric))
		if report.TriggerEvent.Baseline > 0 {
			b.WriteString(fmt.Sprintf("  触发: %s  %.1f -> %.1f%s\n",
				metricLabel, report.TriggerEvent.Baseline, report.TriggerEvent.Current, unit))
		} else {
			b.WriteString(fmt.Sprintf("  当前: %s = %.0f%s\n",
				metricLabel, report.TriggerEvent.Current, unit))
		}
	}
	if report.DurationSec > 0 {
		b.WriteString(fmt.Sprintf("  持续: %.1fs\n", report.DurationSec))
	}

	// Wait profile as table.
	if len(report.WaitProfile) > 0 {
		b.WriteString("\n  等待事件分布:\n")
		var waitRows [][]any
		for i, w := range report.WaitProfile {
			if i >= 5 {
				break
			}
			bar := makeBar(w.Percentage, 20)
			waitRows = append(waitRows, []any{w.EventName, fmt.Sprintf("%.1f%%", w.Percentage), bar, w.WaitClass})
		}
		waitQR := &db.QueryResult{
			Columns: []string{"EVENT", "PCT", "分布", "WAIT_CLASS"},
			Rows:    waitRows,
		}
		b.WriteString(format.FormatTableOpts(waitQR, format.TableOptions{MaxRows: 10, TermWidth: 100}))
	}

	// Top SQL as table.
	if len(report.TopSQLs) > 0 {
		b.WriteString("\n  Top SQL:\n")
		var sqlRows [][]any
		for i, sq := range report.TopSQLs {
			if i >= 5 {
				break
			}
			var latencyStr string
			switch {
			case sq.MaxLatencyMs >= 1000:
				latencyStr = fmt.Sprintf("%.1fs", sq.MaxLatencyMs/1000)
			case sq.MaxLatencyMs >= 1:
				latencyStr = fmt.Sprintf("%.1fms", sq.MaxLatencyMs)
			default:
				latencyStr = "<1ms"
			}
			sqlRows = append(sqlRows, []any{
				sq.Digest,
				latencyStr,
				sq.ExecCount,
				fmt.Sprintf("%.1fms", sq.LockTimeMs),
			})
		}
		sqlQR := &db.QueryResult{
			Columns: []string{"DIGEST", "最长耗时", "执行数", "锁等待"},
			Rows:    sqlRows,
		}
		b.WriteString(format.FormatTableOpts(sqlQR, format.TableOptions{MaxRows: 10, TermWidth: 100}))
	}

	// Blocking chains.
	if len(report.BlockingChains) > 0 {
		b.WriteString("\n  阻塞链:\n")
		var blockRows [][]any
		for _, chain := range report.BlockingChains {
			user := chain.BlockerUser
			if user == "" {
				user = "-"
			}
			waitType := chain.WaitType
			if waitType == "" {
				waitType = "-"
			}
			blockRows = append(blockRows, []any{
				chain.BlockerThreadID, user, waitType, chain.VictimCount,
			})
		}
		blockQR := &db.QueryResult{
			Columns: []string{"THREAD_ID", "阻塞者", "等待类型", "等待数"},
			Rows:    blockRows,
		}
		b.WriteString(format.FormatTableOpts(blockQR, format.TableOptions{MaxRows: 20, TermWidth: 100}))
	}

	// ── Section 2: 规则诊断 ──
	b.WriteString("\n")
	b.WriteString(ruleengine.FormatDiagOutput(output, ruleengine.Config{
		OutputMode: ruleengine.OutputSkill,
	}))

	return b.String()
}

// makeBar creates a simple percentage bar.
func makeBar(pct float64, width int) string {
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// buildLiveReport queries current MySQL state and constructs a BurstReport.
func (s *RuleSkill) buildLiveReport(ctx context.Context) (*sentinel.BurstReport, error) {
	report := &sentinel.BurstReport{
		Metrics: make(map[string]sentinel.MetricSummary),
	}

	// Active threads.
	fastCollector := sentinel.NewFastProbeCollector(s.driver)
	probeValues, err := fastCollector.Probe(ctx)
	if err == nil {
		if v, ok := probeValues[sentinel.MetricThreadsRunning]; ok {
			report.Metrics[string(sentinel.MetricThreadsRunning)] = sentinel.MetricSummary{Avg: v, Max: v}
			report.PeakActive = int(v)
		}
		if v, ok := probeValues[sentinel.MetricLockWaits]; ok {
			report.Metrics[string(sentinel.MetricLockWaits)] = sentinel.MetricSummary{Avg: v, Max: v}
		}
		report.TriggerEvent = sentinel.TriggerEvent{
			Metric:  string(sentinel.MetricThreadsRunning),
			Current: probeValues[sentinel.MetricThreadsRunning],
		}
	}

	// Top SQL from performance_schema.
	report.TopSQLs = sentinel.CollectTopSQL(ctx, s.driver)

	// Blocking chains.
	report.BlockingChains = sentinel.CollectBlockingChains(ctx, s.driver)

	// Wait profile from performance_schema.
	report.WaitProfile = sentinel.CollectWaitProfile(ctx, s.driver)

	// Extended metrics for Go rule matching.
	s.collectExtendedMetrics(ctx, report)

	return report, nil
}

// collectExtendedMetrics gathers InnoDB / global status metrics that Go rules reference.
func (s *RuleSkill) collectExtendedMetrics(ctx context.Context, report *sentinel.BurstReport) {
	// InnoDB metrics + connection/buffer pool stats in one query.
	statusSQL := `SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Innodb_row_lock_waits') AS row_lock_waits,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Innodb_deadlocks') AS deadlocks,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Innodb_buffer_pool_read_requests') AS bp_reads,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Innodb_buffer_pool_reads') AS bp_disk_reads,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Threads_connected') AS threads_connected,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME = 'max_connections') AS max_conn,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Created_tmp_disk_tables') AS tmp_disk,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Created_tmp_tables') AS tmp_total,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Seconds_Behind_Master') AS repl_lag,
  (SELECT COUNT FROM information_schema.INNODB_METRICS WHERE NAME = 'trx_rseg_history_len') AS history_len`

	result, err := s.driver.Query(ctx, statusSQL)
	if err != nil || len(result.Rows) == 0 || len(result.Rows[0]) < 10 {
		return
	}
	row := result.Rows[0]

	setMetric := func(name string, val float64) {
		report.Metrics[name] = sentinel.MetricSummary{Avg: val, Max: val}
	}

	rowLockWaits := toFloat(row[0])
	deadlocks := toFloat(row[1])
	bpReads := toFloat(row[2])
	bpDiskReads := toFloat(row[3])
	threadsConn := toFloat(row[4])
	maxConn := toFloat(row[5])
	tmpDisk := toFloat(row[6])
	tmpTotal := toFloat(row[7])
	replLag := toFloat(row[8])
	historyLen := toFloat(row[9])

	setMetric(string(sentinel.MetricRowLockWaits), rowLockWaits)
	setMetric(string(sentinel.MetricDeadlocks), deadlocks)
	setMetric(string(sentinel.MetricThreadsConnected), threadsConn)
	setMetric(string(sentinel.MetricHistoryList), historyLen)
	setMetric(string(sentinel.MetricReplicationLag), replLag)

	// Buffer pool hit %
	if bpReads > 0 {
		hitPct := (1.0 - bpDiskReads/bpReads) * 100.0
		if hitPct < 0 {
			hitPct = 0
		}
		setMetric(string(sentinel.MetricBufferPoolHit), hitPct)
	}

	// Connection %
	if maxConn > 0 {
		connPct := threadsConn / maxConn * 100.0
		setMetric(string(sentinel.MetricConnectionsPct), connPct)
	}

	// Tmp disk tables %
	if tmpTotal > 0 {
		tmpDiskPct := tmpDisk / tmpTotal * 100.0
		setMetric(string(sentinel.MetricTmpDiskPct), tmpDiskPct)
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
		var f float64
		fmt.Sscanf(fmt.Sprint(n), "%f", &f)
		return f
	}
}
