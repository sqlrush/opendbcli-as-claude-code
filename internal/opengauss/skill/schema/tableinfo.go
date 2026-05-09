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
 *	  internal/opengauss/skill/schema/tableinfo.go
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

const pgColumnsSQL = `SELECT
  a.attname AS column_name,
  pg_catalog.format_type(a.atttypid, a.atttypmod) AS data_type,
  CASE WHEN a.attnotnull THEN 'NOT NULL' ELSE '' END AS nullable,
  COALESCE(pg_get_expr(d.adbin, d.adrelid), '') AS column_default
FROM pg_catalog.pg_attribute a
LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attrelid = '%s.%s'::regclass
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY a.attnum`

const pgIndexesSQL = `SELECT
  i.relname AS index_name,
  am.amname AS index_type,
  CASE WHEN ix.indisunique THEN 'UNIQUE' ELSE '' END AS is_unique,
  CASE WHEN ix.indisprimary THEN 'PK' ELSE '' END AS is_pk,
  pg_get_indexdef(ix.indexrelid) AS index_def,
  pg_relation_size(i.oid) / 1048576 AS size_mb
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_am am ON am.oid = i.relam
WHERE ix.indrelid = '%s.%s'::regclass
ORDER BY ix.indisprimary DESC, i.relname`

const pgConstraintsSQL = `SELECT
  conname,
  contype,
  pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE conrelid = '%s.%s'::regclass
ORDER BY contype, conname`

// pgTableStatsSQL — use pg_stat_all_tables (covers both user and system
// tables) so /tableinfo works on system catalog objects like
// pg_catalog.pg_class without returning empty stats.
const pgTableStatsSQL = `SELECT
  pg_size_pretty(pg_total_relation_size('%s.%s'::regclass)) AS total_size,
  pg_size_pretty(pg_table_size('%s.%s'::regclass))          AS table_size,
  pg_size_pretty(pg_indexes_size('%s.%s'::regclass))        AS index_size,
  (SELECT n_live_tup FROM pg_stat_all_tables WHERE schemaname = '%s' AND relname = '%s') AS live_tup,
  (SELECT n_dead_tup FROM pg_stat_all_tables WHERE schemaname = '%s' AND relname = '%s') AS dead_tup,
  (SELECT last_analyze::text FROM pg_stat_all_tables WHERE schemaname = '%s' AND relname = '%s') AS last_analyze,
  (SELECT last_autovacuum::text FROM pg_stat_all_tables WHERE schemaname = '%s' AND relname = '%s') AS last_autovacuum`

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
func (s *TableInfoSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TableInfoSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/tableinfo <schema.table>",
		Examples: []string{"/tableinfo public.users", "/tableinfo myapp.orders"},
	}
}

func (s *TableInfoSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "tableinfo",
		Description: "Show table structure details: columns, indexes, constraints, statistics",
		Parameters: map[string]any{
			"table": map[string]any{
				"type":        "string",
				"description": "Table name as schema.table (e.g. public.users)",
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
		return fmt.Errorf("请指定表名, 如: /tableinfo public.users")
	}
	return nil
}

func (s *TableInfoSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	tableFull := strings.TrimSpace(params.StringOr("args", ""))
	if tableFull == "" {
		tableFull = strings.TrimSpace(params.StringOr("table", ""))
	}

	var schemaName, tableName string
	if strings.Contains(tableFull, ".") {
		parts := strings.SplitN(tableFull, ".", 2)
		schemaName = parts[0]
		tableName = parts[1]
	} else {
		schemaName = "public"
		tableName = tableFull
	}

	sections := []format.PanelSection{}

	// Table stats — don't fail entire command if stats unavailable (e.g. system catalog tables).
	statsSQL := fmt.Sprintf(pgTableStatsSQL,
		schemaName, tableName, schemaName, tableName, schemaName, tableName,
		schemaName, tableName, schemaName, tableName,
		schemaName, tableName, schemaName, tableName)
	statsResult, err := s.driver.Query(ctx, statsSQL)
	if err != nil && !strings.Contains(tableFull, ".") {
		// Retry with pg_catalog schema for system tables.
		schemaName = "pg_catalog"
		statsSQL = fmt.Sprintf(pgTableStatsSQL,
			schemaName, tableName, schemaName, tableName, schemaName, tableName,
			schemaName, tableName, schemaName, tableName,
			schemaName, tableName, schemaName, tableName)
		statsResult, err = s.driver.Query(ctx, statsSQL)
	}
	if err != nil {
		// Stats failure is non-fatal; continue with column/index queries.
		statsResult = nil
	}
	if statsResult != nil && len(statsResult.Rows) > 0 {
		row := statsResult.Rows[0]
		sections = append(sections, format.PanelSection{
			Header: "Stats",
			Lines: []string{
				// pg_size_pretty already formats with a unit suffix
				// (e.g. "712 kB"); don't append MB.
				fmt.Sprintf("Total Size      : %s", asStr(row[0])),
				fmt.Sprintf("Table Size      : %s", asStr(row[1])),
				fmt.Sprintf("Index Size      : %s", asStr(row[2])),
				fmt.Sprintf("Live Tuples     : %s", asStr(row[3])),
				fmt.Sprintf("Dead Tuples     : %s", asStr(row[4])),
				fmt.Sprintf("Last Analyze    : %s", asStr(row[5])),
				fmt.Sprintf("Last Autovacuum : %s", asStr(row[6])),
			},
		})
	}

	// Columns
	colSQL := fmt.Sprintf(pgColumnsSQL, schemaName, tableName)
	colResult, err := s.driver.Query(ctx, colSQL)
	if err == nil && len(colResult.Rows) > 0 {
		var colLines []string
		for _, row := range colResult.Rows {
			if len(row) < 4 {
				continue
			}
			name := asStr(row[0])
			dataType := asStr(row[1])
			nullable := asStr(row[2])
			defVal := asStr(row[3])

			info := dataType
			if nullable != "" {
				info += " " + nullable
			}
			if defVal != "" {
				info += " DEFAULT " + defVal
			}
			colLines = append(colLines, fmt.Sprintf("%-25s %s", name, info))
		}
		sections = append(sections, format.PanelSection{
			Header: "Columns",
			Lines:  colLines,
		})
	}

	// Indexes
	idxSQL := fmt.Sprintf(pgIndexesSQL, schemaName, tableName)
	idxResult, err := s.driver.Query(ctx, idxSQL)
	if err == nil && len(idxResult.Rows) > 0 {
		var idxLines []string
		for _, row := range idxResult.Rows {
			if len(row) < 6 {
				continue
			}
			name := asStr(row[0])
			idxType := asStr(row[1])
			unique := asStr(row[2])
			pk := asStr(row[3])
			sizeMB := asStr(row[5])

			label := idxType
			if unique != "" {
				label = unique + " " + label
			}
			if pk != "" {
				label = pk + " " + label
			}
			idxLines = append(idxLines, fmt.Sprintf("%-30s %s (%s MB)", name, label, sizeMB))
		}
		sections = append(sections, format.PanelSection{
			Header: "Indexes",
			Lines:  idxLines,
		})
	}

	// Constraints
	conSQL := fmt.Sprintf(pgConstraintsSQL, schemaName, tableName)
	conResult, err := s.driver.Query(ctx, conSQL)
	if err == nil && len(conResult.Rows) > 0 {
		var conLines []string
		for _, row := range conResult.Rows {
			if len(row) < 3 {
				continue
			}
			name := asStr(row[0])
			conType := constraintTypeName(asStr(row[1]))
			def := asStr(row[2])
			if len(def) > 60 {
				def = def[:57] + "..."
			}
			conLines = append(conLines, fmt.Sprintf("%-25s %s  %s", name, conType, def))
		}
		sections = append(sections, format.PanelSection{
			Header: "Constraints",
			Lines:  conLines,
		})
	}

	if len(sections) == 0 {
		msg := fmt.Sprintf("表 %s.%s 不存在或无权限", schemaName, tableName)
		return &skill.Result{
			Type:    skill.ResultError,
			Data:    msg,
			Summary: msg,
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

func constraintTypeName(code string) string {
	switch code {
	case "p":
		return "PRIMARY KEY"
	case "f":
		return "FOREIGN KEY"
	case "u":
		return "UNIQUE"
	case "c":
		return "CHECK"
	case "x":
		return "EXCLUSION"
	default:
		return code
	}
}

// asStr safely converts any value to string.
func asStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
