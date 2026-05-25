/*-------------------------------------------------------------------------
 *
 * info.go
 *	  info — InfoSkill plus helpers (NewInfoSkill) used by the monitor
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/info.go
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

type InfoSkill struct{ driver db.Driver }

func NewInfoSkill(driver db.Driver) *InfoSkill { return &InfoSkill{driver: driver} }

func (s *InfoSkill) Name() string                       { return "info" }
func (s *InfoSkill) Description() string                { return "实例 + 数据库 + 版本 + 关键参数总览" }
func (s *InfoSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *InfoSkill) Validate(_ skill.Params) error      { return nil }
func (s *InfoSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "info", Description: "DM instance + database + key parameters summary"}
}
func (s *InfoSkill) CLIDef() skill.CLIDef { return skill.CLIDef{Command: "info", Usage: "/info"} }

func (s *InfoSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	var b strings.Builder
	var instName, build, startTime, dbName, role any
	roleStr := "PRIMARY"

	r1, err := s.driver.Query(ctx, "SELECT INSTANCE_NAME, BUILD_VERSION, START_TIME FROM V$INSTANCE")
	if err == nil && len(r1.Rows) > 0 {
		instName, build, startTime = r1.Rows[0][0], r1.Rows[0][1], r1.Rows[0][2]
		b.WriteString(fmt.Sprintf("Instance: %v  Build: %v  Start: %v\n", instName, build, startTime))
	}
	r2, err := s.driver.Query(ctx, "SELECT NAME, ROLE$ FROM V$DATABASE")
	if err == nil && len(r2.Rows) > 0 {
		dbName, role = r2.Rows[0][0], r2.Rows[0][1]
		if fmt.Sprintf("%v", role) == "1" {
			roleStr = "STANDBY"
		}
		b.WriteString(fmt.Sprintf("Database: %v  Role$: %v (%s)\n", dbName, role, roleStr))
	}
	const paramSQL = `SELECT PARA_NAME, PARA_VALUE FROM V$DM_INI
WHERE PARA_NAME IN ('COMPATIBLE_MODE','SVR_LOG','PORT_NUM','BUFFER','MEMORY_POOL',
                    'WORKER_THREADS','TASK_THREADS','CASE_SENSITIVE','ARCH_INI')
ORDER BY PARA_NAME`
	r3, err := s.driver.Query(ctx, paramSQL)
	params := map[string]any{}
	if err == nil {
		b.WriteString("\nKey parameters:\n")
		for _, row := range r3.Rows {
			b.WriteString(fmt.Sprintf("  %-20v = %v\n", row[0], row[1]))
			if len(row) >= 2 {
				params[fmt.Sprintf("%v", row[0])] = row[1]
			}
		}
	}

	// [summary]
	b.WriteString("\n[summary]\n")
	b.WriteString(fmt.Sprintf("instance_name: %v\n", instName))
	b.WriteString(fmt.Sprintf("build_version: %v\n", build))
	b.WriteString(fmt.Sprintf("start_time: %v\n", startTime))
	b.WriteString(fmt.Sprintf("db_name: %v\n", dbName))
	b.WriteString(fmt.Sprintf("role: %s\n", roleStr))
	for k, v := range params {
		b.WriteString(fmt.Sprintf("param_%s: %v\n", k, v))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  "instance + database + key params",
	}, nil
}
