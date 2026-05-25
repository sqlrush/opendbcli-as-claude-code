/*-------------------------------------------------------------------------
 *
 * redo.go
 *	  redo — RedoSkill plus helpers (NewRedoSkill) used by the monitor
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/redo.go
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

// V$RLOGFILE 实测列: GROUP_ID, FILE_ID, PATH, CLIENT_PATH, RLOG_SIZE, ...
// V$RLOG 实测列: CUR_FILE, FILE_LSN, CKPT_LSN, FREE_SPACE, ...
const redoFilesSQL = `SELECT GROUP_ID, FILE_ID, PATH, ROUND(RLOG_SIZE/1048576) AS SIZE_MB
FROM V$RLOGFILE
ORDER BY GROUP_ID, FILE_ID`

const redoStatusSQL = `SELECT CUR_FILE, FILE_LSN, CKPT_LSN, FREE_SPACE
FROM V$RLOG`

type RedoSkill struct{ driver db.Driver }

func NewRedoSkill(driver db.Driver) *RedoSkill { return &RedoSkill{driver: driver} }

func (s *RedoSkill) Name() string                       { return "redo" }
func (s *RedoSkill) Description() string                { return "redo/rlog 文件 + LSN 状态 (V$RLOGFILE + V$RLOG)" }
func (s *RedoSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *RedoSkill) Validate(_ skill.Params) error      { return nil }

func (s *RedoSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "redo", Description: "Show DM rlog file usage and LSN state"}
}
func (s *RedoSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "redo", Usage: "/redo"}
}

func (s *RedoSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	files, err := s.driver.Query(ctx, redoFilesSQL)
	if err != nil {
		return nil, fmt.Errorf("dm redo files: %w", err)
	}
	status, err := s.driver.Query(ctx, redoStatusSQL)
	if err != nil {
		return nil, fmt.Errorf("dm redo status: %w", err)
	}

	var b strings.Builder
	b.WriteString("=== Rlog 文件 ===\n")
	b.WriteString(format.FormatTable(files))
	b.WriteString("\n=== Rlog 状态 ===\n")
	b.WriteString(format.FormatTable(status))

	entries := []dmutil.SummaryEntry{
		{Key: "rlog_file_count", Val: len(files.Rows)},
	}
	if len(status.Rows) > 0 && len(status.Rows[0]) >= 4 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "current_file", Val: fmt.Sprintf("%v", status.Rows[0][0])},
			dmutil.SummaryEntry{Key: "file_lsn", Val: fmt.Sprintf("%v", status.Rows[0][1])},
			dmutil.SummaryEntry{Key: "ckpt_lsn", Val: fmt.Sprintf("%v", status.Rows[0][2])},
			dmutil.SummaryEntry{Key: "free_space", Val: fmt.Sprintf("%v", status.Rows[0][3])},
		)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     files,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  fmt.Sprintf("Rlog %d 个文件", len(files.Rows)),
	}, nil
}
