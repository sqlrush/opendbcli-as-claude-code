/*-------------------------------------------------------------------------
 *
 * awr.go
 *	  AWRSkill shows AWR snapshot analysis: list snapshots, top SQL, and
 *	  top wait events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/awr.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const awrSnapshotListSQL = `SELECT s.snap_id,
       TO_CHAR(s.begin_interval_time, 'MM-DD HH24:MI') AS begin_time,
       TO_CHAR(s.end_interval_time, 'MM-DD HH24:MI') AS end_time,
       ROUND(EXTRACT(SECOND FROM (s.end_interval_time - s.begin_interval_time)) +
             EXTRACT(MINUTE FROM (s.end_interval_time - s.begin_interval_time)) * 60, 0) AS dur_sec
FROM dba_hist_snapshot s
ORDER BY s.snap_id DESC
FETCH FIRST 20 ROWS ONLY`

const awrTopSQLSQL = `SELECT sql_id,
       ROUND(SUM(elapsed_time_delta)/1e6, 2) AS elapsed_sec,
       SUM(executions_delta) AS execs,
       ROUND(SUM(elapsed_time_delta)/GREATEST(SUM(executions_delta),1)/1e6, 4) AS avg_sec,
       ROUND(SUM(buffer_gets_delta)/GREATEST(SUM(executions_delta),1)) AS avg_gets
FROM dba_hist_sqlstat
WHERE snap_id BETWEEN :1 AND :2
GROUP BY sql_id
ORDER BY SUM(elapsed_time_delta) DESC
FETCH FIRST 15 ROWS ONLY`

const awrTopWaitSQL = `SELECT event_name,
       wait_class,
       SUM(total_waits_fg) AS waits,
       ROUND(SUM(total_timeouts_fg)) AS timeouts,
       ROUND(SUM(time_waited_micro_fg)/1e6, 2) AS time_sec
FROM dba_hist_system_event
WHERE snap_id BETWEEN :1 AND :2
  AND wait_class != 'Idle'
GROUP BY event_name, wait_class
ORDER BY SUM(time_waited_micro_fg) DESC
FETCH FIRST 15 ROWS ONLY`

// AWRSkill shows AWR snapshot analysis: list snapshots, top SQL, and top wait events.
type AWRSkill struct {
	driver db.Driver
}

// NewAWRSkill creates an AWRSkill backed by the given driver.
func NewAWRSkill(driver db.Driver) *AWRSkill {
	return &AWRSkill{driver: driver}
}

func (s *AWRSkill) Name() string                      { return "awr" }
func (s *AWRSkill) Description() string               { return "Show AWR snapshot analysis" }
func (s *AWRSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *AWRSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "awr",
		Description: "Show AWR snapshot analysis: list snapshots, top SQL, top wait events",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "[snap_id] or [begin_snap end_snap]",
				},
			},
		},
	}
}

func (s *AWRSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "awr",
		Usage:    "/awr [snap_id | begin_snap end_snap]",
		Examples: []string{"/awr", "/awr 1234", "/awr 1230 1234"},
	}
}

func (s *AWRSkill) Validate(_ skill.Params) error { return nil }

func (s *AWRSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	parts := strings.Fields(args)

	switch len(parts) {
	case 0:
		return s.listSnapshots(ctx)
	case 1:
		return s.analyzeSnapshot(ctx, parts[0])
	default:
		return s.compareSnapshots(ctx, parts[0], parts[1])
	}
}

// listSnapshots shows recent AWR snapshots.
func (s *AWRSkill) listSnapshots(ctx context.Context) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, awrSnapshotListSQL)
	if err != nil {
		return nil, fmt.Errorf("查询 AWR 快照: %w", err)
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "未找到 AWR 快照, 请确认 AWR 已启用",
			Summary:  "no AWR snapshots found",
		}, nil
	}

	// Build snapshot list lines for Panel.
	header := fmt.Sprintf(" %-8s  %-12s  %-12s  %s", "SNAP_ID", "BEGIN_TIME", "END_TIME", "DUR(s)")
	var lines []string
	lines = append(lines, header)
	for _, row := range result.Rows {
		snapID := awrStr(row[0])
		begin := awrStr(row[1])
		end := awrStr(row[2])
		dur := awrStr(row[3])
		lines = append(lines, fmt.Sprintf(" %-8s  %-12s  %-12s  %s", snapID, begin, end, dur))
	}

	sections := []format.PanelSection{
		{Lines: lines},
		{
			Header: "用法",
			Lines: []string{
				" /awr <snap_id>               分析单个快照",
				" /awr <begin_snap> <end_snap>  对比两个快照",
			},
		},
	}

	title := fmt.Sprintf("AWR 快照列表 (最近 %d 个)", len(result.Rows))
	rendered := format.Panel(title, sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("AWR 快照列表 — %d 个", len(result.Rows)),
	}, nil
}

// analyzeSnapshot finds the previous snapshot and compares with the given one.
func (s *AWRSkill) analyzeSnapshot(ctx context.Context, snapStr string) (*skill.Result, error) {
	snapID, err := strconv.Atoi(snapStr)
	if err != nil {
		return nil, fmt.Errorf("无效的 snap_id: %s", snapStr)
	}

	beginSnap := snapID - 1
	return s.compareSnapshots(ctx, strconv.Itoa(beginSnap), strconv.Itoa(snapID))
}

// awrSnapTimeSQL retrieves time range for AWR snapshots.
const awrSnapTimeSQL = `SELECT
  TO_CHAR(MIN(s.begin_interval_time), 'MM-DD HH24:MI') AS begin_time,
  TO_CHAR(MAX(s.end_interval_time), 'MM-DD HH24:MI') AS end_time,
  ROUND(EXTRACT(DAY FROM (MAX(s.end_interval_time) - MIN(s.begin_interval_time))) * 86400 +
        EXTRACT(HOUR FROM (MAX(s.end_interval_time) - MIN(s.begin_interval_time))) * 3600 +
        EXTRACT(MINUTE FROM (MAX(s.end_interval_time) - MIN(s.begin_interval_time))) * 60 +
        EXTRACT(SECOND FROM (MAX(s.end_interval_time) - MIN(s.begin_interval_time))), 0) AS dur_sec
FROM dba_hist_snapshot s
WHERE s.snap_id BETWEEN :1 AND :2`

// compareSnapshots shows top SQL and top wait events between two snapshots.
func (s *AWRSkill) compareSnapshots(ctx context.Context, beginStr, endStr string) (*skill.Result, error) {
	beginSnap, err := strconv.Atoi(beginStr)
	if err != nil {
		return nil, fmt.Errorf("无效的 begin_snap: %s", beginStr)
	}
	endSnap, err := strconv.Atoi(endStr)
	if err != nil {
		return nil, fmt.Errorf("无效的 end_snap: %s", endStr)
	}
	if beginSnap >= endSnap {
		return nil, fmt.Errorf("begin_snap (%d) 必须小于 end_snap (%d)", beginSnap, endSnap)
	}

	// Query time range for the snapshots.
	timeResult, _ := s.driver.Query(ctx, awrSnapTimeSQL, beginSnap, endSnap)
	sqlResult, sqlErr := s.driver.Query(ctx, awrTopSQLSQL, beginSnap, endSnap)
	waitResult, waitErr := s.driver.Query(ctx, awrTopWaitSQL, beginSnap, endSnap)

	// Build time period info line.
	timePeriod := ""
	if timeResult != nil && len(timeResult.Rows) > 0 {
		row := timeResult.Rows[0]
		beginTime := awrStr(row[0])
		endTime := awrStr(row[1])
		durSec := awrStr(row[2])
		if beginTime != "" && endTime != "" {
			dur, _ := strconv.Atoi(durSec)
			timePeriod = fmt.Sprintf(" 时间段: %s → %s (%dm %ds)", beginTime, endTime, dur/60, dur%60)
		}
	}

	// Build Top SQL section lines.
	var sqlLines []string
	if sqlErr != nil {
		sqlLines = []string{fmt.Sprintf(" 查询失败: %v", sqlErr)}
	} else if len(sqlResult.Rows) == 0 {
		sqlLines = []string{" (无数据)"}
	} else {
		sqlHeader := fmt.Sprintf(" %-14s %10s %10s %10s %10s", "SQL_ID", "ELAPSED(s)", "EXECS", "AVG(s)", "AVG_GETS")
		sqlLines = append(sqlLines, sqlHeader)
		for _, row := range sqlResult.Rows {
			sqlID := awrStr(row[0])
			elapsed := awrStr(row[1])
			execs := format.HumanNumber(awrFloat(row[2]))
			avg := awrStr(row[3])
			avgGets := format.HumanNumber(awrFloat(row[4]))
			sqlLines = append(sqlLines, fmt.Sprintf(" %-14s %10s %10s %10s %10s", sqlID, elapsed, execs, avg, avgGets))
		}
	}

	// Build Top Wait Events section lines.
	var waitLines []string
	if waitErr != nil {
		waitLines = []string{fmt.Sprintf(" 查询失败: %v", waitErr)}
	} else if len(waitResult.Rows) == 0 {
		waitLines = []string{" (无数据)"}
	} else {
		waitHeader := fmt.Sprintf(" %-30s %-14s %10s %10s %10s", "EVENT", "WAIT_CLASS", "WAITS", "TIMEOUTS", "TIME(s)")
		waitLines = append(waitLines, waitHeader)
		for _, row := range waitResult.Rows {
			event := awrStr(row[0])
			if len(event) > 30 {
				event = event[:27] + "..."
			}
			wclass := awrStr(row[1])
			waits := format.HumanNumber(awrFloat(row[2]))
			timeouts := format.HumanNumber(awrFloat(row[3]))
			timeSec := awrStr(row[4])
			waitLines = append(waitLines, fmt.Sprintf(" %-30s %-14s %10s %10s %10s", event, wclass, waits, timeouts, timeSec))
		}
	}

	// Build sections.
	var sections []format.PanelSection
	if timePeriod != "" {
		sections = append(sections, format.PanelSection{Lines: []string{timePeriod}})
	}
	sections = append(sections,
		format.PanelSection{Header: "Top SQL (by elapsed time)", Lines: sqlLines},
		format.PanelSection{Header: "Top Wait Events", Lines: waitLines},
	)

	title := fmt.Sprintf("AWR 分析: snap %d → %d", beginSnap, endSnap)
	rendered := format.Panel(title, sections)

	sqlCount := 0
	if sqlErr == nil {
		sqlCount = len(sqlResult.Rows)
	}
	waitCount := 0
	if waitErr == nil {
		waitCount = len(waitResult.Rows)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     sqlResult,
		Rendered: rendered,
		Summary:  fmt.Sprintf("AWR 快照对比 (snap %d → %d) — %d SQL, %d 等待事件", beginSnap, endSnap, sqlCount, waitCount),
	}, nil
}

// awrStr safely converts a row value to string.
func awrStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// awrFloat converts a row value to float64.
func awrFloat(v any) float64 {
	if v == nil {
		return 0
	}
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
	default:
		f, _ := strconv.ParseFloat(fmt.Sprintf("%v", v), 64)
		return f
	}
}
