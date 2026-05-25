/*-------------------------------------------------------------------------
 *
 * users.go
 *	  users — UsersSkill plus helpers (NewUsersSkill) used by the
 *	  monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/users.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const usersListSQL = `SELECT USERNAME, ACCOUNT_STATUS, DEFAULT_TABLESPACE,
       TEMPORARY_TABLESPACE, CREATED, EXPIRY_DATE
FROM DBA_USERS
ORDER BY USERNAME`

const userPrivsSQL = `SELECT GRANTEE, PRIVILEGE, ADMIN_OPTION
FROM DBA_SYS_PRIVS
WHERE GRANTEE = UPPER(?)
ORDER BY PRIVILEGE`

const userRolesSQL = `SELECT GRANTEE, GRANTED_ROLE, ADMIN_OPTION, DEFAULT_ROLE
FROM DBA_ROLE_PRIVS
WHERE GRANTEE = UPPER(?)
ORDER BY GRANTED_ROLE`

type UsersSkill struct{ driver db.Driver }

func NewUsersSkill(driver db.Driver) *UsersSkill { return &UsersSkill{driver: driver} }

func (s *UsersSkill) Name() string                       { return "users" }
func (s *UsersSkill) Description() string                { return "用户/角色/权限审计 (DBA_USERS / DBA_ROLE_PRIVS)" }
func (s *UsersSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *UsersSkill) Validate(_ skill.Params) error      { return nil }

func (s *UsersSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "users", Description: "List DM users / show user privileges"}
}
func (s *UsersSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "users",
		Usage:    "/users [username]",
		Examples: []string{"/users", "/users OPENDB"},
	}
}

func (s *UsersSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	if args == "" {
		return s.listAll(ctx)
	}
	return s.userDetail(ctx, args)
}

func (s *UsersSkill) listAll(ctx context.Context) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, usersListSQL)
	if err != nil {
		return nil, fmt.Errorf("dm users list: %w", err)
	}

	statusCount := dmutil.CountByCol(r.Rows, 1)
	entries := []dmutil.SummaryEntry{
		{Key: "total_users", Val: len(r.Rows)},
	}
	for status, n := range statusCount {
		entries = append(entries, dmutil.SummaryEntry{Key: "status_" + status, Val: n})
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("用户列表 — %d 个", len(r.Rows)),
	}, nil
}

func (s *UsersSkill) userDetail(ctx context.Context, user string) (*skill.Result, error) {
	privs, err := s.driver.Query(ctx, userPrivsSQL, user)
	if err != nil {
		return nil, fmt.Errorf("dm users privs: %w", err)
	}
	roles, err := s.driver.Query(ctx, userRolesSQL, user)
	if err != nil {
		return nil, fmt.Errorf("dm users roles: %w", err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== 用户 %s 系统权限 ===\n", strings.ToUpper(user)))
	b.WriteString(format.FormatTable(privs))
	b.WriteString(fmt.Sprintf("\n=== 用户 %s 角色 ===\n", strings.ToUpper(user)))
	b.WriteString(format.FormatTable(roles))

	entries := []dmutil.SummaryEntry{
		{Key: "user", Val: strings.ToUpper(user)},
		{Key: "system_privs_count", Val: len(privs.Rows)},
		{Key: "roles_count", Val: len(roles.Rows)},
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     privs,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  fmt.Sprintf("%s 权限 + 角色", strings.ToUpper(user)),
	}, nil
}
