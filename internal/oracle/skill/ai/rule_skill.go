/*-------------------------------------------------------------------------
 *
 * rule_skill.go
 *	  RuleSkill runs the deterministic rule engine (265 rules) on
 *	  sentinel reports. Works without LLM — provides root-cause
 *	  analysis, evidence chains, and remediation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/ai/rule_skill.go
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
	"github.com/sqlrush/opendb/internal/oracle/ruleengine"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
	orasqladv "github.com/sqlrush/opendb/internal/oracle/sqladvisor"
	"github.com/sqlrush/opendb/internal/skill"
)

// RuleSkill runs the deterministic rule engine (265 rules) on sentinel reports.
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

func (s *RuleSkill) Name() string                        { return "rule" }
func (s *RuleSkill) Description() string                 { return "Rule engine diagnosis (no LLM)" }
func (s *RuleSkill) SecurityLevel() skill.SecurityLevel   { return skill.LevelReadOnly }

func (s *RuleSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "rule",
		Description: "Rule-based diagnosis using 265 expert rules (no LLM required)",
	}
}

func (s *RuleSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "rule",
		Aliases:  []string{"rulecheck", "rc"},
		Usage:    "/rule [<编号>]",
		Examples: []string{"/rule", "/rule 1"},
	}
}

func (s *RuleSkill) Validate(_ skill.Params) error { return nil }

func (s *RuleSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	var report *sentinel.BurstReport
	var reportLabel string

	if args == "live" || args == "now" || args == "current" {
		// Live mode: query current database state directly.
		r, err := s.buildLiveReport(ctx)
		if err != nil {
			return nil, fmt.Errorf("构建实时报告: %w", err)
		}
		report = r
		reportLabel = "实时采集"
	} else if args != "" {
		// Numbered report mode.
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
		// Default: try sentinel, fall back to live.
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
		if report.TriggerEvent.Baseline > 0 {
			b.WriteString(fmt.Sprintf("  触发: %s  %.1f → %.1f\n", report.TriggerEvent.Metric, report.TriggerEvent.Baseline, report.TriggerEvent.Current))
		} else {
			b.WriteString(fmt.Sprintf("  当前: %s = %.0f\n", report.TriggerEvent.Metric, report.TriggerEvent.Current))
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
			if i >= 5 { break }
			bar := makeBar(w.Percentage, 20)
			waitRows = append(waitRows, []any{w.Event, fmt.Sprintf("%.1f%%", w.Percentage), bar, w.WaitClass})
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
			if i >= 5 { break }
			event := sq.Event
			if event == "" { event = "-" }
			elapsed := sq.MaxElapsedSec
			var elapsedStr string
			switch {
			case elapsed >= 1.0:
				elapsedStr = fmt.Sprintf("%.1fs", elapsed)
			case elapsed >= 0.001:
				elapsedStr = fmt.Sprintf("%.1fms", elapsed*1000)
			default:
				elapsedStr = "<1ms"
			}
			sqlRows = append(sqlRows, []any{
				sq.SQLID,
				elapsedStr,
				sq.MaxConcurrent,
				event,
			})
		}
		sqlQR := &db.QueryResult{
			Columns: []string{"SQL_ID", "最长耗时", "并发", "等待事件"},
			Rows:    sqlRows,
		}
		b.WriteString(format.FormatTableOpts(sqlQR, format.TableOptions{MaxRows: 10, TermWidth: 100}))
	}

	// Blocking chains.
	if len(report.BlockingChains) > 0 {
		b.WriteString("\n  阻塞链:\n")
		var blockRows [][]any
		for _, c := range report.BlockingChains {
			user := c.RootUser
			if user == "" {
				user = "-"
			}
			sqlID := c.RootSQLID
			if sqlID == "" {
				sqlID = "-"
			}
			event := c.RootEvent
			if event == "" {
				event = "-"
			}
			blockRows = append(blockRows, []any{
				c.RootSID, user, sqlID, event, c.VictimCount,
			})
		}
		blockQR := &db.QueryResult{
			Columns: []string{"SID", "阻塞者", "SQL_ID", "等待资源", "等待数"},
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

// makeBar creates a simple percentage bar: █████░░░░░
func makeBar(pct float64, width int) string {
	filled := int(pct / 100 * float64(width))
	if filled > width { filled = width }
	if filled < 0 { filled = 0 }
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// SQL for building live report from current database state.
const (
	liveWaitSQL = `SELECT NVL(event, 'ON CPU') AS event,
       NVL(wait_class, 'CPU') AS wait_class,
       COUNT(*) AS cnt,
       ROUND(COUNT(*)*100/NULLIF(SUM(COUNT(*)) OVER(), 0), 1) AS pct
FROM v$session
WHERE status = 'ACTIVE' AND username IS NOT NULL
  AND NVL(wait_class, 'CPU') != 'Idle'
GROUP BY event, wait_class
ORDER BY COUNT(*) DESC
FETCH FIRST 10 ROWS ONLY`

	liveActiveSQL = `SELECT COUNT(*) FROM v$session WHERE status = 'ACTIVE' AND username IS NOT NULL`

	liveTopSQLSQL = `SELECT s.sql_id,
       ROUND(COALESCE(
           MAX(q.elapsed_time/GREATEST(q.executions, 1))/1e6,
           MAX(s.last_call_et)
       ), 3) AS avg_sec,
       COUNT(*) AS concurrent,
       MAX(CASE WHEN s.wait_class != 'Idle' THEN s.event END) AS event
FROM v$session s
LEFT JOIN v$sql q ON s.sql_id = q.sql_id AND q.child_number = 0
WHERE s.status = 'ACTIVE' AND s.username IS NOT NULL AND s.sql_id IS NOT NULL
GROUP BY s.sql_id
ORDER BY COUNT(*) DESC
FETCH FIRST 5 ROWS ONLY`

	liveBlockSQL = `SELECT b.blocking_session AS root_sid,
       MAX(s.username) AS username,
       MAX(s.sql_id) AS blocker_sql,
       MAX(b.event) AS victim_event,
       COUNT(*) AS victims,
       MAX(s.event) AS blocker_event,
       MAX(s.status) AS blocker_status,
       MAX(s.command) AS blocker_command
FROM v$session b
LEFT JOIN v$session s ON s.sid = b.blocking_session
WHERE b.blocking_session IS NOT NULL
GROUP BY b.blocking_session
ORDER BY COUNT(*) DESC
FETCH FIRST 10 ROWS ONLY`

	liveSpaceSQL = `SELECT t.tablespace_name,
       ROUND(t.bytes/1024/1024) total_mb,
       ROUND(NVL(f.free_mb,0)) free_mb,
       ROUND((1 - NVL(f.free_mb,0)*1024*1024/t.bytes)*100, 1) used_pct
FROM (SELECT tablespace_name, SUM(bytes) bytes FROM dba_data_files GROUP BY tablespace_name) t
LEFT JOIN (SELECT tablespace_name, SUM(bytes)/1024/1024 free_mb FROM dba_free_space GROUP BY tablespace_name) f
ON t.tablespace_name = f.tablespace_name
WHERE (1 - NVL(f.free_mb,0)*1024*1024/t.bytes)*100 > 85
ORDER BY used_pct DESC`

	// SQL plan characteristics for a single SQL ID (bind variable :1).
	liveSQLPlanSQL = `SELECT
       MAX(CASE WHEN p.operation='TABLE ACCESS' AND p.options='FULL' THEN 1 ELSE 0 END) has_full_scan,
       MAX(CASE WHEN p.operation IN ('SORT ORDER BY','SORT AGGREGATE','SORT GROUP BY','SORT UNIQUE','SORT JOIN') THEN 1 ELSE 0 END) has_sort,
       MAX(CASE WHEN p.operation='HASH JOIN' THEN 1 ELSE 0 END) has_hash_join,
       MAX(CASE WHEN INSTR(p.operation,'INDEX') > 0 THEN 1 ELSE 0 END) has_index
FROM v$sql_plan p
WHERE p.sql_id = :1`

	// SQL area stats for a single SQL ID (bind variable :1).
	liveSQLStatsSQL = `SELECT SUM(invalidations) invalidations,
       COUNT(*) version_count,
       SUM(disk_reads) disk_reads,
       SUM(buffer_gets) buffer_gets,
       SUM(executions) executions,
       SUM(rows_processed) rows_processed
FROM v$sql
WHERE sql_id = :1`

	// Plan drift detection for a single SQL ID (bind variable :1).
	livePlanDriftSQL = `SELECT plan_hash_value,
       SUM(executions) execs,
       ROUND(SUM(elapsed_time)/GREATEST(SUM(executions),1)/1e6, 3) avg_sec
FROM v$sql
WHERE sql_id = :1
GROUP BY plan_hash_value
ORDER BY avg_sec`

	// PGA/TEMP usage.
	livePGATempSQL = `SELECT
       (SELECT ROUND(SUM(pga_used_mem)/1024/1024) FROM v$process) pga_used_mb,
       (SELECT ROUND(SUM(pga_alloc_mem)/1024/1024) FROM v$process) pga_alloc_mb,
       (SELECT ROUND(NVL(SUM(blocks*8192)/1024/1024,0)) FROM v$sort_usage) temp_used_mb,
       (SELECT value FROM v$parameter WHERE name='pga_aggregate_target') pga_target
FROM dual`

	// Row cache stats: check dc_sequences specifically + overall top type.
	liveRowCacheSQL = `SELECT
       (SELECT parameter FROM v$rowcache WHERE gets > 100 ORDER BY getmisses DESC FETCH FIRST 1 ROWS ONLY) top_type,
       (SELECT ROUND(getmisses/NULLIF(gets,0)*100,2) FROM v$rowcache WHERE gets > 100 ORDER BY getmisses DESC FETCH FIRST 1 ROWS ONLY) top_miss_pct,
       NVL((SELECT getmisses FROM v$rowcache WHERE parameter='dc_sequences' AND ROWNUM=1), 0) seq_misses,
       NVL((SELECT gets FROM v$rowcache WHERE parameter='dc_sequences' AND ROWNUM=1), 0) seq_gets,
       NVL((SELECT modifications FROM v$rowcache WHERE parameter='dc_sequences' AND ROWNUM=1), 0) seq_mods
FROM dual`

	// Hard parse rate: ratio of parse_calls that are hard parses.
	liveParseStatsSQL = `SELECT
       ROUND(hp.value * 100.0 / NULLIF(tp.value, 0), 2) AS hard_parse_pct,
       hp.value AS hard_parses,
       tp.value AS total_parses
FROM (SELECT value FROM v$sysstat WHERE name = 'parse count (hard)') hp,
     (SELECT value FROM v$sysstat WHERE name = 'parse count (total)') tp`

	// Key parameter checks for configuration validation.
	liveParamCheckSQL = `SELECT name, value FROM v$parameter
WHERE name IN ('undo_retention', 'open_cursors', 'optimizer_index_cost_adj',
               'db_file_multiblock_read_count', 'cursor_sharing',
               'parallel_max_servers', 'resource_manager_plan')
ORDER BY name`

	// Session/process usage percentage.
	liveSessionUsageSQL = `SELECT
       (SELECT COUNT(*) FROM v$session WHERE type='USER') AS user_sessions,
       (SELECT value FROM v$parameter WHERE name='sessions') AS max_sessions,
       (SELECT COUNT(*) FROM v$process) AS processes,
       (SELECT value FROM v$parameter WHERE name='processes') AS max_processes
FROM dual`
)

// buildLiveReport queries current v$session state and constructs a BurstReport.
func (s *RuleSkill) buildLiveReport(ctx context.Context) (*sentinel.BurstReport, error) {
	report := &sentinel.BurstReport{
		Metrics: make(map[string]sentinel.MetricSummary),
	}

	// Wait profile from active sessions.
	waitResult, err := s.driver.Query(ctx, liveWaitSQL)
	if err == nil {
		for _, row := range waitResult.Rows {
			event := fmt.Sprintf("%v", row[0])
			waitClass := fmt.Sprintf("%v", row[1])
			pct := toFloat(row[3])
			report.WaitProfile = append(report.WaitProfile, sentinel.WaitBucket{
				Event:      event,
				WaitClass:  waitClass,
				Percentage: pct,
			})
		}
	}

	// Active session count.
	activeResult, err := s.driver.Query(ctx, liveActiveSQL)
	if err == nil && len(activeResult.Rows) > 0 {
		active := toFloat(activeResult.Rows[0][0])
		report.Metrics["active_sessions"] = sentinel.MetricSummary{Avg: active, Max: active}
		report.TriggerEvent = sentinel.TriggerEvent{
			Metric:  "active_sessions",
			Current: active,
		}
	}

	// Top SQL (from active sessions with concurrent count and wait event).
	sqlResult, err := s.driver.Query(ctx, liveTopSQLSQL)
	if err == nil {
		for _, row := range sqlResult.Rows {
			sqlID := fmt.Sprintf("%v", row[0])
			avgSec := toFloat(row[1])
			concurrent := int(toFloat(row[2]))
			event := ""
			if len(row) > 3 && row[3] != nil {
				event = fmt.Sprintf("%v", row[3])
			}
			report.TopSQLs = append(report.TopSQLs, sentinel.SQLProfile{
				SQLID:          sqlID,
				AvgElapsedSec:  avgSec,
				MaxElapsedSec:  avgSec,
				MaxConcurrent:  concurrent,
				Event:          event,
			})
		}
	}

	// Enrich Top SQL with plan characteristics and stats (only if we have SQL IDs).
	if len(report.TopSQLs) > 0 {
		// Build IN-list for Top 3 SQL IDs.
		limit := 3
		if len(report.TopSQLs) < limit {
			limit = len(report.TopSQLs)
		}
		_ = strings.Join // keep import used

		// Execution plan features — query per SQL ID.
		for i := 0; i < limit; i++ {
			sid := report.TopSQLs[i].SQLID
			planSQL := strings.Replace(liveSQLPlanSQL, ":1", "'"+sid+"'", 1)
			planResult, planErr := s.driver.Query(ctx, planSQL)
			if planErr == nil && len(planResult.Rows) > 0 {
				row := planResult.Rows[0]
				report.TopSQLs[i].HasFullScan = toFloat(row[0]) > 0
				report.TopSQLs[i].HasSort = toFloat(row[1]) > 0
				report.TopSQLs[i].HasHashJoin = toFloat(row[2]) > 0
				report.TopSQLs[i].HasIndex = toFloat(row[3]) > 0
			}
		}

		// SQL area stats + plan drift — query per SQL ID.
		for i := 0; i < limit; i++ {
			sid := report.TopSQLs[i].SQLID

			// Stats.
			statsSQL := strings.Replace(liveSQLStatsSQL, ":1", "'"+sid+"'", 1)
			statsResult, statsErr := s.driver.Query(ctx, statsSQL)
			if statsErr == nil && len(statsResult.Rows) > 0 {
				row := statsResult.Rows[0]
				report.TopSQLs[i].Invalidations = int(toFloat(row[0]))
				report.TopSQLs[i].VersionCount = int(toFloat(row[1]))
				report.TopSQLs[i].DiskReads = int64(toFloat(row[2]))
				report.TopSQLs[i].BufferGets = int64(toFloat(row[3]))
				report.TopSQLs[i].Executions = int64(toFloat(row[4]))
				report.TopSQLs[i].RowsProcessed = int64(toFloat(row[5]))
			}

			// Plan drift.
			driftSQL := strings.Replace(livePlanDriftSQL, ":1", "'"+sid+"'", 1)
			driftResult, driftErr := s.driver.Query(ctx, driftSQL)
			if driftErr == nil && len(driftResult.Rows) > 0 {
				planCount := len(driftResult.Rows)
				bestSec := toFloat(driftResult.Rows[0][2])                       // ORDER BY avg_sec ASC → first is best
				lastSec := toFloat(driftResult.Rows[len(driftResult.Rows)-1][2]) // last is worst
				report.TopSQLs[i].PlanCount = planCount
				report.TopSQLs[i].BestPlanAvgSec = bestSec
				report.TopSQLs[i].CurrentPlanAvgSec = lastSec
			}
		}

		// SQL Advisor analysis for full-scan or slow SQLs.
		advisor := orasqladv.New(s.driver)
		for i := range report.TopSQLs {
			sql := &report.TopSQLs[i]
			if sql.HasFullScan || sql.AvgElapsedSec > 1.0 {
				advReport, err := advisor.Analyze(ctx, sql.SQLID)
				if err == nil && len(advReport.Findings) > 0 {
					sql.AdvisorReport = advReport
				}
			}
		}

		// PGA/TEMP usage.
		pgaResult, pgaErr := s.driver.Query(ctx, livePGATempSQL)
		if pgaErr == nil && len(pgaResult.Rows) > 0 {
			pgaUsed := toFloat(pgaResult.Rows[0][0])
			pgaTarget := toFloat(pgaResult.Rows[0][3])
			tempUsed := toFloat(pgaResult.Rows[0][2])
			if pgaTarget > 0 {
				pgaPct := pgaUsed * 100.0 / pgaTarget
				report.Metrics["pga_used_pct"] = sentinel.MetricSummary{Avg: pgaPct, Max: pgaPct}
			}
			report.Metrics["temp_used_mb"] = sentinel.MetricSummary{Avg: tempUsed, Max: tempUsed}
		}
	}

	// Blocking chains.
	blockResult, err := s.driver.Query(ctx, liveBlockSQL)
	if err == nil {
		for _, row := range blockResult.Rows {
			sid := int(toFloat(row[0]))
			username := nilStr(row[1])
			blockerSQL := nilStr(row[2])
			victimEvent := nilStr(row[3])
			victims := int(toFloat(row[4]))
			blockerEvent := ""
			blockerStatus := ""
			if len(row) > 5 {
				blockerEvent = nilStr(row[5])
			}
			if len(row) > 6 {
				blockerStatus = nilStr(row[6])
			}
			blockerCmd := 0
			if len(row) > 7 {
				blockerCmd = int(toFloat(row[7]))
			}
			report.BlockingChains = append(report.BlockingChains, sentinel.BlockingChain{
				RootSID:        sid,
				RootUser:       username,
				RootSQLID:      blockerSQL,
				RootEvent:      victimEvent,
				VictimCount:    victims,
				BlockerEvent:   blockerEvent,
				BlockerStatus:  blockerStatus,
				BlockerCommand: blockerCmd,
			})
		}
	}

	// Row cache stats — for WD015 row cache lock diagnosis.
	rcResult, err := s.driver.Query(ctx, liveRowCacheSQL)
	if err == nil && len(rcResult.Rows) > 0 {
		topType := nilStr(rcResult.Rows[0][0])
		topMissPct := toFloat(rcResult.Rows[0][1])
		seqMisses := toFloat(rcResult.Rows[0][2])
		seqMods := toFloat(rcResult.Rows[0][4])

		// Determine if dc_sequences contention is from user sequences (NEXTVAL)
		// or from internal DDL operations (OBJ# allocation).
		// DDL produces high dc_objects modifications; NEXTVAL produces high dc_sequences getmisses.
		objResult, objErr := s.driver.Query(ctx,
			`SELECT NVL(modifications, 0) FROM v$rowcache WHERE parameter='dc_objects' AND ROWNUM=1`)
		var objMods float64
		if objErr == nil && len(objResult.Rows) > 0 {
			objMods = toFloat(objResult.Rows[0][0])
		}
		if seqMisses > 500 && objMods < seqMisses {
			topType = "dc_sequences"
		} else if objMods > 100 && objMods > seqMisses*0.3 {
			topType = "dc_objects"
		} else if seqMods > 1000 && seqMisses > 500 {
			topType = "dc_sequences"
		}
		report.Metrics["row_cache_top_type"] = sentinel.MetricSummary{Trend: topType}
		report.Metrics["row_cache_miss_pct"] = sentinel.MetricSummary{Avg: topMissPct, Max: topMissPct}
	}

	// Hard parse stats — for shared pool latch and hard parse storm diagnosis.
	parseResult, err := s.driver.Query(ctx, liveParseStatsSQL)
	if err == nil && len(parseResult.Rows) > 0 {
		hardParsePct := toFloat(parseResult.Rows[0][0])
		report.Metrics["hard_parse_pct"] = sentinel.MetricSummary{Avg: hardParsePct, Max: hardParsePct}
	}

	// Parameter checks — key database parameters for configuration validation.
	paramResult, err := s.driver.Query(ctx, liveParamCheckSQL)
	if err == nil {
		for _, row := range paramResult.Rows {
			name := nilStr(row[0])
			val := nilStr(row[1])
			numVal := toFloat(row[1])
			report.Metrics["param_"+name] = sentinel.MetricSummary{Avg: numVal, Max: numVal, Trend: val}
		}
	}

	// Session/process usage — detect near-limit conditions.
	sessResult, err := s.driver.Query(ctx, liveSessionUsageSQL)
	if err == nil && len(sessResult.Rows) > 0 {
		userSess := toFloat(sessResult.Rows[0][0])
		maxSess := toFloat(sessResult.Rows[0][1])
		procs := toFloat(sessResult.Rows[0][2])
		maxProcs := toFloat(sessResult.Rows[0][3])
		if maxSess > 0 {
			sessPct := userSess * 100.0 / maxSess
			report.Metrics["sessions_used_pct"] = sentinel.MetricSummary{Avg: sessPct, Max: sessPct}
		}
		if maxProcs > 0 {
			procPct := procs * 100.0 / maxProcs
			report.Metrics["processes_used_pct"] = sentinel.MetricSummary{Avg: procPct, Max: procPct}
		}
	}

	// --- Session distribution by source ---
	sessionDistSQL := `SELECT username, machine, program, status, COUNT(*) cnt
FROM v$session WHERE type='USER'
GROUP BY username, machine, program, status
ORDER BY cnt DESC FETCH FIRST 20 ROWS ONLY`
	if distResult, err := s.driver.Query(ctx, sessionDistSQL); err == nil {
		report.Metrics["session_distribution_available"] = sentinel.MetricSummary{Avg: float64(len(distResult.Rows))}
	}

	// --- Idle session age distribution ---
	idleAgeSQL := `SELECT COUNT(*) total_idle,
  SUM(CASE WHEN (SYSDATE-logon_time)*1440 > 60 THEN 1 ELSE 0 END) idle_gt_1h,
  SUM(CASE WHEN (SYSDATE-logon_time)*1440 > 480 THEN 1 ELSE 0 END) idle_gt_8h
FROM v$session WHERE status='INACTIVE' AND type='USER'`
	if idleResult, err := s.driver.Query(ctx, idleAgeSQL); err == nil && len(idleResult.Rows) > 0 {
		row := idleResult.Rows[0]
		totalIdle := toFloat(row[0])
		idleGt1h := toFloat(row[1])
		idleGt8h := toFloat(row[2])
		report.Metrics["idle_sessions_total"] = sentinel.MetricSummary{Avg: totalIdle}
		report.Metrics["idle_sessions_gt_1h"] = sentinel.MetricSummary{Avg: idleGt1h}
		report.Metrics["idle_sessions_gt_8h"] = sentinel.MetricSummary{Avg: idleGt8h}
		if totalIdle > 0 {
			activeSess := float64(0)
			if m, ok := report.Metrics["active_sessions"]; ok {
				activeSess = m.Avg
			}
			report.Metrics["idle_session_ratio"] = sentinel.MetricSummary{Avg: totalIdle / (totalIdle + activeSess)}
		}
	}

	// --- Resource limit check ---
	resLimitSQL := `SELECT resource_name, current_utilization, max_utilization,
  TO_NUMBER(DECODE(limit_value,'UNLIMITED','0',limit_value)) limit_val
FROM v$resource_limit WHERE resource_name IN ('sessions','processes')`
	if rlResult, err := s.driver.Query(ctx, resLimitSQL); err == nil {
		for _, row := range rlResult.Rows {
			name := fmt.Sprintf("%v", row[0])
			curr := toFloat(row[1])
			limit := toFloat(row[3])
			if limit > 0 {
				pct := curr * 100.0 / limit
				report.Metrics[name+"_limit_pct"] = sentinel.MetricSummary{Avg: pct, Max: pct}
			}
		}
	}

	// Space details — tablespaces >85% used (capacity check even when idle).
	spaceResult, err := s.driver.Query(ctx, liveSpaceSQL)
	if err == nil {
		for _, row := range spaceResult.Rows {
			if len(row) < 4 {
				continue
			}
			report.SpaceDetails = append(report.SpaceDetails, sentinel.SpaceDetail{
				Name:    nilStr(row[0]),
				TotalMB: toFloat(row[1]),
				UsedPct: toFloat(row[3]),
			})
		}
	}

	return report, nil
}

func nilStr(v any) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprintf("%v", v)
	if s == "<nil>" {
		return ""
	}
	return s
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
