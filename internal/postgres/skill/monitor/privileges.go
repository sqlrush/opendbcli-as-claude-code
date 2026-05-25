/*-------------------------------------------------------------------------
 *
 * privileges.go
 *	  privileges — PrivilegesSkill plus helpers (NewPrivilegesSkill)
 *	  used by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/privileges.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const rolesSQL = `SELECT
  r.rolname,
  r.rolsuper,
  r.rolcreaterole,
  r.rolcreatedb,
  r.rolcanlogin,
  r.rolreplication,
  r.rolbypassrls,
  COALESCE(r.rolconnlimit, -1) AS connlimit,
  COALESCE(string_agg(m.rolname, ', ' ORDER BY m.rolname), '') AS member_of
FROM pg_roles r
LEFT JOIN pg_auth_members am ON r.oid = am.member
LEFT JOIN pg_roles m ON am.roleid = m.oid
WHERE r.rolname NOT LIKE 'pg_%'
GROUP BY r.rolname, r.rolsuper, r.rolcreaterole, r.rolcreatedb,
         r.rolcanlogin, r.rolreplication, r.rolbypassrls, r.rolconnlimit
ORDER BY r.rolsuper DESC, r.rolcanlogin DESC, r.rolname`

const secDefinerFuncsSQL = `SELECT
  n.nspname AS schema,
  p.proname AS function_name,
  pg_get_userbyid(p.proowner) AS owner,
  pg_get_function_arguments(p.oid) AS args
FROM pg_proc p
JOIN pg_namespace n ON p.pronamespace = n.oid
WHERE p.prosecdef = true
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY n.nspname, p.proname
LIMIT 30`

type PrivilegesSkill struct{ driver db.Driver }

func NewPrivilegesSkill(driver db.Driver) *PrivilegesSkill {
	return &PrivilegesSkill{driver: driver}
}

func (s *PrivilegesSkill) Name() string                      { return "privileges" }
func (s *PrivilegesSkill) Description() string               { return "user roles and privilege chain" }
func (s *PrivilegesSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *PrivilegesSkill) Validate(_ skill.Params) error     { return nil }
func (s *PrivilegesSkill) CLIDef() skill.CLIDef              { return skill.CLIDef{Usage: "/privileges"} }
func (s *PrivilegesSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "privileges",
		Description: "Show user roles, privilege attributes (superuser/createrole/createdb/login/replication/bypassrls), role memberships, and SECURITY DEFINER functions for security auditing",
	}
}

func (s *PrivilegesSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	var sections []format.PanelSection

	// Roles and memberships
	rolesResult, err := s.driver.Query(ctx, rolesSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	rolesLines := formatRolesPanel(rolesResult)
	sections = append(sections, format.PanelSection{Header: "Roles & Memberships", Lines: rolesLines})

	// SECURITY DEFINER functions
	secResult, _ := s.driver.Query(ctx, secDefinerFuncsSQL)
	secLines := formatSecDefinerPanel(secResult)
	sections = append(sections, format.PanelSection{Header: "SECURITY DEFINER Functions", Lines: secLines})

	rendered := format.Panel("Privileges Overview", sections)
	superCount := countSuperusers(rolesResult)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("Roles: %d (%d superuser), SECURITY DEFINER: %d", len(rolesResult.Rows), superCount, len(secResult.Rows)),
	}, nil
}

func formatRolesPanel(qr *db.QueryResult) []string {
	if qr == nil || len(qr.Rows) == 0 {
		return []string{"  No roles found"}
	}

	const (
		nameW   = 16
		attrsW  = 36
		memberW = 30
		limitW  = 6
	)

	header := " " + format.PadRight("Role", nameW) + " " +
		format.PadRight("Attributes", attrsW) + " " +
		format.PadLeft("Limit", limitW) + " " +
		format.PadRight("Member Of", memberW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	var rows []string
	for _, row := range qr.Rows {
		if len(row) < 9 {
			continue
		}
		name := fmtVal(row[0])
		attrs := buildAttrs(row)
		limit := fmtVal(row[7])
		if limit == "-1" {
			limit = "-"
		}
		memberOf := fmtVal(row[8])
		if memberOf == "" {
			memberOf = "-"
		}

		line := " " + format.PadRight(format.TruncDisplayWidth(name, nameW), nameW) + " " +
			format.PadRight(format.TruncDisplayWidth(attrs, attrsW), attrsW) + " " +
			format.PadLeft(limit, limitW) + " " +
			format.PadRight(format.TruncDisplayWidth(memberOf, memberW), memberW)
		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)
	return lines
}

func buildAttrs(row []any) string {
	var attrs []string
	if isTruthy(row[1]) {
		attrs = append(attrs, "SUPERUSER")
	}
	if isTruthy(row[2]) {
		attrs = append(attrs, "CREATEROLE")
	}
	if isTruthy(row[3]) {
		attrs = append(attrs, "CREATEDB")
	}
	if isTruthy(row[4]) {
		attrs = append(attrs, "LOGIN")
	}
	if isTruthy(row[5]) {
		attrs = append(attrs, "REPLICATION")
	}
	if isTruthy(row[6]) {
		attrs = append(attrs, "BYPASSRLS")
	}
	if len(attrs) == 0 {
		return "-"
	}
	result := ""
	for i, a := range attrs {
		if i > 0 {
			result += ", "
		}
		result += a
	}
	return result
}

func isTruthy(v any) bool {
	s := fmtVal(v)
	return s == "true" || s == "t" || s == "1"
}

func formatSecDefinerPanel(qr *db.QueryResult) []string {
	if qr == nil || len(qr.Rows) == 0 {
		return []string{"  No SECURITY DEFINER functions found"}
	}

	var lines []string
	for _, row := range qr.Rows {
		if len(row) < 4 {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s.%s(%s)  owner: %s",
			fmtVal(row[0]), fmtVal(row[1]), fmtVal(row[3]), fmtVal(row[2])))
	}
	return lines
}

func countSuperusers(qr *db.QueryResult) int {
	count := 0
	if qr == nil {
		return 0
	}
	for _, row := range qr.Rows {
		if len(row) >= 2 && isTruthy(row[1]) {
			count++
		}
	}
	return count
}
