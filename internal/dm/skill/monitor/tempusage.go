/*-------------------------------------------------------------------------
 *
 * tempusage.go
 *	  tempusage — TempUsageSkill plus helpers (NewTempUsageSkill) used
 *	  by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/tempusage.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// 临时表空间使用情况：DM 用 SF_GET_TEMP_TS_INFO()/V$TEMPORARY_TABLESPACE
// 这里用 DBA_DATA_FILES 兼容视图查 TEMP 表空间，然后用 SF_GET_TEMP_TS_INFO 看实时使用率
const tempFilesSQL = `SELECT TABLESPACE_NAME, FILE_NAME,
       ROUND(BYTES/1048576) AS SIZE_MB,
       AUTOEXTENSIBLE AS AUTOEXT,
       ROUND(MAXBYTES/1048576) AS MAX_MB
FROM DBA_DATA_FILES
WHERE UPPER(TABLESPACE_NAME) IN ('TEMP','TEMP_TBS','HMAIN')
ORDER BY TABLESPACE_NAME`

const tempUsageSQL = `SELECT TABLESPACE_NAME,
       ROUND(SF_GET_TS_USED_SPACE(TABLESPACE_NAME)/1024/128, 2) AS USED_MB
FROM DBA_TABLESPACES
WHERE UPPER(TABLESPACE_NAME) IN ('TEMP','HMAIN')`

type TempUsageSkill struct{ driver db.Driver }

func NewTempUsageSkill(driver db.Driver) *TempUsageSkill { return &TempUsageSkill{driver: driver} }

func (s *TempUsageSkill) Name() string                       { return "tempusage" }
func (s *TempUsageSkill) Description() string                { return "临时表空间使用 (DBA_DATA_FILES + SF_GET_TS_USED_SPACE)" }
func (s *TempUsageSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *TempUsageSkill) Validate(_ skill.Params) error      { return nil }

func (s *TempUsageSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "tempusage", Description: "Show DM TEMP tablespace usage"}
}
func (s *TempUsageSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "tempusage", Usage: "/tempusage"}
}

func (s *TempUsageSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	files, err := s.driver.Query(ctx, tempFilesSQL)
	if err != nil {
		return nil, fmt.Errorf("dm tempusage files: %w", err)
	}

	usage, usageErr := s.driver.Query(ctx, tempUsageSQL)
	// usageErr 可能因为 SF_GET_TS_USED_SPACE 不可用而失败，不致命
	var usageTable string
	if usageErr == nil && usage != nil {
		usageTable = format.FormatTable(usage)
	} else {
		usageTable = "(SF_GET_TS_USED_SPACE 不可用，仅显示文件信息)\n"
	}

	out := "=== TEMP 表空间文件 ===\n" + format.FormatTable(files) +
		"\n=== TEMP 表空间使用率 ===\n" + usageTable

	entries := []dmutil.SummaryEntry{
		{Key: "temp_file_count", Val: len(files.Rows)},
	}
	if usageErr == nil && len(usage.Rows) > 0 && len(usage.Rows[0]) >= 2 {
		for _, row := range usage.Rows {
			ts := fmt.Sprintf("%v", row[0])
			used := fmt.Sprintf("%v", row[1])
			entries = append(entries, dmutil.SummaryEntry{Key: "used_mb_" + ts, Val: used})
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     files,
		Rendered: dmutil.FormatTableWithSummary(out, entries),
		Summary:  fmt.Sprintf("TEMP %d 个文件", len(files.Rows)),
	}, nil
}
