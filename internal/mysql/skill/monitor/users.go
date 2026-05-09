/*-------------------------------------------------------------------------
 *
 * users.go
 *	  UsersSkill shows MySQL user accounts.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/users.go
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
  User,
  Host,
  account_locked,
  password_expired,
  password_lifetime
FROM mysql.user
WHERE User NOT IN ('mysql.sys', 'mysql.session', 'mysql.infoschema')
ORDER BY password_expired DESC, User`

// UsersSkill shows MySQL user accounts.
type UsersSkill struct {
	driver db.Driver
}

// NewUsersSkill creates a UsersSkill backed by the given driver.
func NewUsersSkill(driver db.Driver) *UsersSkill {
	return &UsersSkill{driver: driver}
}

func (s *UsersSkill) Name() string                       { return "users" }
func (s *UsersSkill) Description() string                { return "用户账户列表" }
func (s *UsersSkill) Category() string                   { return "monitor" }
func (s *UsersSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *UsersSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/users", Examples: []string{"/users"}}
}

func (s *UsersSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "users",
		Description: "List MySQL user accounts with lock status and password expiry",
	}
}

func (s *UsersSkill) Validate(_ skill.Params) error { return nil }

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
