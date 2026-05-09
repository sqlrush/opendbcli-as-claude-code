/*-------------------------------------------------------------------------
 *
 * xid.go
 *	  xid — XIDSkill plus helpers (NewXIDSkill) used by the monitor
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/xid.go
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

const xidSQL = `SELECT
  datname,
  age(datfrozenxid) AS xid_age,
  ROUND(age(datfrozenxid)::numeric / 2147483647 * 100, 2) AS pct_to_wraparound,
  datfrozenxid::text
FROM pg_database
WHERE datname NOT IN ('template0')
ORDER BY age(datfrozenxid) DESC`

type XIDSkill struct{ driver db.Driver }

func NewXIDSkill(driver db.Driver) *XIDSkill { return &XIDSkill{driver: driver} }

func (s *XIDSkill) Name() string                      { return "xid" }
func (s *XIDSkill) Description() string                { return "事务ID年龄和Wraparound风险" }
func (s *XIDSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *XIDSkill) Validate(_ skill.Params) error      { return nil }
func (s *XIDSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/xid"} }
func (s *XIDSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "xid", Description: "Check transaction ID age and wraparound risk per database"}
}

func (s *XIDSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, xidSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("事务ID年龄 — %d 个数据库", len(result.Rows)),
		Summary:  fmt.Sprintf("XID age: %d 个数据库", len(result.Rows)),
	}, nil
}
