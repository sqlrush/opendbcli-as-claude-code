/*-------------------------------------------------------------------------
 *
 * explain_test.go
 *	  Test cases for explain.go (query package):
 *	  TestExplainSkill_Metadata, TestExplainSkill_Validate,
 *	  TestExplainSkill_Execute_PrependsExplain.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/query/explain_test.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestExplainSkill_Metadata(t *testing.T) {
	s := NewExplainSkill(mock.NewMockDriver())
	if s.Name() != "explain" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.CLIDef().Command != "explain" {
		t.Errorf("CLIDef().Command = %q", s.CLIDef().Command)
	}
}

func TestExplainSkill_Validate(t *testing.T) {
	s := NewExplainSkill(mock.NewMockDriver())

	// 无参数应该报错
	if err := s.Validate(skill.ParamsFromMap(nil)); err == nil {
		t.Error("Validate(empty) should error")
	}
	// args 空白也应该报错
	if err := s.Validate(skill.ParamsFromMap(map[string]any{"args": "  "})); err == nil {
		t.Error("Validate(whitespace) should error")
	}
	// 有 args 应该通过
	if err := s.Validate(skill.ParamsFromMap(map[string]any{"args": "SELECT 1"})); err != nil {
		t.Errorf("Validate(SELECT 1) = %v, want nil", err)
	}
	// sql 字段也支持 (LLM 用 ToolDef parameters)
	if err := s.Validate(skill.ParamsFromMap(map[string]any{"sql": "SELECT 1"})); err != nil {
		t.Errorf("Validate(sql=SELECT 1) = %v, want nil", err)
	}
}

func TestExplainSkill_Execute_PrependsExplain(t *testing.T) {
	var captured string
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = sql
		return &db.QueryResult{
			Columns: []string{"PLAN"},
			Rows: [][]any{
				{"#NSET2: [1, 100, 24]"},
				{"  #PRJT2: [1, 100, 24]"},
				{"    #CSCN2: [1, 100, 24]; INDEX33555476(BENCH_USERS)"},
			},
		}, nil
	}
	r, err := NewExplainSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "SELECT * FROM bench_users WHERE status=3"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// 关键: 必须在用户 SQL 前面加 "EXPLAIN "
	if !strings.HasPrefix(captured, "EXPLAIN ") {
		t.Errorf("query SQL must start with 'EXPLAIN '. Got: %q", captured)
	}
	// 输出必须含 explain_lines + format_note (LLM 解读三元组用)
	want := []string{"explain_lines: 3", "format_note: 每行末尾"}
	for _, w := range want {
		if !strings.Contains(r.Rendered, w) {
			t.Errorf("Rendered missing %q\n%s", w, r.Rendered)
		}
	}
}

func TestExplainSkill_Execute_QueryError(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return nil, errors.New("DM-2007: syntax error near 'WHRE'")
	}
	_, err := NewExplainSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "SELECT * WHRE foo"}))
	if err == nil {
		t.Fatal("expected error from invalid SQL")
	}
}

func TestExplainSkill_AcceptsBothArgsAndSqlField(t *testing.T) {
	drv := mock.NewMockDriver()
	captured := []string{}
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = append(captured, sql)
		return &db.QueryResult{Columns: []string{"PLAN"}, Rows: [][]any{}}, nil
	}
	s := NewExplainSkill(drv)
	// 通过 args (CLI 路径)
	_, _ = s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "SELECT 1"}))
	// 通过 sql (LLM tool call 路径)
	_, _ = s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"sql": "SELECT 2"}))

	if len(captured) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(captured))
	}
	if !strings.Contains(captured[0], "SELECT 1") {
		t.Errorf("first query missing 'SELECT 1': %q", captured[0])
	}
	if !strings.Contains(captured[1], "SELECT 2") {
		t.Errorf("second query missing 'SELECT 2': %q", captured[1])
	}
}
