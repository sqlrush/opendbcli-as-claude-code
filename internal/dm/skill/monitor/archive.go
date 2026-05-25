/*-------------------------------------------------------------------------
 *
 * archive.go
 *	  archive — ArchiveSkill plus helpers (NewArchiveSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/archive.go
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

// V$DM_ARCH_INI 实测列: ARCH_NAME, ARCH_TYPE, ARCH_DEST, ARCH_FILE_SIZE, ARCH_SPACE_LIMIT, ...
// V$ARCH_STATUS 实测列: ARCH_TYPE, ARCH_DEST, ARCH_STATUS, ...
// V$ARCHIVED_LOG 实测列: ARCH_NAME, FIRST_TIME, NEXT_CHANGE#, ...
const archiveConfigSQL = `SELECT ARCH_NAME, ARCH_TYPE, ARCH_DEST,
       ARCH_FILE_SIZE, ARCH_SPACE_LIMIT
FROM V$DM_ARCH_INI`

const archiveStatusSQL = `SELECT ARCH_TYPE, ARCH_DEST, ARCH_STATUS
FROM V$ARCH_STATUS`

const archiveRecentSQL = `SELECT ARCH_NAME, FIRST_TIME, NEXT_CHANGE#
FROM V$ARCHIVED_LOG
ORDER BY FIRST_TIME DESC
LIMIT 10`

type ArchiveSkill struct{ driver db.Driver }

func NewArchiveSkill(driver db.Driver) *ArchiveSkill { return &ArchiveSkill{driver: driver} }

func (s *ArchiveSkill) Name() string                       { return "archive" }
func (s *ArchiveSkill) Description() string                { return "归档配置 + 状态 + 最近 10 条 (V$DM_ARCH_INI / V$ARCH_STATUS / V$ARCHIVED_LOG)" }
func (s *ArchiveSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ArchiveSkill) Validate(_ skill.Params) error      { return nil }

func (s *ArchiveSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "archive", Description: "Show DM archive log configuration and status"}
}
func (s *ArchiveSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "archive", Usage: "/archive"}
}

func (s *ArchiveSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	cfg, cfgErr := s.driver.Query(ctx, archiveConfigSQL)
	st, stErr := s.driver.Query(ctx, archiveStatusSQL)
	recent, recErr := s.driver.Query(ctx, archiveRecentSQL)

	var b strings.Builder
	entries := []dmutil.SummaryEntry{}

	if cfgErr == nil && cfg != nil {
		b.WriteString("=== 归档配置 ===\n")
		b.WriteString(format.FormatTable(cfg))
		entries = append(entries, dmutil.SummaryEntry{Key: "arch_dest_count", Val: len(cfg.Rows)})
		if len(cfg.Rows) == 0 {
			entries = append(entries, dmutil.SummaryEntry{Key: "archive_mode", Val: "OFF (no destination configured)"})
		} else {
			entries = append(entries, dmutil.SummaryEntry{Key: "archive_mode", Val: "ON"})
		}
	} else {
		b.WriteString("=== 归档配置 ===\n(V$DM_ARCH_INI 不可用)\n")
	}

	if stErr == nil && st != nil {
		b.WriteString("\n=== 归档状态 ===\n")
		b.WriteString(format.FormatTable(st))
		statusCount := dmutil.CountByCol(st.Rows, 2)
		for status, n := range statusCount {
			entries = append(entries, dmutil.SummaryEntry{Key: "status_" + status, Val: n})
		}
	}

	if recErr == nil && recent != nil {
		b.WriteString("\n=== 最近 10 条归档 ===\n")
		b.WriteString(format.FormatTable(recent))
		entries = append(entries, dmutil.SummaryEntry{Key: "recent_archives", Val: len(recent.Rows)})
		if len(recent.Rows) > 0 && len(recent.Rows[0]) >= 2 {
			entries = append(entries, dmutil.SummaryEntry{
				Key: "latest_archive_time",
				Val: fmt.Sprintf("%v", recent.Rows[0][1]),
			})
		}
	}

	if cfgErr != nil && stErr != nil && recErr != nil {
		return nil, fmt.Errorf("dm archive: cfg=%v status=%v recent=%v", cfgErr, stErr, recErr)
	}

	var firstQR *db.QueryResult
	if cfg != nil {
		firstQR = cfg
	} else if st != nil {
		firstQR = st
	} else {
		firstQR = recent
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     firstQR,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  "归档配置 + 状态",
	}, nil
}
