/*-------------------------------------------------------------------------
 *
 * dbtop.go
 *	  DbtopSkill — DM 实时 top dashboard.
 *	  简化版：单文件实现 V$SESSIONS + V$LONG_EXEC_SQLS
 *	  实时刷新。 不做 Oracle dbtop 那种 delta/历史曲线。
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/dbtop.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// DbtopSkill — DM 实时 top dashboard.
// 简化版：单文件实现 V$SESSIONS + V$LONG_EXEC_SQLS 实时刷新。
// 不做 Oracle dbtop 那种 delta/历史曲线。
type DbtopSkill struct {
	driver db.Driver
}

func NewDbtopSkill(driver db.Driver) *DbtopSkill { return &DbtopSkill{driver: driver} }

func (s *DbtopSkill) Name() string                       { return "dbtop" }
func (s *DbtopSkill) Description() string                { return "DM 实时 top dashboard (V$SESSIONS / V$LONG_EXEC_SQLS)" }
func (s *DbtopSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *DbtopSkill) Validate(_ skill.Params) error      { return nil }

func (s *DbtopSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "dbtop", Description: "Real-time DM top dashboard"}
}
func (s *DbtopSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "dbtop", Usage: "/dbtop [interval_seconds]"}
}

func (s *DbtopSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	intervalSec := 2
	if args := strings.TrimSpace(params.StringOr("args", "")); args != "" {
		if n, err := strconv.Atoi(args); err == nil && n > 0 {
			intervalSec = n
		}
	}
	return &skill.Result{
		Type: skill.ResultRefresh,
		Data: &dmDbtopRefresh{driver: s.driver, intervalSec: intervalSec},
	}, nil
}

type dmDbtopRefresh struct {
	driver      db.Driver
	intervalSec int
}

func (c *dmDbtopRefresh) DbtopInterval() int { return c.intervalSec }
func (c *dmDbtopRefresh) NewDbtopLoop() skill.DbtopLoop {
	return &dmDbtopLoop{driver: c.driver}
}

type dmDbtopLoop struct {
	driver db.Driver
}

const dbtopInstanceSQL = `SELECT NAME, INSTANCE_NAME, HOST_NAME, STATUS$, START_TIME
FROM V$INSTANCE`

const dbtopSessionsSQL = `SELECT
       (SELECT COUNT(*) FROM V$SESSIONS) AS TOTAL,
       (SELECT COUNT(*) FROM V$SESSIONS WHERE STATE = 'ACTIVE') AS ACTIVE,
       (SELECT COUNT(*) FROM V$SESSIONS WHERE STATE = 'IDLE') AS IDLE,
       (SELECT COUNT(*) FROM V$LOCK WHERE BLOCKED = 1) AS BLOCKED
FROM DUAL`

const dbtopActiveSQL = `SELECT SESS_ID, USER_NAME,
       SUBSTR(SQL_TEXT, 1, 60) AS SQL_HEAD,
       CAST((SYSDATE - LAST_SEND_TIME) * 86400 AS INT) AS ELAPSED_SEC
FROM V$SESSIONS
WHERE STATE = 'ACTIVE' AND SQL_TEXT IS NOT NULL
ORDER BY LAST_SEND_TIME ASC
LIMIT 10`

const dbtopLongSQL = `SELECT SESS_ID, EXEC_TIME, SUBSTR(SQL_TEXT, 1, 60) AS SQL_HEAD
FROM V$LONG_EXEC_SQLS
ORDER BY EXEC_TIME DESC
LIMIT 5`

func (l *dmDbtopLoop) RenderFrame(ctx context.Context, cols, intervalSec int) []string {
	var lines []string

	// Header
	now := time.Now().Format("2006-01-02 15:04:05")
	header := fmt.Sprintf("DM dbtop  %s  refresh=%ds", now, intervalSec)
	lines = append(lines, header, strings.Repeat("─", min(cols, 80)))

	// Instance info
	if r, err := l.driver.Query(ctx, dbtopInstanceSQL); err == nil && r != nil && len(r.Rows) > 0 {
		row := r.Rows[0]
		if len(row) >= 5 {
			lines = append(lines, fmt.Sprintf("Instance: %v (%v)  Host: %v  Status: %v  Started: %v",
				row[0], row[1], row[2], row[3], row[4]))
		}
	} else if err != nil {
		lines = append(lines, fmt.Sprintf("(instance query error: %v)", err))
	}

	// Session counters
	if r, err := l.driver.Query(ctx, dbtopSessionsSQL); err == nil && r != nil && len(r.Rows) > 0 {
		row := r.Rows[0]
		if len(row) >= 4 {
			lines = append(lines, fmt.Sprintf("Sessions  total=%v  active=%v  idle=%v  blocked=%v",
				row[0], row[1], row[2], row[3]))
		}
	} else if err != nil {
		lines = append(lines, fmt.Sprintf("(session counter error: %v)", err))
	}

	lines = append(lines, "")

	// Active sessions table
	lines = append(lines, "── Active Sessions (Top 10) ──────────────")
	if r, err := l.driver.Query(ctx, dbtopActiveSQL); err == nil && r != nil {
		if len(r.Rows) == 0 {
			lines = append(lines, "(no active session)")
		} else {
			lines = append(lines, fmt.Sprintf("%-18s %-12s %-60s %s", "SESS_ID", "USER", "SQL_HEAD", "ELAPSED_S"))
			for _, row := range r.Rows {
				if len(row) < 4 {
					continue
				}
				lines = append(lines, fmt.Sprintf("%-18v %-12v %-60v %v",
					row[0], truncate(fmt.Sprintf("%v", row[1]), 12),
					truncate(fmt.Sprintf("%v", row[2]), 60), row[3]))
			}
		}
	} else if err != nil {
		lines = append(lines, fmt.Sprintf("(active query error: %v)", err))
	}

	lines = append(lines, "")

	// Long-running SQL
	lines = append(lines, "── Long-running SQLs (Top 5) ─────────────")
	if r, err := l.driver.Query(ctx, dbtopLongSQL); err == nil && r != nil {
		if len(r.Rows) == 0 {
			lines = append(lines, "(no long-running SQL)")
		} else {
			lines = append(lines, fmt.Sprintf("%-18s %-12s %s", "SESS_ID", "EXEC_TIME", "SQL_HEAD"))
			for _, row := range r.Rows {
				if len(row) < 3 {
					continue
				}
				lines = append(lines, fmt.Sprintf("%-18v %-12v %v",
					row[0], row[1], truncate(fmt.Sprintf("%v", row[2]), 60)))
			}
		}
	} else if err != nil {
		lines = append(lines, fmt.Sprintf("(long SQL error: %v)", err))
	}

	return lines
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
