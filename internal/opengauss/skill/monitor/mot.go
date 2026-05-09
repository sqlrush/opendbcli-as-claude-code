/*-------------------------------------------------------------------------
 *
 * mot.go
 *	  MOTSkill shows MOT (Memory-Optimized Table) engine status —
 *	  OG-exclusive.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/mot.go
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

// motSessionMemSQL shows MOT per-session memory usage.
const motSessionMemSQL = `SELECT
  session_id,
  session_name,
  session_total_size,
  session_used_size,
  session_free_size
FROM mot_session_memory_detail
ORDER BY session_used_size DESC
LIMIT 20`

// motMemCfgSQL pulls MOT engine-wide memory configuration.
const motMemCfgSQL = `SELECT
  engine_name,
  total_size,
  reserved_size,
  used_size,
  free_size
FROM mot_mem_cfg`

// MOTSkill shows MOT (Memory-Optimized Table) engine status — OG-exclusive.
type MOTSkill struct{ driver db.Driver }

// NewMOTSkill creates a MOTSkill.
func NewMOTSkill(driver db.Driver) *MOTSkill { return &MOTSkill{driver: driver} }

func (s *MOTSkill) Name() string                       { return "mot" }
func (s *MOTSkill) Description() string                { return "MOT 内存表引擎状态" }
func (s *MOTSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *MOTSkill) Validate(_ skill.Params) error      { return nil }
func (s *MOTSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/mot"} }

func (s *MOTSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "mot",
		Description: "Show MOT (Memory-Optimized Table) engine memory usage (OG-exclusive)",
	}
}

func (s *MOTSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	cfg, err := s.driver.Query(ctx, motMemCfgSQL)
	if err != nil {
		// MOT is optional — likely the engine isn't installed.
		msg := fmt.Sprintf("MOT 视图不可用: %v\n提示: MOT 是 OG 独有引擎，需编译时启用 (--enable-mot)。", err)
		return &skill.Result{Type: skill.ResultText, Rendered: msg, Summary: "MOT unavailable"}, nil
	}

	sess, _ := s.driver.Query(ctx, motSessionMemSQL)
	sessRows := 0
	if sess != nil {
		sessRows = len(sess.Rows)
	}

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     cfg,
		Rendered: fmt.Sprintf("MOT 引擎内存配置（另有 %d 个 session 在用 MOT）", sessRows),
		Summary:  fmt.Sprintf("MOT cfg + %d sessions", sessRows),
	}, nil
}
