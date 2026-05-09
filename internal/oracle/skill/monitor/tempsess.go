/*-------------------------------------------------------------------------
 *
 * tempsess.go
 *	  TempSessSkill shows sessions consuming temporary tablespace.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/tempsess.go
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

const tempsessSQL = `SELECT s.sid,
       NVL(s.username, s.program) AS username,
       ROUND(su.blocks * (SELECT value FROM v$parameter WHERE name='db_block_size') / 1048576) AS temp_mb,
       s.sql_id, s.event, s.status
FROM v$sort_usage su
JOIN v$session s ON su.session_addr = s.saddr
ORDER BY su.blocks DESC`

// TempSessSkill shows sessions consuming temporary tablespace.
type TempSessSkill struct {
	driver db.Driver
}

// NewTempSessSkill creates a TempSessSkill backed by the given driver.
func NewTempSessSkill(driver db.Driver) *TempSessSkill {
	return &TempSessSkill{driver: driver}
}

func (s *TempSessSkill) Name() string        { return "tempsess" }
func (s *TempSessSkill) Description() string  { return "Show sessions consuming temporary tablespace" }
func (s *TempSessSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TempSessSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "tempsess",
		Description: "Show sessions consuming temporary tablespace",
	}
}

func (s *TempSessSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "tempsess",
		Usage:   "/tempsess",
	}
}

func (s *TempSessSkill) Validate(_ skill.Params) error { return nil }

func (s *TempSessSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	qr, err := s.driver.Query(ctx, tempsessSQL)
	if err != nil {
		return nil, fmt.Errorf("querying temp session usage: %w", err)
	}

	if len(qr.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无会话占用临时空间.",
			Summary:  "无临时空间占用",
		}, nil
	}

	totalMB := 0
	for _, row := range qr.Rows {
		totalMB += rowInt(row, 2)
	}

	summary := fmt.Sprintf("%d 个会话, 合计 %s MB | 提示: /kill %s 终止占用最大的会话",
		len(qr.Rows), formatNumber(totalMB), rowStr(qr.Rows[0], 0))

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     qr,
		Rendered: fmt.Sprintf("临时空间占用会话 — %d 个", len(qr.Rows)),
		Summary:  summary,
	}, nil
}

// formatNumber adds comma separators to an integer.
func formatNumber(n int) string {
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
