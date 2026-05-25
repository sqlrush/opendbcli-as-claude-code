/*-------------------------------------------------------------------------
 *
 * alert.go
 *	  alert — AlertSkill plus helpers (NewAlertSkill) used by the
 *	  monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/alert.go
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

type AlertSkill struct{ driver db.Driver }

func NewAlertSkill(driver db.Driver) *AlertSkill { return &AlertSkill{driver: driver} }

func (s *AlertSkill) Name() string                       { return "alert" }
func (s *AlertSkill) Description() string                { return "告警事件 (死锁/危险事件/运行时错误)" }
func (s *AlertSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *AlertSkill) Validate(_ skill.Params) error      { return nil }
func (s *AlertSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "alert", Description: "DM alerts: deadlocks + danger events + runtime errors"}
}
func (s *AlertSkill) CLIDef() skill.CLIDef { return skill.CLIDef{Command: "alert", Usage: "/alert"} }

func (s *AlertSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	var b strings.Builder
	counts := map[string]any{}

	count := func(label, key, sqlStr string) {
		r, err := s.driver.Query(ctx, sqlStr)
		if err != nil {
			b.WriteString(fmt.Sprintf("%s: ERR %v\n", label, err))
			counts[key] = "ERR"
			return
		}
		var v any
		if len(r.Rows) > 0 && len(r.Rows[0]) > 0 {
			v = r.Rows[0][0]
		} else {
			v = 0
		}
		b.WriteString(fmt.Sprintf("%-20s : %v\n", label, v))
		counts[key] = v
	}

	b.WriteString("=== DM 告警计数 (累计自上次 reset) ===\n")
	count("累计死锁", "deadlock_total", "SELECT COUNT(*) FROM V$DEADLOCK_HISTORY")
	count("累计危险事件", "danger_event_total", "SELECT COUNT(*) FROM V$DANGER_EVENT")
	count("累计运行时错误", "runtime_err_total", "SELECT COUNT(*) FROM V$RUNTIME_ERR_HISTORY")
	count("当前阻塞会话", "blocked_session_count", "SELECT COUNT(DISTINCT TRX_ID) FROM V$LOCK WHERE BLOCKED = 1")
	count("当前长 SQL", "long_sql_count", "SELECT COUNT(*) FROM V$LONG_EXEC_SQLS")

	// V$DANGER_EVENT 实测列: OPTIME (不是 HAPPEN_TIME) / OPERATION / OPUSER
	r, err := s.driver.Query(ctx, "SELECT * FROM V$DANGER_EVENT ORDER BY OPTIME DESC LIMIT 5")
	if err == nil && len(r.Rows) > 0 {
		b.WriteString(fmt.Sprintf("\n--- V$DANGER_EVENT 最近 %d 条 ---\n", len(r.Rows)))
		for i, row := range r.Rows {
			b.WriteString(fmt.Sprintf("  [%d] %v\n", i+1, row))
		}
	}

	// [summary]
	b.WriteString("\n[summary]\n")
	for k, v := range counts {
		b.WriteString(fmt.Sprintf("%s: %v\n", k, v))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  "DM alerts (deadlocks + danger + errors counters)",
	}, nil
}
