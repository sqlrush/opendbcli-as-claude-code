/*-------------------------------------------------------------------------
 *
 * dbtop_test.go
 *	  Test cases for dbtop.go (monitor package):
 *	  TestDbtopSkill_Metadata, TestDbtopSkill_ExecuteReturnsRefresh,
 *	  TestDbtopSkill_CustomInterval.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/dbtop_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestDbtopSkill_Metadata(t *testing.T) {
	s := NewDbtopSkill(mock.NewMockDriver())
	if s.Name() != "dbtop" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v", s.SecurityLevel())
	}
}

func TestDbtopSkill_ExecuteReturnsRefresh(t *testing.T) {
	s := NewDbtopSkill(mock.NewMockDriver())
	r, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Type != skill.ResultRefresh {
		t.Errorf("Type = %v, want ResultRefresh", r.Type)
	}
	src, ok := r.Data.(skill.DbtopRefreshSource)
	if !ok {
		t.Fatalf("Data type = %T, want DbtopRefreshSource", r.Data)
	}
	if src.DbtopInterval() != 2 {
		t.Errorf("default interval = %d, want 2", src.DbtopInterval())
	}
}

func TestDbtopSkill_CustomInterval(t *testing.T) {
	s := NewDbtopSkill(mock.NewMockDriver())
	r, _ := s.Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "5"}))
	src := r.Data.(skill.DbtopRefreshSource)
	if src.DbtopInterval() != 5 {
		t.Errorf("custom interval = %d, want 5", src.DbtopInterval())
	}
}

func TestDbtopSkill_InvalidIntervalFallsBack(t *testing.T) {
	s := NewDbtopSkill(mock.NewMockDriver())
	for _, in := range []string{"abc", "-5", "0"} {
		r, _ := s.Execute(context.Background(),
			skill.ParamsFromMap(map[string]any{"args": in}))
		src := r.Data.(skill.DbtopRefreshSource)
		if src.DbtopInterval() != 2 {
			t.Errorf("interval(%q) = %d, want fallback 2", in, src.DbtopInterval())
		}
	}
}

// truncate helper: dbtop 用来限制 SQL_TEXT 显示宽度.
func TestDbtopTruncate(t *testing.T) {
	tests := []struct {
		s, want string
		n       int
	}{
		{"hello", "hello", 10},   // 不截 (5 <= 10)
		{"hello", "hello", 5},    // 边界 (5 == 5, 不截)
		{"hello world", "hello", 5}, // 截到 5
		{"", "", 5},              // 空串
		{"abc", "ab", 2},         // 截到 2
	}
	for _, tt := range tests {
		got := truncate(tt.s, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

// RenderFrame 错误分支: 任何 query 报错时, 应该 inline 错误信息而不是 panic.
func TestDbtopRenderFrame_QueryErrors(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return nil, errors.New("DM-2007: connection refused")
	}
	r, _ := NewDbtopSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	src := r.Data.(skill.DbtopRefreshSource)
	loop := src.NewDbtopLoop()
	frame := loop.RenderFrame(context.Background(), 80, 1)
	full := strings.Join(frame, "\n")
	// 错误必须 inline 显示, 不能 panic
	if !strings.Contains(full, "error") && !strings.Contains(full, "DM-2007") {
		t.Errorf("RenderFrame should inline query errors. Got:\n%s", full)
	}
}

// RenderFrame: 测渲染输出含关键字段.
func TestDbtopSkill_RenderFrame(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		switch {
		case strings.Contains(sql, "V$INSTANCE"):
			return &db.QueryResult{
				Columns: []string{"NAME", "INSTANCE_NAME", "HOST_NAME", "STATUS$", "START_TIME"},
				Rows:    [][]any{{"DAMENG", "DM01", "host1", "OPEN", "2026-05-02"}},
			}, nil
		case strings.Contains(sql, "FROM DUAL"):
			return &db.QueryResult{
				Columns: []string{"TOTAL", "ACTIVE", "IDLE", "BLOCKED"},
				Rows:    [][]any{{int64(50), int64(15), int64(35), int64(0)}},
			}, nil
		case strings.Contains(sql, "STATE = 'ACTIVE'"):
			return &db.QueryResult{
				Columns: []string{"SESS_ID", "USER_NAME", "SQL_HEAD", "ELAPSED_SEC"},
				Rows: [][]any{
					{int64(140304100), "OPENDB", "SELECT COUNT(*) FROM bench", int64(120)},
				},
			}, nil
		case strings.Contains(sql, "V$LONG_EXEC_SQLS"):
			return &db.QueryResult{
				Columns: []string{"SESS_ID", "EXEC_TIME", "SQL_HEAD"},
				Rows: [][]any{
					{int64(140304100), int64(120), "SELECT COUNT(*) FROM bench"},
				},
			}, nil
		}
		return &db.QueryResult{}, nil
	}

	r, _ := NewDbtopSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	src := r.Data.(skill.DbtopRefreshSource)
	loop := src.NewDbtopLoop()

	frame := loop.RenderFrame(context.Background(), 80, 2)
	full := strings.Join(frame, "\n")

	for _, want := range []string{
		"DM dbtop",
		"Instance: DAMENG",
		"host1",
		"total=50",
		"active=15",
		"blocked=0",
		"Active Sessions",
		"OPENDB",
		"Long-running SQLs",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("RenderFrame missing %q\nFull:\n%s", want, full)
		}
	}
}
