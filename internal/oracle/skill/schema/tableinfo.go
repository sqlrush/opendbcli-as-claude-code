/*-------------------------------------------------------------------------
 *
 * tableinfo.go
 *	  ColumnInfo is one column's metadata.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/schema/tableinfo.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const columnsSQL = `SELECT column_name, data_type, data_length, data_precision, data_scale,
       nullable, data_default
FROM dba_tab_columns
WHERE table_name = UPPER(:1)
AND owner = NVL(UPPER(:2), USER)
ORDER BY column_id`

const indexesSQL = `SELECT i.index_name, i.uniqueness, i.status,
       LISTAGG(ic.column_name, ', ') WITHIN GROUP (ORDER BY ic.column_position) as columns
FROM dba_indexes i
JOIN dba_ind_columns ic ON i.index_name = ic.index_name AND i.owner = ic.index_owner
WHERE i.table_name = UPPER(:1)
AND i.owner = NVL(UPPER(:2), USER)
GROUP BY i.index_name, i.uniqueness, i.status`

const tableStatsSQL = `SELECT num_rows, blocks, avg_row_len, last_analyzed
FROM dba_tables
WHERE table_name = UPPER(:1)
AND owner = NVL(UPPER(:2), USER)`

const constraintsSQL = `SELECT constraint_name, constraint_type, status,
       search_condition, r_constraint_name
FROM dba_constraints
WHERE table_name = UPPER(:1)
AND owner = NVL(UPPER(:2), USER)
ORDER BY constraint_type`

// ColumnInfo is one column's metadata.
type ColumnInfo struct {
	Name          string `json:"name"`
	DataType      string `json:"data_type"`
	DataLength    string `json:"data_length"`
	DataPrecision string `json:"data_precision,omitempty"`
	DataScale     string `json:"data_scale,omitempty"`
	Nullable      string `json:"nullable"`
	DataDefault   string `json:"data_default,omitempty"`
}

// IndexInfo is one index's metadata.
type IndexInfo struct {
	Name       string `json:"name"`
	Uniqueness string `json:"uniqueness"`
	Status     string `json:"status"`
	Columns    string `json:"columns"`
}

// TableStats holds table-level statistics.
type TableStats struct {
	NumRows      string `json:"num_rows"`
	Blocks       string `json:"blocks"`
	AvgRowLen    string `json:"avg_row_len"`
	LastAnalyzed string `json:"last_analyzed"`
}

// ConstraintInfo is one constraint's metadata.
type ConstraintInfo struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Status          string `json:"status"`
	SearchCondition string `json:"search_condition,omitempty"`
	RConstraintName string `json:"r_constraint_name,omitempty"`
}

// TableInfoData is the structured output of the tableinfo skill for LLM consumption.
type TableInfoData struct {
	Table       string           `json:"table"`
	Owner       string           `json:"owner,omitempty"`
	Columns     []ColumnInfo     `json:"columns"`
	Indexes     []IndexInfo      `json:"indexes"`
	Stats       *TableStats      `json:"stats,omitempty"`
	Constraints []ConstraintInfo `json:"constraints"`
}

// TableInfoSkill shows detailed table information including columns,
// indexes, statistics, and constraints.
type TableInfoSkill struct {
	driver db.Driver
}

// NewTableInfoSkill creates a TableInfoSkill backed by the given driver.
func NewTableInfoSkill(driver db.Driver) *TableInfoSkill {
	return &TableInfoSkill{driver: driver}
}

func (s *TableInfoSkill) Name() string                       { return "tableinfo" }
func (s *TableInfoSkill) Description() string                { return "Show detailed table information" }
func (s *TableInfoSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TableInfoSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "tableinfo",
		Description: "Show detailed table information (columns, indexes, stats, constraints)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Table name, optionally prefixed with schema (e.g., ORDERS or HR.ORDERS)",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *TableInfoSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "tableinfo",
		Aliases:  []string{"ti"},
		Usage:    "/tableinfo <table_name>",
		Examples: []string{"/tableinfo ORDERS", "/tableinfo HR.EMPLOYEES"},
	}
}

func (s *TableInfoSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("tableinfo requires a table name")
	}
	return nil
}

func (s *TableInfoSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	tableName, owner := parseTableRef(args)

	var b strings.Builder
	var data TableInfoData
	data.Table = tableName
	data.Owner = owner

	// Columns
	colResult, err := s.driver.Query(ctx, columnsSQL, tableName, owner)
	if err != nil {
		return nil, fmt.Errorf("querying columns: %w", err)
	}
	writeTableSection(&b, "Columns", "columns", colResult)
	data.Columns = parseColumns(colResult)

	// Indexes
	idxResult, err := s.driver.Query(ctx, indexesSQL, tableName, owner)
	if err != nil {
		return nil, fmt.Errorf("querying indexes: %w", err)
	}
	writeTableSection(&b, "Indexes", "indexes", idxResult)
	data.Indexes = parseIndexes(idxResult)

	// Stats (key-value format)
	statResult, err := s.driver.Query(ctx, tableStatsSQL, tableName, owner)
	if err != nil {
		return nil, fmt.Errorf("querying table stats: %w", err)
	}
	writeStatsSection(&b, statResult)
	data.Stats = parseTableStats(statResult)

	// Constraints
	conResult, err := s.driver.Query(ctx, constraintsSQL, tableName, owner)
	if err != nil {
		return nil, fmt.Errorf("querying constraints: %w", err)
	}
	writeTableSection(&b, "Constraints", "constraints", conResult)
	data.Constraints = parseConstraints(conResult)

	label := tableName
	if owner != "" {
		label = owner + "." + tableName
	}

	summary := fmt.Sprintf("表结构: %s — %d 列, %d 索引",
		label, len(data.Columns), len(data.Indexes))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &data,
		Rendered: b.String(),
		Summary:  summary,
		Metadata: map[string]string{
			"table": tableName,
			"owner": owner,
		},
	}, nil
}

// parseTableRef splits "SCHEMA.TABLE" into (table, owner).
// If no dot is present, owner is empty (will default to current user via NVL).
func parseTableRef(ref string) (table, owner string) {
	if idx := strings.IndexByte(ref, '.'); idx >= 0 {
		return ref[idx+1:], ref[:idx]
	}
	return ref, ""
}

// writeTableSection renders a section with a box-drawing header and formatted table.
func writeTableSection(b *strings.Builder, title, emptyLabel string, result *db.QueryResult) {
	b.WriteString("── " + title + " ──\n")
	if len(result.Rows) == 0 {
		b.WriteString("(no " + emptyLabel + " found)\n")
	} else {
		b.WriteString(format.FormatTableOpts(result, format.TableOptions{MaxRows: 200, TermWidth: 120}))
	}
	b.WriteByte('\n')
}

// writeStatsSection renders table statistics as key-value pairs.
func writeStatsSection(b *strings.Builder, result *db.QueryResult) {
	b.WriteString("── Table Statistics ──\n")
	if len(result.Rows) == 0 {
		b.WriteString("(no statistics found)\n")
	} else {
		row := result.Rows[0]
		labels := []string{"Num Rows", "Blocks", "Avg Row Len", "Last Analyzed"}
		for i, label := range labels {
			val := "-"
			if i < len(row) && row[i] != nil {
				switch v := row[i].(type) {
				case time.Time:
					val = v.Format("2006-01-02 15:04:05")
				default:
					s := fmt.Sprint(row[i])
					if s != "" && s != "<nil>" {
						val = s
					}
				}
			}
			fmt.Fprintf(b, "  %-16s %s\n", label+":", val)
		}
	}
	b.WriteByte('\n')
}

// formatRow builds a "COL=val | COL=val" string from columns and row values.
// Retained for test compatibility.
func formatRow(columns []string, row []any) string {
	parts := make([]string, 0, len(columns))
	for i, col := range columns {
		val := ""
		if i < len(row) && row[i] != nil {
			val = fmt.Sprint(row[i])
		}
		parts = append(parts, fmt.Sprintf("%s=%s", col, val))
	}
	return strings.Join(parts, " | ")
}

func parseColumns(result *db.QueryResult) []ColumnInfo {
	cols := make([]ColumnInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		cols = append(cols, ColumnInfo{
			Name:          fmtVal(row, 0),
			DataType:      fmtVal(row, 1),
			DataLength:    fmtVal(row, 2),
			DataPrecision: fmtVal(row, 3),
			DataScale:     fmtVal(row, 4),
			Nullable:      fmtVal(row, 5),
			DataDefault:   fmtVal(row, 6),
		})
	}
	return cols
}

func parseIndexes(result *db.QueryResult) []IndexInfo {
	idxs := make([]IndexInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		idxs = append(idxs, IndexInfo{
			Name:       fmtVal(row, 0),
			Uniqueness: fmtVal(row, 1),
			Status:     fmtVal(row, 2),
			Columns:    fmtVal(row, 3),
		})
	}
	return idxs
}

func parseTableStats(result *db.QueryResult) *TableStats {
	if len(result.Rows) == 0 {
		return nil
	}
	row := result.Rows[0]
	return &TableStats{
		NumRows:      fmtVal(row, 0),
		Blocks:       fmtVal(row, 1),
		AvgRowLen:    fmtVal(row, 2),
		LastAnalyzed: fmtVal(row, 3),
	}
}

func parseConstraints(result *db.QueryResult) []ConstraintInfo {
	cons := make([]ConstraintInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		cons = append(cons, ConstraintInfo{
			Name:            fmtVal(row, 0),
			Type:            fmtVal(row, 1),
			Status:          fmtVal(row, 2),
			SearchCondition: fmtVal(row, 3),
			RConstraintName: fmtVal(row, 4),
		})
	}
	return cons
}

func fmtVal(row []any, idx int) string {
	if idx >= len(row) || row[idx] == nil {
		return ""
	}
	return fmt.Sprintf("%v", row[idx])
}
