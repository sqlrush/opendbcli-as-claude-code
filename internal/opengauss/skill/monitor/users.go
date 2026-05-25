/*-------------------------------------------------------------------------
 *
 * users.go
 *	  UsersSkill shows user/role accounts with login and expiry info.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/users.go
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

const usersSQL = `SELECT
  rolname,
  rolcanlogin,
  rolsuper,
  rolcreaterole,
  rolcreatedb,
  rolvaliduntil::text,
  CASE
    WHEN rolvaliduntil IS NOT NULL
    THEN EXTRACT(DAY FROM rolvaliduntil - now())::int
    ELSE NULL
  END AS days_left
FROM pg_authid
WHERE rolname NOT LIKE 'pg_%'
ORDER BY rolvaliduntil NULLS LAST`

// UsersSkill shows user/role accounts with login and expiry info.
type UsersSkill struct{ driver db.Driver }

// NewUsersSkill creates a UsersSkill backed by the given driver.
func NewUsersSkill(driver db.Driver) *UsersSkill {
	return &UsersSkill{driver: driver}
}

func (s *UsersSkill) Name() string                       { return "users" }
func (s *UsersSkill) Description() string                { return "用户/角色列表" }
func (s *UsersSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *UsersSkill) Validate(_ skill.Params) error      { return nil }
func (s *UsersSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/users"} }
func (s *UsersSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "users", Description: "Show OpenGauss roles with login, superuser, and password expiry info"}
}

func (s *UsersSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, usersSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("数据库用户 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个用户", len(result.Rows)),
	}, nil
}
