/*-------------------------------------------------------------------------
 *
 * users_test.go
 *	  Test cases for users.go (monitor package):
 *	  TestUsersSkill_Metadata, TestUsersSkill_ListAll,
 *	  TestUsersSkill_Detail.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/users_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestUsersSkill_Metadata(t *testing.T) {
	s := NewUsersSkill(makeRoutedDriver())
	if s.Name() != "users" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestUsersSkill_ListAll(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "DBA_USERS",
		result: &db.QueryResult{
			Columns: []string{"USERNAME", "ACCOUNT_STATUS", "DEFAULT_TABLESPACE", "TEMPORARY_TABLESPACE", "CREATED", "EXPIRY_DATE"},
			Rows: [][]any{
				{"SYS", "OPEN", "MAIN", "TEMP", "2026-04-01", nil},
				{"OPENDB", "OPEN", "MAIN", "TEMP", "2026-04-15", "2027-04-15"},
				{"TEST_LOCKED", "LOCKED", "MAIN", "TEMP", "2026-03-01", nil},
			},
		},
	})
	r, _ := NewUsersSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "total_users: 3")
	assertSummaryContains(t, r.Rendered, "status_OPEN: 2")
	assertSummaryContains(t, r.Rendered, "status_LOCKED: 1")
}

func TestUsersSkill_Detail(t *testing.T) {
	var sqls []string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, args ...any) (*db.QueryResult, error) {
		sqls = append(sqls, sql)
		switch {
		case strings.Contains(sql, "DBA_SYS_PRIVS"):
			return &db.QueryResult{
				Columns: []string{"GRANTEE", "PRIVILEGE", "ADMIN_OPTION"},
				Rows: [][]any{
					{"OPENDB", "SELECT ANY TABLE", "NO"},
					{"OPENDB", "CREATE SESSION", "NO"},
				},
			}, nil
		case strings.Contains(sql, "DBA_ROLE_PRIVS"):
			return &db.QueryResult{
				Columns: []string{"GRANTEE", "GRANTED_ROLE", "ADMIN_OPTION", "DEFAULT_ROLE"},
				Rows: [][]any{
					{"OPENDB", "RESOURCE", "NO", "YES"},
				},
			}, nil
		}
		return &db.QueryResult{}, nil
	}
	r, _ := NewUsersSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "opendb"}))

	// 详情路径必须发两次 SQL: privs + roles
	if len(sqls) != 2 {
		t.Errorf("expected 2 queries (privs+roles), got %d", len(sqls))
	}
	assertSummaryContains(t, r.Rendered, "user: OPENDB")
	assertSummaryContains(t, r.Rendered, "system_privs_count: 2")
	assertSummaryContains(t, r.Rendered, "roles_count: 1")
}
