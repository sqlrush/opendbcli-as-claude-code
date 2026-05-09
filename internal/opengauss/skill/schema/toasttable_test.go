/*-------------------------------------------------------------------------
 *
 * toasttable_test.go
 *	  Test cases for toasttable.go (schema package):
 *	  TestToastTableMetadata.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/schema/toasttable_test.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"testing"

	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestToastTableMetadata(t *testing.T) {
	s := NewToastTableSkill(mock.NewMockDriver())

	if s.Name() != "toasttable" {
		t.Errorf("Name() = %q, want toasttable", s.Name())
	}
	if s.Description() == "" {
		t.Errorf("Description() is empty")
	}
	if s.ToolDef().Name != "toasttable" {
		t.Errorf("ToolDef().Name = %q, want toasttable", s.ToolDef().Name)
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel should be read-only")
	}
	if err := s.Validate(skill.Params{}); err != nil {
		t.Errorf("Validate({}) unexpected error: %v", err)
	}
}
