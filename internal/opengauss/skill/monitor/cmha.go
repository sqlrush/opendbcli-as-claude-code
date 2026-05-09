/*-------------------------------------------------------------------------
 *
 * cmha.go
 *	  CMHASkill shows cluster HA state from the DB side.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/cmha.go
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

// cmhaRoleSQL reads the local node replication role + sync state. In OG
// enterprise deployments the CM (Cluster Manager) drives this; from the DB
// side we can at least see who we are (primary/standby) and how far behind
// the standby is.
const cmhaRoleSQL = `SELECT
  local_role,
  static_connections,
  db_state,
  detail_information
FROM pg_stat_get_stream_replications()`

const cmhaStandbySQL = `SELECT
  application_name,
  client_addr::text,
  state,
  sync_state,
  sync_priority,
  write_lag::text,
  flush_lag::text,
  replay_lag::text
FROM pg_stat_replication
ORDER BY application_name`

// CMHASkill shows cluster HA state from the DB side.
type CMHASkill struct{ driver db.Driver }

// NewCMHASkill creates a CMHASkill.
func NewCMHASkill(driver db.Driver) *CMHASkill { return &CMHASkill{driver: driver} }

func (s *CMHASkill) Name() string                       { return "cmha" }
func (s *CMHASkill) Description() string                { return "CM 集群 / 双机热备状态" }
func (s *CMHASkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *CMHASkill) Validate(_ skill.Params) error      { return nil }
func (s *CMHASkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/cmha"} }

func (s *CMHASkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "cmha",
		Description: "Show cluster/HA state (local role + standby replication lag); OG CM full state via `cm_ctl query`",
	}
}

func (s *CMHASkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	role, err := s.driver.Query(ctx, cmhaRoleSQL)
	standby, stErr := s.driver.Query(ctx, cmhaStandbySQL)

	// If pg_stat_get_stream_replications() isn't present (plain PG-compatible
	// install), fall back to pg_stat_replication alone.
	if err != nil && stErr != nil {
		msg := fmt.Sprintf("读取集群状态失败: %v\n提示: 非 HA 部署可能没有流复制视图，或需要 cm_ctl query 在 shell 查询。", err)
		return &skill.Result{Type: skill.ResultText, Rendered: msg, Summary: "HA views unavailable"}, nil
	}

	roleSummary := "未知"
	if role != nil && len(role.Rows) > 0 && len(role.Rows[0]) > 0 && role.Rows[0][0] != nil {
		roleSummary = fmt.Sprintf("%v", role.Rows[0][0])
	}
	stRows := 0
	if standby != nil {
		stRows = len(standby.Rows)
	}

	// Prefer the standby table as primary data — that's where replication lag
	// shows up.
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     standby,
		Rendered: fmt.Sprintf("本地角色: %s    standby: %d 个", roleSummary, stRows),
		Summary:  fmt.Sprintf("role=%s, %d standbys", roleSummary, stRows),
	}, nil
}
