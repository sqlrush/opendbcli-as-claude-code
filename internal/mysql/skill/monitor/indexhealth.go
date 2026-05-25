/*-------------------------------------------------------------------------
 *
 * indexhealth.go
 *	  IndexHealthSkill shows unused indexes from the sys schema.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/indexhealth.go
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

const indexHealthSQL = `SELECT
  object_schema,
  object_name,
  index_name
FROM sys.schema_unused_indexes
WHERE object_schema NOT IN ('mysql', 'performance_schema', 'sys')
LIMIT 20`

// IndexHealthSkill shows unused indexes from the sys schema.
type IndexHealthSkill struct {
	driver db.Driver
}

// NewIndexHealthSkill creates an IndexHealthSkill backed by the given driver.
func NewIndexHealthSkill(driver db.Driver) *IndexHealthSkill {
	return &IndexHealthSkill{driver: driver}
}

func (s *IndexHealthSkill) Name() string                       { return "indexhealth" }
func (s *IndexHealthSkill) Description() string                { return "未使用索引检查" }
func (s *IndexHealthSkill) Category() string                   { return "monitor" }
func (s *IndexHealthSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *IndexHealthSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/indexhealth", Examples: []string{"/indexhealth"}}
}

func (s *IndexHealthSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "indexhealth",
		Description: "Show unused indexes from sys.schema_unused_indexes (candidates for removal)",
	}
}

func (s *IndexHealthSkill) Validate(_ skill.Params) error { return nil }

func (s *IndexHealthSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, indexHealthSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "所有索引均有使用记录",
			Summary:  "no unused indexes",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("未使用索引 — %d 个 (候选删除)", len(result.Rows)),
		Summary:  fmt.Sprintf("索引健康: %d 个未使用索引", len(result.Rows)),
	}, nil
}
