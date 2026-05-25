/*-------------------------------------------------------------------------
 *
 * extensions.go
 *	  ExtensionsSkill shows installed PostgreSQL extensions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/extensions.go
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

const extensionsSQL = `SELECT
  e.extname,
  e.extversion,
  n.nspname AS schema,
  c.description
FROM pg_extension e
JOIN pg_namespace n ON n.oid = e.extnamespace
LEFT JOIN pg_description c ON c.objoid = e.oid
ORDER BY e.extname`

// ExtensionsSkill shows installed PostgreSQL extensions.
type ExtensionsSkill struct{ driver db.Driver }

// NewExtensionsSkill creates an ExtensionsSkill backed by the given driver.
func NewExtensionsSkill(driver db.Driver) *ExtensionsSkill {
	return &ExtensionsSkill{driver: driver}
}

func (s *ExtensionsSkill) Name() string                       { return "extensions" }
func (s *ExtensionsSkill) Description() string                { return "已安装扩展列表" }
func (s *ExtensionsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ExtensionsSkill) Validate(_ skill.Params) error      { return nil }
func (s *ExtensionsSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/extensions"} }
func (s *ExtensionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "extensions", Description: "Show installed PostgreSQL extensions with version and schema"}
}

func (s *ExtensionsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, extensionsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无已安装扩展",
			Summary:  "no extensions",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("已安装扩展 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个扩展", len(result.Rows)),
	}, nil
}
