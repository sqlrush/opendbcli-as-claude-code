/*-------------------------------------------------------------------------
 *
 * sessions.go
 *	  sessions — SessionsSkill plus helpers (NewSessionsSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/sessions.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// Scope aligned with /activesessions: include parallel workers (which the
// planner spawns as separate backends) and exclude our own session, so the
// totals from /sessions and /activesessions reconcile.
const sessionsWhere = `WHERE backend_type IN ('client backend', 'parallel worker')
  AND pid != pg_backend_pid()`

const sessionsSQL = `SELECT
  pid, usename, client_addr::text, datname,
  state, wait_event_type, wait_event,
  LEFT(query, 80) AS query,
  EXTRACT(EPOCH FROM clock_timestamp() - query_start)::int AS query_sec
FROM pg_stat_activity
` + sessionsWhere + `
ORDER BY query_start NULLS LAST`

// P01: Connection frequency metrics for short-connection storm detection.
const sessionsFreqSQL = `SELECT
  count(*) FILTER (WHERE backend_start > clock_timestamp() - interval '1 min') AS new_conns_1m,
  count(*) FILTER (WHERE state = 'idle') AS idle_count,
  count(*) FILTER (WHERE state = 'active') AS active_count,
  count(*) AS total_count,
  ROUND(AVG(EXTRACT(EPOCH FROM clock_timestamp() - backend_start))::numeric, 0) AS avg_conn_age_sec
FROM pg_stat_activity
` + sessionsWhere

type SessionsSkill struct{ driver db.Driver }

func NewSessionsSkill(driver db.Driver) *SessionsSkill { return &SessionsSkill{driver: driver} }

func (s *SessionsSkill) Name() string                          { return "sessions" }
func (s *SessionsSkill) Description() string                    { return "所有连接概览" }
func (s *SessionsSkill) SecurityLevel() skill.SecurityLevel     { return skill.LevelReadOnly }
func (s *SessionsSkill) Validate(_ skill.Params) error          { return nil }
func (s *SessionsSkill) CLIDef() skill.CLIDef                   { return skill.CLIDef{Usage: "/sessions"} }
func (s *SessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "sessions", Description: "List all PostgreSQL connections with state, wait event, and current query"}
}

func (s *SessionsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, sessionsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	// P01: Append connection frequency summary for short-connection storm detection.
	freqSuffix := ""
	if freq, err2 := s.driver.Query(ctx, sessionsFreqSQL); err2 == nil && len(freq.Rows) > 0 {
		row := freq.Rows[0]
		newConns := fmt.Sprintf("%v", row[0])
		idle := fmt.Sprintf("%v", row[1])
		active := fmt.Sprintf("%v", row[2])
		avgAge := fmt.Sprintf("%v", row[4])
		freqSuffix = fmt.Sprintf("\n连接频率: 最近1分钟新建=%s, 活跃=%s, 空闲=%s, 平均连接存活=%ss", newConns, active, idle, avgAge)
	}

	summary := fmt.Sprintf("全部会话概览 — %d 个%s", len(result.Rows), freqSuffix)
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: summary,
		Summary:  fmt.Sprintf("全部会话 — %d 个%s", len(result.Rows), freqSuffix),
	}, nil
}
