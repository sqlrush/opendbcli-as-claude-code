/*-------------------------------------------------------------------------
 *
 * tableinfo_test.go
 *	  Test cases for tableinfo.go (schema package):
 *	  TestTableInfoSkill_Metadata, TestTableInfoSkill_Validate,
 *	  TestTableInfoSkill_Execute_AllThreeQueries.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/schema/tableinfo_test.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestTableInfoSkill_Metadata(t *testing.T) {
	s := NewTableInfoSkill(mock.NewMockDriver())
	if s.Name() != "tableinfo" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v", s.SecurityLevel())
	}
	if s.CLIDef().Command != "tableinfo" {
		t.Errorf("CLIDef().Command = %q", s.CLIDef().Command)
	}
}

func TestTableInfoSkill_Validate(t *testing.T) {
	s := NewTableInfoSkill(mock.NewMockDriver())
	if err := s.Validate(skill.ParamsFromMap(nil)); err == nil {
		t.Error("Validate(empty) should error")
	}
	if err := s.Validate(skill.ParamsFromMap(map[string]any{"args": "BENCH_USERS"})); err != nil {
		t.Errorf("Validate(args) = %v", err)
	}
	if err := s.Validate(skill.ParamsFromMap(map[string]any{"table": "BENCH_USERS"})); err != nil {
		t.Errorf("Validate(table) = %v", err)
	}
}

// TableInfo 调三个 SQL: ALL_TAB_COLUMNS, ALL_INDEXES, DBA_SEGMENTS.
// 用 sqlRouter 给每个 SQL 定向返回数据.
func TestTableInfoSkill_Execute_AllThreeQueries(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		switch {
		case strings.Contains(sql, "ALL_TAB_COLUMNS"):
			return &db.QueryResult{
				Columns: []string{"COLUMN_NAME", "DATA_TYPE", "NULLABLE", "DATA_DEFAULT"},
				Rows: [][]any{
					{"USER_ID", "BIGINT", "N", nil},
					{"STATUS", "SMALLINT", "Y", "0"},
					{"NAME", "VARCHAR(50)", "Y", nil},
				},
			}, nil
		case strings.Contains(sql, "ALL_INDEXES"):
			return &db.QueryResult{
				Columns: []string{"INDEX_NAME", "UNIQUENESS", "STATUS"},
				Rows: [][]any{
					{"PK_BENCH_USERS", "UNIQUE", "VALID"},
					{"IDX_STATUS", "NONUNIQUE", "VALID"},
				},
			}, nil
		case strings.Contains(sql, "DBA_SEGMENTS"):
			return &db.QueryResult{
				Columns: []string{"SEGMENT_NAME", "SEGMENT_TYPE", "BYTES"},
				Rows: [][]any{
					{"BENCH_USERS", "TABLE", int64(125829120)}, // 120 MB
				},
			}, nil
		}
		return &db.QueryResult{}, nil
	}

	r, err := NewTableInfoSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "OPENDB.BENCH_USERS"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	want := []string{
		"Table: OPENDB.BENCH_USERS",
		"Columns (3)",
		"USER_ID", "STATUS", "NAME",
		"Indexes (2)",
		"PK_BENCH_USERS", "IDX_STATUS",
		"--- Segment ---",
		"125829120",
		// summary 字段
		"schema: OPENDB",
		"table: BENCH_USERS",
		"column_count: 3",
		"index_count: 2",
		"segment_total_bytes: 125829120",
	}
	for _, w := range want {
		if !strings.Contains(r.Rendered, w) {
			t.Errorf("Rendered missing %q\nFull:\n%s", w, r.Rendered)
		}
	}
}

func TestTableInfoSkill_DefaultSchemaIsOPENDB(t *testing.T) {
	var schemaCaptured string
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		if strings.Contains(sql, "ALL_TAB_COLUMNS") {
			// 看 OWNER 字段的值
			if i := strings.Index(sql, "OWNER='"); i >= 0 {
				rest := sql[i+len("OWNER='"):]
				if j := strings.Index(rest, "'"); j > 0 {
					schemaCaptured = rest[:j]
				}
			}
		}
		return &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}, nil
	}
	// 不带 schema 前缀
	_, _ = NewTableInfoSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "BENCH_USERS"}))
	if schemaCaptured != "OPENDB" {
		t.Errorf("default schema = %q, want OPENDB", schemaCaptured)
	}
}

func TestTableInfoSkill_QueryError_OnColumns(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		if strings.Contains(sql, "ALL_TAB_COLUMNS") {
			return nil, errors.New("DM-2111: invalid table")
		}
		return &db.QueryResult{}, nil
	}
	_, err := NewTableInfoSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "NONEXIST"}))
	if err == nil {
		t.Fatal("expected error when columns query fails")
	}
}

// 关键回归: tableinfo 的 SQL 必须用 ALL_TAB_COLUMNS / ALL_INDEXES / DBA_SEGMENTS.
// DM 兼容 Oracle 字典视图, 不应回退到 SYS.SYSCOLUMNS 这类 DM 内部表.
func TestTableInfoSkill_UsesOracleCompatViews(t *testing.T) {
	var sqls []string
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		sqls = append(sqls, sql)
		return &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}, nil
	}
	_, _ = NewTableInfoSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "BENCH_A"}))

	hasCols := false
	hasIdx := false
	hasSeg := false
	for _, sql := range sqls {
		if strings.Contains(sql, "ALL_TAB_COLUMNS") {
			hasCols = true
		}
		if strings.Contains(sql, "ALL_INDEXES") {
			hasIdx = true
		}
		if strings.Contains(sql, "DBA_SEGMENTS") {
			hasSeg = true
		}
	}
	if !hasCols {
		t.Error("missing ALL_TAB_COLUMNS query")
	}
	if !hasIdx {
		t.Error("missing ALL_INDEXES query")
	}
	if !hasSeg {
		t.Error("missing DBA_SEGMENTS query")
	}
}
