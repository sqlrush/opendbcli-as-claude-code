/*-------------------------------------------------------------------------
 *
 * old_skills_metadata_test.go
 *	  Test cases for old_skills_metadata.go (query package):
 *	  TestOldQuerySkillsMetadata, TestOldQuerySkillsValidate.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/old_skills_metadata_test.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"testing"

	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

// TestOldQuerySkillsMetadata validates the metadata contract (Name / Description /
// ToolDef / CLIDef / SecurityLevel / Validate) for the "old" OpenGauss query
// skills: /topsql, /slowsql, /sql, /explain, /ash, /ogerr.
//
// These skills predate the `new_skills_test.go` batch (wdr, planhistory); this
// file backfills them without touching production code.
func TestOldQuerySkillsMetadata(t *testing.T) {
	drv := mock.NewMockDriver()

	cases := []struct {
		name  string
		skill skill.Skill
		key   string
	}{
		{"topsql", NewTopSQLSkill(drv), "topsql"},
		{"slowsql", NewSlowSQLSkill(drv), "slowsql"},
		{"sql", NewSQLSkill(drv), "sql"},
		{"explain", NewExplainSkill(drv), "explain"},
		{"ash", NewASHSkill(drv), "ash"},
		// /ogerr uses an in-memory knowledge base, no driver needed.
		{"ogerr", NewOGErrSkill(), "ogerr"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.skill.Name(); got != c.key {
				t.Errorf("Name() = %q, want %q", got, c.key)
			}
			if c.skill.Description() == "" {
				t.Errorf("Description() is empty")
			}

			tool := c.skill.ToolDef()
			if tool.Name != c.key {
				t.Errorf("ToolDef().Name = %q, want %q", tool.Name, c.key)
			}
			if tool.Description == "" {
				t.Errorf("ToolDef().Description is empty")
			}

			cli := c.skill.CLIDef()
			if cli.Usage == "" {
				t.Errorf("CLIDef().Usage is empty")
			}

			if c.skill.SecurityLevel() != skill.LevelReadOnly {
				t.Errorf("SecurityLevel() should be LevelReadOnly, got %v", c.skill.SecurityLevel())
			}
		})
	}
}

// TestOldQuerySkillsValidate exercises Validate() behavior.
//
// Most old skills accept empty params (they either tolerate missing input or
// defer the check to Execute). /explain is the exception: it requires a SQL
// statement and must reject empty input at Validate time.
func TestOldQuerySkillsValidate(t *testing.T) {
	drv := mock.NewMockDriver()

	t.Run("topsql_allows_empty", func(t *testing.T) {
		if err := NewTopSQLSkill(drv).Validate(skill.Params{}); err != nil {
			t.Errorf("Validate({}) unexpected error: %v", err)
		}
	})

	t.Run("slowsql_allows_empty", func(t *testing.T) {
		if err := NewSlowSQLSkill(drv).Validate(skill.Params{}); err != nil {
			t.Errorf("Validate({}) unexpected error: %v", err)
		}
	})

	t.Run("sql_allows_empty", func(t *testing.T) {
		if err := NewSQLSkill(drv).Validate(skill.Params{}); err != nil {
			t.Errorf("Validate({}) unexpected error: %v", err)
		}
	})

	t.Run("ash_allows_empty", func(t *testing.T) {
		if err := NewASHSkill(drv).Validate(skill.Params{}); err != nil {
			t.Errorf("Validate({}) unexpected error: %v", err)
		}
	})

	t.Run("ogerr_allows_empty", func(t *testing.T) {
		if err := NewOGErrSkill().Validate(skill.Params{}); err != nil {
			t.Errorf("Validate({}) unexpected error: %v", err)
		}
	})

	t.Run("explain_rejects_empty", func(t *testing.T) {
		if err := NewExplainSkill(drv).Validate(skill.Params{}); err == nil {
			t.Errorf("Validate({}) should reject empty SQL for /explain")
		}
	})

	t.Run("explain_accepts_sql", func(t *testing.T) {
		p := skill.ParamsFromMap(map[string]any{"args": "SELECT 1"})
		if err := NewExplainSkill(drv).Validate(p); err != nil {
			t.Errorf("Validate(SELECT 1) unexpected error: %v", err)
		}
	})
}
