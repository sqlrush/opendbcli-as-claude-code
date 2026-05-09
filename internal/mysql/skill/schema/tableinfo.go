/*-------------------------------------------------------------------------
 *
 * tableinfo.go
 *	  TableInfoSkill shows detailed table structure: columns, indexes,
 *	  constraints, stats.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/schema/tableinfo.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const columnsSQL = `SELECT
  COLUMN_NAME,
  COLUMN_TYPE,
  IS_NULLABLE,
  COLUMN_DEFAULT,
  COLUMN_KEY,
  EXTRA
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'
ORDER BY ORDINAL_POSITION`

const indexesSQL = `SELECT
  INDEX_NAME,
  SEQ_IN_INDEX,
  COLUMN_NAME,
  NON_UNIQUE,
  INDEX_TYPE,
  NULLABLE
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'
ORDER BY INDEX_NAME, SEQ_IN_INDEX`

const constraintsSQL = `SELECT
  CONSTRAINT_NAME,
  CONSTRAINT_TYPE
FROM information_schema.TABLE_CONSTRAINTS
WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'
ORDER BY CONSTRAINT_TYPE, CONSTRAINT_NAME`

const tableStatsSQL = `SELECT
  ENGINE,
  TABLE_ROWS,
  AVG_ROW_LENGTH,
  ROUND(DATA_LENGTH / 1048576, 2) AS data_mb,
  ROUND(INDEX_LENGTH / 1048576, 2) AS index_mb,
  ROUND(DATA_FREE / 1048576, 2) AS free_mb,
  AUTO_INCREMENT,
  CREATE_TIME,
  UPDATE_TIME,
  TABLE_COLLATION
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'`

// TableInfoSkill shows detailed table structure: columns, indexes, constraints, stats.
type TableInfoSkill struct {
	driver db.Driver
}

// NewTableInfoSkill creates a TableInfoSkill backed by the given driver.
func NewTableInfoSkill(driver db.Driver) *TableInfoSkill {
	return &TableInfoSkill{driver: driver}
}

func (s *TableInfoSkill) Name() string                       { return "tableinfo" }
func (s *TableInfoSkill) Description() string                { return "表结构详情" }
func (s *TableInfoSkill) Category() string                   { return "schema" }
func (s *TableInfoSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TableInfoSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/tableinfo <schema.table>",
		Examples: []string{"/tableinfo mydb.users", "/tableinfo mydb.orders"},
	}
}

func (s *TableInfoSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "tableinfo",
		Description: "Show table structure details: columns, indexes, constraints, stats",
		Parameters: map[string]any{
			"table": map[string]any{
				"type":        "string",
				"description": "Table name as schema.table",
			},
		},
	}
}

func (s *TableInfoSkill) Validate(params skill.Params) error {
	table := strings.TrimSpace(params.StringOr("args", ""))
	if table == "" {
		table = strings.TrimSpace(params.StringOr("table", ""))
	}
	if table == "" {
		return fmt.Errorf("请指定表名, 如: /tableinfo mydb.users")
	}
	if !strings.Contains(table, ".") {
		return fmt.Errorf("请使用 schema.table 格式, 如: /tableinfo mydb.users")
	}
	return nil
}

func (s *TableInfoSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	tableFull := strings.TrimSpace(params.StringOr("args", ""))
	if tableFull == "" {
		tableFull = strings.TrimSpace(params.StringOr("table", ""))
	}
	parts := strings.SplitN(tableFull, ".", 2)
	schemaName := parts[0]
	tableName := parts[1]

	sections := []format.PanelSection{}

	// Table stats
	statsResult, err := s.driver.Query(ctx, fmt.Sprintf(tableStatsSQL, schemaName, tableName))
	if err == nil && len(statsResult.Rows) > 0 {
		sections = append(sections, buildStatsSection(statsResult))
	} else if err != nil {
		errMsg := fmt.Sprintf("查询表统计失败: %s", err.Error())
		return &skill.Result{Type: skill.ResultError, Data: errMsg, Summary: errMsg}, nil
	}

	// Columns
	colResult, err := s.driver.Query(ctx, fmt.Sprintf(columnsSQL, schemaName, tableName))
	if err == nil && len(colResult.Rows) > 0 {
		sections = append(sections, buildColumnsSection(colResult))
	}

	// Indexes
	idxResult, err := s.driver.Query(ctx, fmt.Sprintf(indexesSQL, schemaName, tableName))
	if err == nil && len(idxResult.Rows) > 0 {
		sections = append(sections, buildIndexesSection(idxResult))
	}

	// Constraints
	conResult, err := s.driver.Query(ctx, fmt.Sprintf(constraintsSQL, schemaName, tableName))
	if err == nil && len(conResult.Rows) > 0 {
		sections = append(sections, buildConstraintsSection(conResult))
	}

	if len(sections) == 0 {
		errMsg := fmt.Sprintf("表 %s.%s 不存在或无权限", schemaName, tableName)
		return &skill.Result{
			Type:    skill.ResultError,
			Data:    errMsg,
			Summary: errMsg,
		}, nil
	}

	rendered := format.Panel(fmt.Sprintf("%s.%s", schemaName, tableName), sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  fmt.Sprintf("table info for %s.%s", schemaName, tableName),
	}, nil
}

func buildStatsSection(result *db.QueryResult) format.PanelSection {
	row := result.Rows[0]
	var lines []string
	labels := []string{"Engine", "Rows", "Avg Row Len", "Data MB", "Index MB", "Free MB", "Auto Inc", "Created", "Updated", "Collation"}
	for i, label := range labels {
		if i < len(row) {
			lines = append(lines, fmt.Sprintf("%-15s : %s", label, asStr(row[i])))
		}
	}
	return format.PanelSection{Header: "Stats", Lines: lines}
}

func buildColumnsSection(result *db.QueryResult) format.PanelSection {
	var lines []string
	for _, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		name := asStr(row[0])
		colType := asStr(row[1])
		nullable := asStr(row[2])
		defVal := asStr(row[3])
		key := asStr(row[4])
		extra := asStr(row[5])

		info := colType
		if key != "" {
			info += " [" + key + "]"
		}
		if nullable == "NO" {
			info += " NOT NULL"
		}
		if defVal != "" {
			info += " DEFAULT " + defVal
		}
		if extra != "" {
			info += " " + extra
		}

		lines = append(lines, fmt.Sprintf("%-25s %s", name, info))
	}
	return format.PanelSection{Header: "Columns", Lines: lines}
}

func buildIndexesSection(result *db.QueryResult) format.PanelSection {
	// Group columns by index name.
	type indexInfo struct {
		columns  []string
		unique   bool
		idxType  string
	}
	indexes := make(map[string]*indexInfo)
	var order []string

	for _, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		idxName := asStr(row[0])
		colName := asStr(row[2])
		nonUnique := asStr(row[3])
		idxType := asStr(row[4])

		idx, exists := indexes[idxName]
		if !exists {
			idx = &indexInfo{
				unique:  nonUnique == "0",
				idxType: idxType,
			}
			indexes[idxName] = idx
			order = append(order, idxName)
		}
		idx.columns = append(idx.columns, colName)
	}

	var lines []string
	for _, name := range order {
		idx := indexes[name]
		uniqueStr := ""
		if idx.unique {
			uniqueStr = "UNIQUE "
		}
		lines = append(lines, fmt.Sprintf("%-25s %s%s (%s)", name, uniqueStr, idx.idxType, strings.Join(idx.columns, ", ")))
	}
	return format.PanelSection{Header: "Indexes", Lines: lines}
}

func buildConstraintsSection(result *db.QueryResult) format.PanelSection {
	var lines []string
	for _, row := range result.Rows {
		if len(row) < 2 {
			continue
		}
		name := asStr(row[0])
		cType := asStr(row[1])
		lines = append(lines, fmt.Sprintf("%-25s %s", name, cType))
	}
	return format.PanelSection{Header: "Constraints", Lines: lines}
}

// asStr safely converts any value to string.
func asStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
