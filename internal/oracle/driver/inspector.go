/*-------------------------------------------------------------------------
 *
 * inspector.go
 *	  Database introspection helpers for the Oracle driver — wraps
 *	  DBA_/V$ lookups (object existence, column types, partition
 *	  info) used by skills and /tableinfo.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/driver/inspector.go
 *
 *-------------------------------------------------------------------------
 */
package driver

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
)

// Inspector SQL queries for Oracle data dictionary.
const (
	querySchemas = `SELECT username FROM dba_users
		WHERE oracle_maintained = 'N'
		ORDER BY username`

	queryTables = `SELECT owner, table_name, num_rows,
		NVL(
			(SELECT SUM(bytes) FROM dba_segments
			 WHERE segment_name = t.table_name AND owner = t.owner),
			0
		) AS size_bytes
		FROM dba_tables t
		WHERE owner = :schema
		ORDER BY table_name`

	queryColumns = `SELECT column_name, data_type, nullable, data_default
		FROM dba_tab_columns
		WHERE owner = :schema AND table_name = :table
		ORDER BY column_id`
)

// Databases returns an empty list since Oracle does not have the concept of
// multiple databases within a single instance in the same way as PostgreSQL/MySQL.
// Use Schemas to list Oracle users/schemas instead.
func (d *OracleDriver) Databases(ctx context.Context) ([]string, error) {
	return []string{}, nil
}

// Schemas returns the list of non-Oracle-maintained user schemas.
func (d *OracleDriver) Schemas(ctx context.Context) ([]string, error) {
	rows, err := d.conn.QueryContext(ctx, querySchemas)
	if err != nil {
		return nil, fmt.Errorf("failed to query schemas: %w", err)
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan schema: %w", err)
		}
		schemas = append(schemas, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schema iteration error: %w", err)
	}
	return schemas, nil
}

// Tables returns table metadata for the given schema.
func (d *OracleDriver) Tables(ctx context.Context, schema string) ([]db.TableInfo, error) {
	if schema == "" {
		return nil, fmt.Errorf("schema is required")
	}

	rows, err := d.conn.QueryContext(ctx, queryTables, schema)
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	var tables []db.TableInfo
	for rows.Next() {
		var t db.TableInfo
		var numRows *int64
		if err := rows.Scan(&t.Schema, &t.Name, &numRows, &t.SizeBytes); err != nil {
			return nil, fmt.Errorf("failed to scan table: %w", err)
		}
		if numRows != nil {
			t.RowCount = *numRows
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("table iteration error: %w", err)
	}
	return tables, nil
}

// Columns returns column metadata for the given schema and table.
func (d *OracleDriver) Columns(ctx context.Context, schema, table string) ([]db.ColumnInfo, error) {
	if schema == "" {
		return nil, fmt.Errorf("schema is required")
	}
	if table == "" {
		return nil, fmt.Errorf("table is required")
	}

	rows, err := d.conn.QueryContext(ctx, queryColumns, schema, table)
	if err != nil {
		return nil, fmt.Errorf("failed to query columns: %w", err)
	}
	defer rows.Close()

	var columns []db.ColumnInfo
	for rows.Next() {
		var c db.ColumnInfo
		var nullable string
		var defaultVal *string
		if err := rows.Scan(&c.Name, &c.Type, &nullable, &defaultVal); err != nil {
			return nil, fmt.Errorf("failed to scan column: %w", err)
		}
		c.Nullable = nullable == "Y"
		if defaultVal != nil {
			c.Default = *defaultVal
		}
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("column iteration error: %w", err)
	}
	return columns, nil
}
