/*-------------------------------------------------------------------------
 *
 * resource.go
 *	  resource — ResourceSkill plus helpers (NewResourceSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/resource.go
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

// 注意: DM 没有 V$RESOURCE_LIMIT (Oracle 视图)。
// DM 资源限制信息分散:
// - V$PARAMETER 看上限参数 (MAX_SESSIONS / MEMORY_TARGET / BUFFER 等)
// - V$SESSIONS COUNT 看实时使用
// - V$MEM_POOL SUM 看实际占用

// V$PARAMETER 实测列: NAME, TYPE, VALUE, SYS_VALUE, FILE_VALUE, DESCRIPTION ...
const resourceParamSQL = `SELECT NAME, VALUE
FROM V$PARAMETER
WHERE NAME IN (
    'MAX_SESSIONS','MAX_OS_MEMORY','MEMORY_TARGET','MEMORY_POOL',
    'BUFFER','MAX_SESSION_STATEMENT','MAX_CONCURRENT_TRX',
    'WORKER_THREADS','TASK_THREADS','IO_THR_GROUPS'
)
ORDER BY NAME`

const resourceUsageSQL = `SELECT
       (SELECT COUNT(*) FROM V$SESSIONS) AS SESSIONS_USED,
       (SELECT COUNT(*) FROM V$SESSIONS WHERE STATE = 'ACTIVE') AS SESSIONS_ACTIVE,
       (SELECT COUNT(*) FROM V$TRX) AS TRX_USED,
       (SELECT ROUND(SUM(TOTAL_SIZE)/1024/1024) FROM V$MEM_POOL) AS MEMORY_USED_MB
FROM DUAL`

type ResourceSkill struct{ driver db.Driver }

func NewResourceSkill(driver db.Driver) *ResourceSkill { return &ResourceSkill{driver: driver} }

func (s *ResourceSkill) Name() string                       { return "resource" }
func (s *ResourceSkill) Description() string                { return "资源限制 + 使用率 (V$PARAMETER + V$SESSIONS + V$TRX + V$MEM_POOL)" }
func (s *ResourceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ResourceSkill) Validate(_ skill.Params) error      { return nil }

func (s *ResourceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "resource", Description: "Show DM resource limits and usage"}
}
func (s *ResourceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "resource", Usage: "/resource"}
}

func (s *ResourceSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	params, err := s.driver.Query(ctx, resourceParamSQL)
	if err != nil {
		return nil, fmt.Errorf("dm resource params: %w", err)
	}
	usage, usageErr := s.driver.Query(ctx, resourceUsageSQL)

	var b strings.Builder
	b.WriteString("=== 资源参数上限 (V$PARAMETER) ===\n")
	b.WriteString(format.FormatTable(params))
	if usageErr == nil && usage != nil {
		b.WriteString("\n=== 当前使用率 ===\n")
		b.WriteString(format.FormatTable(usage))
	}

	entries := []dmutil.SummaryEntry{
		{Key: "param_count", Val: len(params.Rows)},
	}
	// 把每个参数列入 summary
	paramMap := make(map[string]string)
	for _, row := range params.Rows {
		if len(row) >= 2 {
			paramMap[fmt.Sprintf("%v", row[0])] = fmt.Sprintf("%v", row[1])
		}
	}
	for k, v := range paramMap {
		entries = append(entries, dmutil.SummaryEntry{Key: "limit_" + k, Val: v})
	}
	if usageErr == nil && len(usage.Rows) > 0 && len(usage.Rows[0]) >= 4 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "sessions_used", Val: fmt.Sprintf("%v", usage.Rows[0][0])},
			dmutil.SummaryEntry{Key: "sessions_active", Val: fmt.Sprintf("%v", usage.Rows[0][1])},
			dmutil.SummaryEntry{Key: "trx_used", Val: fmt.Sprintf("%v", usage.Rows[0][2])},
			dmutil.SummaryEntry{Key: "memory_used_mb", Val: fmt.Sprintf("%v", usage.Rows[0][3])},
		)
		// 计算 sessions 使用率
		if maxSess, ok := paramMap["MAX_SESSIONS"]; ok {
			entries = append(entries, dmutil.SummaryEntry{
				Key: "sessions_limit",
				Val: maxSess,
			})
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     params,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  fmt.Sprintf("资源参数 — %d 个", len(params.Rows)),
	}, nil
}
