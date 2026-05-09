/*-------------------------------------------------------------------------
 *
 * logout.go
 *	  LogoutSkill disconnects the current database connection.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/logout.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/skill"
)

// LogoutSkill disconnects the current database connection.
type LogoutSkill struct {
	manager *connection.Manager
}

// NewLogoutSkill creates a LogoutSkill that uses the given connection manager.
func NewLogoutSkill(manager *connection.Manager) *LogoutSkill {
	return &LogoutSkill{manager: manager}
}

func (s *LogoutSkill) Name() string        { return "logout" }
func (s *LogoutSkill) Description() string  { return "Disconnect from current database" }
func (s *LogoutSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *LogoutSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "logout",
		Description: "Disconnect from current database",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *LogoutSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "logout",
		Aliases:  []string{"disconnect"},
		Usage:    "/logout",
		Examples: []string{"/logout"},
	}
}

func (s *LogoutSkill) Validate(_ skill.Params) error { return nil }

func (s *LogoutSkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	currentName := s.manager.CurrentName()

	if err := s.manager.Disconnect(); err != nil {
		return nil, fmt.Errorf("logout failed: %w", err)
	}

	msg := "Disconnected"
	if currentName != "" {
		msg = fmt.Sprintf("Disconnected from %s", currentName)
	}

	return &skill.Result{
		Type:    skill.ResultText,
		Data:    msg,
		Summary: "disconnected",
	}, nil
}
