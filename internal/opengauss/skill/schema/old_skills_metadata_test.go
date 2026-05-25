/*-------------------------------------------------------------------------
 *
 * old_skills_metadata_test.go
 *	  Test cases for old_skills_metadata.go (schema package):
 *	  TestTableInfoSkill_Metadata, TestTableInfoSkill_ValidateEmpty,
 *	  TestIndexAdviseSkill_Metadata.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/schema/old_skills_metadata_test.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"testing"

	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestTableInfoSkill_Metadata(t *testing.T) {
	s := NewTableInfoSkill(mock.NewMockDriver())

	if s.Name() != "tableinfo" {
		t.Errorf("Name() = %q, want 'tableinfo'", s.Name())
	}
	if s.Description() == "" {
		t.Errorf("Description() is empty")
	}
	if s.ToolDef().Name != "tableinfo" {
		t.Errorf("ToolDef().Name = %q, want 'tableinfo'", s.ToolDef().Name)
	}
	if s.ToolDef().Description == "" {
		t.Errorf("ToolDef().Description is empty")
	}
	if s.CLIDef().Usage == "" {
		t.Errorf("CLIDef().Usage is empty")
	}
	if len(s.CLIDef().Examples) == 0 {
		t.Errorf("CLIDef().Examples is empty")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
}

func TestTableInfoSkill_ValidateEmpty(t *testing.T) {
	s := NewTableInfoSkill(mock.NewMockDriver())
	if err := s.Validate(skill.Params{}); err == nil {
		t.Errorf("Validate(empty) expected error, got nil")
	}
}

func TestIndexAdviseSkill_Metadata(t *testing.T) {
	s := NewIndexAdviseSkill(mock.NewMockDriver())

	if s.Name() != "indexadvise" {
		t.Errorf("Name() = %q, want 'indexadvise'", s.Name())
	}
	if s.Description() == "" {
		t.Errorf("Description() is empty")
	}
	if s.ToolDef().Name != "indexadvise" {
		t.Errorf("ToolDef().Name = %q, want 'indexadvise'", s.ToolDef().Name)
	}
	if s.ToolDef().Description == "" {
		t.Errorf("ToolDef().Description is empty")
	}
	if s.CLIDef().Usage == "" {
		t.Errorf("CLIDef().Usage is empty")
	}
	if len(s.CLIDef().Examples) == 0 {
		t.Errorf("CLIDef().Examples is empty")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
}

func TestIndexAdviseSkill_ValidateEmpty(t *testing.T) {
	s := NewIndexAdviseSkill(mock.NewMockDriver())
	if err := s.Validate(skill.Params{}); err == nil {
		t.Errorf("Validate(empty) expected error, got nil")
	}
}
