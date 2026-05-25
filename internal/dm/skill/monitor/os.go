/*-------------------------------------------------------------------------
 *
 * os.go
 *	  os — OSSkill plus helpers (NewOSSkill) used by the monitor
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/os.go
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

// DM 没有 V$OSSTAT，主机指标分散在 V$INSTANCE / V$THREADS / V$MEM_POOL / V$IOSTAT (如有) 中。
// 这里聚合一份"实例侧主机视角"的快照。

// V$INSTANCE 实测列 (DM 8.1.4.200):
// NAME, INSTANCE_NAME, INSTANCE_NUMBER, HOST_NAME, SVR_VERSION, DB_VERSION,
// START_TIME, STATUS$, MODE$, OGUID, DSC_SEQNO, DSC_ROLE, BUILD_VERSION, BUILD_TIME
// 注意: 列叫 SVR_VERSION 不是 VERSION; STATUS 必须用 STATUS$ 加引号或别名
const osInstanceSQL = `SELECT NAME, INSTANCE_NAME, HOST_NAME,
       SVR_VERSION AS VERSION, START_TIME, STATUS$ AS STATUS, MODE$ AS MODE
FROM V$INSTANCE`

// V$THREADS 实测列: ID, NAME, STARTUP_TIME, ...
const osThreadsSQL = `SELECT NAME AS THREAD_TYPE, COUNT(*) AS COUNT
FROM V$THREADS
GROUP BY NAME
ORDER BY COUNT DESC`

// V$PROCESS 实测列: PROC_ID, COMM (操作命令), ...
// DM 把 process 等同于 client connection
const osProcSQL = `SELECT COUNT(*) AS PROCESS_COUNT
FROM V$PROCESS`

// V$MEM_POOL 总内存
const osMemSQL = `SELECT COUNT(*) AS POOL_COUNT,
       ROUND(SUM(TOTAL_SIZE)/1024/1024) AS TOTAL_MB
FROM V$MEM_POOL`

type OSSkill struct{ driver db.Driver }

func NewOSSkill(driver db.Driver) *OSSkill { return &OSSkill{driver: driver} }

func (s *OSSkill) Name() string                       { return "os" }
func (s *OSSkill) Description() string                { return "实例主机视角 (V$INSTANCE / V$THREADS / V$PROCESS / V$MEM_POOL)" }
func (s *OSSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *OSSkill) Validate(_ skill.Params) error      { return nil }

func (s *OSSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "os", Description: "Show DM instance OS-level overview"}
}
func (s *OSSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "os", Aliases: []string{"osstat"}, Usage: "/os"}
}

func (s *OSSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	inst, err := s.driver.Query(ctx, osInstanceSQL)
	if err != nil {
		return nil, fmt.Errorf("dm os instance: %w", err)
	}
	threads, threadsErr := s.driver.Query(ctx, osThreadsSQL)
	procs, procsErr := s.driver.Query(ctx, osProcSQL)
	memPool, memErr := s.driver.Query(ctx, osMemSQL)

	var b strings.Builder
	b.WriteString("=== 实例信息 ===\n")
	b.WriteString(format.FormatTable(inst))
	if threadsErr == nil && threads != nil {
		b.WriteString("\n=== DM 线程统计 ===\n")
		b.WriteString(format.FormatTable(threads))
	}
	if procsErr == nil && procs != nil {
		b.WriteString("\n=== 进程数 ===\n")
		b.WriteString(format.FormatTable(procs))
	}
	if memErr == nil && memPool != nil {
		b.WriteString("\n=== 内存池总计 ===\n")
		b.WriteString(format.FormatTable(memPool))
	}

	entries := []dmutil.SummaryEntry{}
	if len(inst.Rows) > 0 && len(inst.Rows[0]) >= 6 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "instance_name", Val: fmt.Sprintf("%v", inst.Rows[0][1])},
			dmutil.SummaryEntry{Key: "host_name", Val: fmt.Sprintf("%v", inst.Rows[0][2])},
			dmutil.SummaryEntry{Key: "version", Val: fmt.Sprintf("%v", inst.Rows[0][3])},
			dmutil.SummaryEntry{Key: "start_time", Val: fmt.Sprintf("%v", inst.Rows[0][4])},
			dmutil.SummaryEntry{Key: "status", Val: fmt.Sprintf("%v", inst.Rows[0][5])},
		)
	}
	if threadsErr == nil && threads != nil {
		entries = append(entries, dmutil.SummaryEntry{Key: "thread_kinds", Val: len(threads.Rows)})
	}
	if procsErr == nil && len(procs.Rows) > 0 {
		entries = append(entries, dmutil.SummaryEntry{Key: "process_count", Val: fmt.Sprintf("%v", procs.Rows[0][0])})
	}
	if memErr == nil && len(memPool.Rows) > 0 && len(memPool.Rows[0]) >= 2 {
		entries = append(entries, dmutil.SummaryEntry{Key: "memory_pool_total_mb", Val: fmt.Sprintf("%v", memPool.Rows[0][1])})
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     inst,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  "实例主机视角",
	}, nil
}
