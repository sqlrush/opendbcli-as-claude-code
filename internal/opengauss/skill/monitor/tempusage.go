/*-------------------------------------------------------------------------
 *
 * tempusage.go
 *	  TempUsageSkill shows temp file usage per database and per active
 *	  session.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/tempusage.go
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

// tempByDBSQL pulls cumulative temp file stats per database.
const tempByDBSQL = `SELECT
  datname,
  temp_files,
  pg_size_pretty(temp_bytes) AS temp_bytes_pretty,
  temp_bytes,
  stats_reset::text
FROM pg_stat_database
WHERE datname IS NOT NULL
ORDER BY temp_bytes DESC
LIMIT 10`

// tempBySessionSQL lists live sessions currently spilling to temp files.
const tempBySessionSQL = `SELECT
  pid,
  usename,
  datname,
  state,
  LEFT(query, 80) AS query,
  EXTRACT(EPOCH FROM now() - query_start)::int AS elapsed_sec
FROM pg_stat_activity
WHERE temp_files > 0
ORDER BY temp_files DESC
LIMIT 10`

// TempUsageSkill shows temp file usage per database and per active session.
type TempUsageSkill struct{ driver db.Driver }

// NewTempUsageSkill creates a TempUsageSkill.
func NewTempUsageSkill(driver db.Driver) *TempUsageSkill { return &TempUsageSkill{driver: driver} }

func (s *TempUsageSkill) Name() string                       { return "tempusage" }
func (s *TempUsageSkill) Description() string                { return "临时文件使用" }
func (s *TempUsageSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *TempUsageSkill) Validate(_ skill.Params) error      { return nil }
func (s *TempUsageSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/tempusage"} }

func (s *TempUsageSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "tempusage",
		Description: "Show temp file usage per database and live sessions spilling to temp",
	}
}

func (s *TempUsageSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	byDB, err := s.driver.Query(ctx, tempByDBSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	bySession, _ := s.driver.Query(ctx, tempBySessionSQL)

	dbRows := 0
	if byDB != nil {
		dbRows = len(byDB.Rows)
	}
	sessRows := 0
	if bySession != nil {
		sessRows = len(bySession.Rows)
	}

	// If any live session is spilling, prefer that view — it is the more
	// actionable signal.
	if sessRows > 0 {
		return &skill.Result{
			Type:     skill.ResultTable,
			Data:     bySession,
			Rendered: fmt.Sprintf("正在使用临时文件的会话 — %d 个（另有 %d 库累计数据）", sessRows, dbRows),
			Summary:  fmt.Sprintf("%d sessions spilling", sessRows),
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     byDB,
		Rendered: fmt.Sprintf("临时文件使用（按库）— %d 个", dbRows),
		Summary:  fmt.Sprintf("%d dbs", dbRows),
	}, nil
}
