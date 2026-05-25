/*-------------------------------------------------------------------------
 *
 * perfsnap.go
 *	  perfsnap — PerfSnapSkill plus helpers (NewPerfSnapSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/perfsnap.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// DM AWR 通过 DBMS_WORKLOAD_REPOSITORY 实现，类似 Oracle。
// 关键视图: WRM$_SNAPSHOT (快照清单), WRH$_SYSSTAT (系统统计快照)
// 关键过程: SP_INIT_AWR_SYS(1) 启用, DBMS_WORKLOAD_REPOSITORY.CREATE_SNAPSHOT() 手动触发
//
// 这个 skill 不生成 AWR 报告 (报告生成需要写文件 + 工作集大), 只展示:
// 1. AWR 是否启用
// 2. 最近 10 个快照
// 3. 最近 1 小时关键统计变化
// 4. 命令提示用户如何手动 SP_AWR_REPORT_LAST_DAY()

const perfsnapEnabledSQL = `SELECT NAME, VALUE
FROM V$PARAMETER
WHERE NAME IN ('AWR_RPT_HOME','AWR_AUTO_FLUSH_FREQ','AWR_RTSP_CRT_INTERVAL')
ORDER BY NAME`

const perfsnapRecentSQL = `SELECT SNAP_ID, BEGIN_INTERVAL_TIME, END_INTERVAL_TIME
FROM SYS.WRM$_SNAPSHOT
ORDER BY SNAP_ID DESC
LIMIT 10`

const perfsnapRecentSQLFallback = `SELECT SNAP_ID, BEGIN_TIME, END_TIME
FROM WRM$_SNAPSHOT
ORDER BY SNAP_ID DESC
LIMIT 10`

type PerfSnapSkill struct{ driver db.Driver }

func NewPerfSnapSkill(driver db.Driver) *PerfSnapSkill { return &PerfSnapSkill{driver: driver} }

func (s *PerfSnapSkill) Name() string                       { return "perfsnap" }
func (s *PerfSnapSkill) Description() string                { return "DM AWR 快照状态 + 报告生成提示 (WRM$_SNAPSHOT / DBMS_WORKLOAD_REPOSITORY)" }
func (s *PerfSnapSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *PerfSnapSkill) Validate(_ skill.Params) error      { return nil }

func (s *PerfSnapSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "perfsnap", Description: "Show DM AWR snapshot status and report generation hints"}
}
func (s *PerfSnapSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "perfsnap", Aliases: []string{"awr"}, Usage: "/perfsnap"}
}

func (s *PerfSnapSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	cfg, cfgErr := s.driver.Query(ctx, perfsnapEnabledSQL)
	snaps, snapsErr := s.driver.Query(ctx, perfsnapRecentSQL)
	if snapsErr != nil {
		// fallback to alt schema
		snaps, snapsErr = s.driver.Query(ctx, perfsnapRecentSQLFallback)
	}

	var b strings.Builder
	entries := []dmutil.SummaryEntry{}
	awrStatus := "unknown"

	if cfgErr == nil && cfg != nil {
		b.WriteString("=== AWR 配置参数 ===\n")
		b.WriteString(format.FormatTable(cfg))
		hasAWRHome := false
		for _, row := range cfg.Rows {
			if len(row) >= 2 && fmt.Sprintf("%v", row[0]) == "AWR_RPT_HOME" && fmt.Sprintf("%v", row[1]) != "" {
				hasAWRHome = true
				entries = append(entries, dmutil.SummaryEntry{Key: "awr_rpt_home", Val: fmt.Sprintf("%v", row[1])})
			}
		}
		if hasAWRHome {
			awrStatus = "configured"
		} else {
			awrStatus = "not configured (AWR_RPT_HOME empty)"
		}
	} else {
		b.WriteString("=== AWR 配置 ===\n(V$PARAMETER 查询失败)\n")
	}

	if snapsErr == nil && snaps != nil {
		b.WriteString("\n=== 最近 10 个 AWR 快照 ===\n")
		b.WriteString(format.FormatTable(snaps))
		entries = append(entries, dmutil.SummaryEntry{Key: "snap_count_recent", Val: len(snaps.Rows)})
		if len(snaps.Rows) > 0 && len(snaps.Rows[0]) >= 3 {
			entries = append(entries,
				dmutil.SummaryEntry{Key: "latest_snap_id", Val: fmt.Sprintf("%v", snaps.Rows[0][0])},
				dmutil.SummaryEntry{Key: "latest_snap_end", Val: fmt.Sprintf("%v", snaps.Rows[0][2])},
			)
			awrStatus = "active"
		} else if awrStatus != "not configured (AWR_RPT_HOME empty)" {
			awrStatus = "configured but no snapshots yet"
		}
	} else {
		b.WriteString("\n=== AWR 快照 ===\n(WRM$_SNAPSHOT 不可访问 — AWR 可能未启用)\n")
		entries = append(entries, dmutil.SummaryEntry{Key: "snap_query_error", Val: snapsErr.Error()})
	}

	b.WriteString("\n=== 操作命令 ===\n")
	b.WriteString("  启用 AWR:        CALL SP_INIT_AWR_SYS(1);\n")
	b.WriteString("  设快照间隔:      CALL DBMS_WORKLOAD_REPOSITORY.MODIFY_SNAPSHOT_SETTINGS(60, 7);\n")
	b.WriteString("  手动触发快照:    CALL DBMS_WORKLOAD_REPOSITORY.CREATE_SNAPSHOT();\n")
	b.WriteString("  生成最近一天报告: CALL SP_AWR_REPORT_LAST_DAY();\n")
	b.WriteString("  生成区间报告:    CALL SP_AWR_REPORT(<start_snap>,<end_snap>,'/path/awr.html');\n")

	entries = append([]dmutil.SummaryEntry{
		{Key: "awr_status", Val: awrStatus},
	}, entries...)

	var data *db.QueryResult
	if snapsErr == nil && snaps != nil {
		data = snaps
	} else if cfgErr == nil && cfg != nil {
		data = cfg
	} else {
		data = &db.QueryResult{Columns: []string{"info"}, Rows: [][]any{}}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     data,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  fmt.Sprintf("AWR 状态: %s", awrStatus),
	}, nil
}
