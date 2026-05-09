/*-------------------------------------------------------------------------
 *
 * undosess.go
 *	  UndoSessSkill shows sessions consuming undo tablespace.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/undosess.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const undosessSQL = `SELECT s.sid, NVL(s.username, s.program) AS username,
       ROUND(t.used_ublk * (SELECT value FROM v$parameter WHERE name='db_block_size') / 1048576) AS undo_mb,
       s.sql_id, TO_CHAR(t.start_date, 'HH24:MI:SS') AS start_time, s.status
FROM v$transaction t
JOIN v$session s ON t.ses_addr = s.saddr
ORDER BY t.used_ublk DESC`

// UndoSessSkill shows sessions consuming undo tablespace.
type UndoSessSkill struct {
	driver db.Driver
}

// NewUndoSessSkill creates an UndoSessSkill backed by the given driver.
func NewUndoSessSkill(driver db.Driver) *UndoSessSkill {
	return &UndoSessSkill{driver: driver}
}

func (s *UndoSessSkill) Name() string        { return "undosess" }
func (s *UndoSessSkill) Description() string  { return "Show sessions consuming undo tablespace" }
func (s *UndoSessSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *UndoSessSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "undosess",
		Description: "Show sessions consuming undo tablespace",
	}
}

func (s *UndoSessSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "undosess",
		Usage:   "/undosess",
	}
}

func (s *UndoSessSkill) Validate(_ skill.Params) error { return nil }

func (s *UndoSessSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	qr, err := s.driver.Query(ctx, undosessSQL)
	if err != nil {
		return nil, fmt.Errorf("querying undo session usage: %w", err)
	}

	if len(qr.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无活跃Undo事务.",
			Summary:  "无活跃Undo事务",
		}, nil
	}

	totalMB := 0
	for _, row := range qr.Rows {
		totalMB += rowInt(row, 2)
	}

	hint := fmt.Sprintf("合计 %s MB | 提示: /kill %s 终止占用最大的会话",
		undoFormatNumber(totalMB), rowStr(qr.Rows[0], 0))

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     qr,
		Rendered: fmt.Sprintf("Undo 占用事务 — %d 个", len(qr.Rows)),
		Summary:  fmt.Sprintf("%d 个会话, %s MB\n%s", len(qr.Rows), undoFormatNumber(totalMB), hint),
	}, nil
}

// undoFormatNumber adds comma separators to an integer.
func undoFormatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for i := len(s); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		parts = append([]string{s[start:i]}, parts...)
	}
	return strings.Join(parts, ",")
}
