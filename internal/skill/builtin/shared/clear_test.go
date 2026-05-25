/*-------------------------------------------------------------------------
 *
 * clear_test.go
 *	  Test cases for clear.go (shared package): TestClearSkill_Execute,
 *	  TestClearSkill_Metadata.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/clear_test.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestClearSkill_Execute(t *testing.T) {
	tests := []struct {
		name     string
		wantData string
	}{
		{
			name:     "returns clear sentinel",
			wantData: "__clear__",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClearSkill()
			result, err := c.Execute(context.Background(), skill.ParamsFromMap(nil))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Type != skill.ResultText {
				t.Errorf("want ResultText, got %v", result.Type)
			}
			data, ok := result.Data.(string)
			if !ok {
				t.Fatalf("Data is not a string: %T", result.Data)
			}
			if data != tt.wantData {
				t.Errorf("Data = %q, want %q", data, tt.wantData)
			}
		})
	}
}

func TestClearSkill_Metadata(t *testing.T) {
	c := NewClearSkill()

	if c.Name() != "clear" {
		t.Errorf("Name() = %q, want %q", c.Name(), "clear")
	}
	if c.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", c.SecurityLevel())
	}
	if err := c.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
