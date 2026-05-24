/*-------------------------------------------------------------------------
 *
 * new_skills_test.go
 *	  Test cases for new_skills.go (query package):
 *	  TestNewQuerySkillsMetadata, TestPlanHistoryRequiresQueryID,
 *	  TestWDRWithNoArgs.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/new_skills_test.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestNewQuerySkillsMetadata(t *testing.T) {
	drv := mock.NewMockDriver()
	cases := []struct {
		name  string
		skill skill.Skill
		key   string
	}{
		{"wdr", NewWDRSkill(drv), "wdr"},
		{"planhistory", NewPlanHistorySkill(drv), "planhistory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.skill.Name() != c.key {
				t.Errorf("Name() = %q, want %q", c.skill.Name(), c.key)
			}
			if c.skill.Description() == "" {
				t.Errorf("Description() is empty")
			}
			if c.skill.ToolDef().Name != c.key {
				t.Errorf("ToolDef().Name = %q, want %q", c.skill.ToolDef().Name, c.key)
			}
			if c.skill.SecurityLevel() != skill.LevelReadOnly {
				t.Errorf("SecurityLevel should be read-only")
			}
			if err := c.skill.Validate(skill.Params{}); err != nil {
				t.Errorf("Validate({}) unexpected error: %v", err)
			}
		})
	}
}

// /planhistory requires a unique_query_id argument; calling it with no args
// must produce a clear usage error rather than hang.
func TestPlanHistoryRequiresQueryID(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewPlanHistorySkill(drv)
	// This also exercises the arg parsing path.
	res, err := s.Execute(nil, skill.Params{})
	if err != nil {
		t.Fatalf("Execute unexpected Go error: %v", err)
	}
	if res.Type != skill.ResultError {
		t.Errorf("expected ResultError for missing query_id, got %v", res.Type)
	}
}

// /wdr with no args should still execute — it lists recent snapshots.
// Here we're only checking it doesn't panic on nil driver return.
func TestWDRWithNoArgs(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewWDRSkill(drv)
	_, err := s.Execute(nil, skill.Params{})
	if err != nil {
		t.Fatalf("Execute unexpected Go error: %v", err)
	}
}

func TestWDRRenderedIncludesSnapshotTable(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
		return &db.QueryResult{
			Columns: []string{"snapshot_id", "start_ts", "end_ts", "window_sec"},
			Rows: [][]any{
				{78, "2026-05-23 10:00:00", "2026-05-23 10:30:00", 1800},
			},
		}, nil
	}
	s := NewWDRSkill(drv)
	res, err := s.Execute(context.Background(), skill.Params{})
	if err != nil {
		t.Fatalf("Execute unexpected Go error: %v", err)
	}
	for _, want := range []string{"WDR 快照", "snapshot_id", "78", "/wdranalyze <begin_snap> <end_snap>"} {
		if !strings.Contains(res.Rendered, want) {
			t.Fatalf("Rendered output missing %q:\n%s", want, res.Rendered)
		}
	}
}
