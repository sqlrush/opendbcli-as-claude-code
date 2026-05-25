/*-------------------------------------------------------------------------
 *
 * sql.go
 *	  sql — SQLSkill plus helpers (NewSQLSkill) used by the query
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/sql.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

type SQLSkill struct{ driver db.Driver }

func NewSQLSkill(driver db.Driver) *SQLSkill { return &SQLSkill{driver: driver} }

func (s *SQLSkill) Name() string                      { return "sql" }
func (s *SQLSkill) Description() string                { return "执行SQL语句" }
func (s *SQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SQLSkill) Validate(_ skill.Params) error      { return nil }
func (s *SQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/sql <statement>", Examples: []string{"/sql SELECT 1", "/sql \\dt"}}
}
func (s *SQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "sql", Description: "Execute a SQL statement directly against OpenGauss",
		Parameters: map[string]any{"sql": map[string]any{"type": "string", "description": "SQL statement"}}}
}

func (s *SQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	sqlStr := strings.TrimSpace(params.StringOr("args", ""))
	if sqlStr == "" {
		sqlStr = params.StringOr("sql", "")
	}
	if sqlStr == "" {
		return &skill.Result{Type: skill.ResultError, Summary: "missing input"}, nil
	}

	upper := strings.ToUpper(strings.TrimSpace(sqlStr))
	isQuery := strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "EXPLAIN") ||
		strings.HasPrefix(upper, "WITH") ||
		strings.HasPrefix(upper, "TABLE") ||
		strings.HasPrefix(upper, "VALUES")

	if isQuery {
		result, err := s.driver.Query(ctx, sqlStr)
		if err != nil {
			return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
		}
		return &skill.Result{Type: skill.ResultTable, Data: result}, nil
	}

	result, err := s.driver.Exec(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	return &skill.Result{Type: skill.ResultText, Summary: "OK", Data: result}, nil
}
