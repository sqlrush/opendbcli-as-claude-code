/*-------------------------------------------------------------------------
 *
 * tableinfo.go
 *	  tableinfo — TableInfoSkill plus helpers (NewTableInfoSkill) used
 *	  by the schema package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/schema/tableinfo.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

type TableInfoSkill struct{ driver db.Driver }

func NewTableInfoSkill(driver db.Driver) *TableInfoSkill { return &TableInfoSkill{driver: driver} }

func (s *TableInfoSkill) Name() string                       { return "tableinfo" }
func (s *TableInfoSkill) Description() string                { return "表结构 + 列 + 索引 + 段大小" }
func (s *TableInfoSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TableInfoSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "tableinfo",
		Description: "Show DM table structure: columns + indexes + size",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"table": map[string]any{"type": "string", "description": "schema.table or table"},
			},
			"required": []string{"table"},
		},
	}
}

func (s *TableInfoSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "tableinfo", Usage: "/tableinfo <[schema.]table>"}
}

func (s *TableInfoSkill) Validate(p skill.Params) error {
	if strings.TrimSpace(p.StringOr("args", p.StringOr("table", ""))) == "" {
		return fmt.Errorf("tableinfo 需要表名")
	}
	return nil
}

func (s *TableInfoSkill) Execute(ctx context.Context, p skill.Params) (*skill.Result, error) {
	tbl := strings.TrimSpace(p.StringOr("args", p.StringOr("table", "")))
	schema := "OPENDB"
	name := strings.ToUpper(tbl)
	if i := strings.Index(name, "."); i > 0 {
		schema = name[:i]
		name = name[i+1:]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Table: %s.%s ===\n\n", schema, name))

	// 列
	colSQL := fmt.Sprintf(`SELECT COLUMN_NAME, DATA_TYPE, NULLABLE, DATA_DEFAULT
FROM ALL_TAB_COLUMNS WHERE OWNER='%s' AND TABLE_NAME='%s'
ORDER BY COLUMN_ID`, schema, name)
	r1, err := s.driver.Query(ctx, colSQL)
	if err != nil {
		return nil, fmt.Errorf("dm tableinfo cols: %w", err)
	}
	b.WriteString(fmt.Sprintf("--- Columns (%d) ---\n", len(r1.Rows)))
	for _, row := range r1.Rows {
		b.WriteString(fmt.Sprintf("  %v %v NULL=%v DEFAULT=%v\n", row[0], row[1], row[2], row[3]))
	}

	// 索引
	idxSQL := fmt.Sprintf(`SELECT INDEX_NAME, UNIQUENESS, STATUS
FROM ALL_INDEXES WHERE TABLE_OWNER='%s' AND TABLE_NAME='%s'`, schema, name)
	r2, err := s.driver.Query(ctx, idxSQL)
	if err == nil {
		b.WriteString(fmt.Sprintf("\n--- Indexes (%d) ---\n", len(r2.Rows)))
		for _, row := range r2.Rows {
			b.WriteString(fmt.Sprintf("  %v unique=%v status=%v\n", row[0], row[1], row[2]))
		}
	}

	// 段大小
	segSQL := fmt.Sprintf(`SELECT SEGMENT_NAME, SEGMENT_TYPE, BYTES
FROM DBA_SEGMENTS WHERE OWNER='%s' AND SEGMENT_NAME='%s'`, schema, name)
	r3, err := s.driver.Query(ctx, segSQL)
	totalBytes := int64(0)
	if err == nil && len(r3.Rows) > 0 {
		b.WriteString("\n--- Segment ---\n")
		for _, row := range r3.Rows {
			b.WriteString(fmt.Sprintf("  %v %v %v bytes\n", row[0], row[1], row[2]))
			if v, ok := row[2].(int64); ok {
				totalBytes += v
			}
		}
	}

	// [summary]
	b.WriteString("\n[summary]\n")
	b.WriteString(fmt.Sprintf("schema: %s\n", schema))
	b.WriteString(fmt.Sprintf("table: %s\n", name))
	b.WriteString(fmt.Sprintf("column_count: %d\n", len(r1.Rows)))
	if r2 != nil {
		b.WriteString(fmt.Sprintf("index_count: %d\n", len(r2.Rows)))
	}
	if totalBytes > 0 {
		b.WriteString(fmt.Sprintf("segment_total_bytes: %d\n", totalBytes))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("table info — %s.%s", schema, name),
	}, nil
}
