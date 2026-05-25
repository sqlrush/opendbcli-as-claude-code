/*-------------------------------------------------------------------------
 *
 * config.go
 *	  ConfigSkill displays the current configuration settings.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/config.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/skill"
)

// ConfigSkill displays the current configuration settings.
type ConfigSkill struct {
	cfg *config.Config
}

// NewConfigSkill creates a ConfigSkill that reads from the given config.
func NewConfigSkill(cfg *config.Config) *ConfigSkill {
	return &ConfigSkill{cfg: cfg}
}

func (s *ConfigSkill) Name() string        { return "config" }
func (s *ConfigSkill) Description() string  { return "Show current configuration" }
func (s *ConfigSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ConfigSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "config",
		Description: "Show current configuration",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *ConfigSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "config",
		Aliases:  []string{"cfg"},
		Usage:    "/config",
		Examples: []string{"/config"},
	}
}

func (s *ConfigSkill) Validate(_ skill.Params) error { return nil }

func (s *ConfigSkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	var b strings.Builder
	b.WriteString("Current configuration:\n")
	fmt.Fprintf(&b, "  connections_dir        %s\n", s.cfg.ConnectionsDir)
	fmt.Fprintf(&b, "  security.default_level %d\n", s.cfg.Security.DefaultLevel)
	fmt.Fprintf(&b, "  security.confirm       %v\n", s.cfg.Security.ConfirmOnDangerous)
	fmt.Fprintf(&b, "  output.format          %s\n", s.cfg.Output.Format)
	fmt.Fprintf(&b, "  output.max_rows        %d\n", s.cfg.Output.MaxRows)
	fmt.Fprintf(&b, "  llm.provider           %s\n", s.cfg.LLM.Provider)
	fmt.Fprintf(&b, "  session.restore        %v\n", s.cfg.Session.RestoreOnSwitch)
	fmt.Fprintf(&b, "  session.history_dir    %s\n", s.cfg.Session.HistoryDir)

	return &skill.Result{
		Type: skill.ResultText,
		Data: b.String(),
	}, nil
}
