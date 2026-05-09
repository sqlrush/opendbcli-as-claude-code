/*-------------------------------------------------------------------------
 *
 * config_test.go
 *	  Test cases for config.go (shared package):
 *	  TestConfigSkill_Execute, TestConfigSkill_Metadata.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/config_test.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestConfigSkill_Execute(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		wantSub []string
	}{
		{
			name: "default config",
			cfg: func() *config.Config {
				c := config.Default()
				return &c
			}(),
			wantSub: []string{
				"connections_dir",
				"output.format",
				"terminal",
				"output.max_rows",
				"1000",
				"llm.provider",
				"none",
			},
		},
		{
			name: "custom config",
			cfg: &config.Config{
				ConnectionsDir: "/custom/connections",
				Output: config.OutputConfig{
					Format:  "json",
					MaxRows: 500,
				},
				LLM: config.LLMConfig{
					Provider: "anthropic",
				},
			},
			wantSub: []string{
				"/custom/connections",
				"json",
				"500",
				"anthropic",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewConfigSkill(tt.cfg)
			result, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
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
			for _, sub := range tt.wantSub {
				if !strings.Contains(data, sub) {
					t.Errorf("output does not contain %q:\n%s", sub, data)
				}
			}
		})
	}
}

func TestConfigSkill_Metadata(t *testing.T) {
	cfg := config.Default()
	s := NewConfigSkill(&cfg)

	if s.Name() != "config" {
		t.Errorf("Name() = %q, want %q", s.Name(), "config")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
