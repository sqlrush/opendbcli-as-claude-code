/*-------------------------------------------------------------------------
 *
 * login.go
 *	  LoginSkill connects to a database or lists available connections.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/login.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/skill"
)

// PickerAction is the signal returned when /login is invoked with no args,
// telling the REPL to open an interactive connection picker.
const PickerAction = "connection_picker"

// LoginSkill connects to a database or lists available connections.
type LoginSkill struct {
	manager *connection.Manager
}

// NewLoginSkill creates a LoginSkill that uses the given connection manager.
func NewLoginSkill(manager *connection.Manager) *LoginSkill {
	return &LoginSkill{manager: manager}
}

func (s *LoginSkill) Name() string        { return "login" }
func (s *LoginSkill) Description() string  { return "Connect to a database" }
func (s *LoginSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *LoginSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "login",
		Description: "Connect to a database",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Connection name to connect to",
				},
			},
		},
	}
}

func (s *LoginSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "login",
		Aliases:        []string{"connect"},
		Usage:          "/login",
		Examples:       []string{"/login"},
		ArgCompletions: s.manager.ConnectionNames(),
	}
}

func (s *LoginSkill) Validate(_ skill.Params) error { return nil }

func (s *LoginSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := params.StringOr("args", "")
	if args == "" {
		// Signal the REPL to open the interactive connection picker.
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     "picker",
			Rendered: "",
			Summary:  "picker",
			Metadata: map[string]string{"action": PickerAction},
		}, nil
	}

	// Parse: /login <connName> [user password] or /login <connName> [password]
	connName, user, password := parseLoginArgs(args)
	return s.connect(ctx, connName, user, password)
}

// parseLoginArgs splits args into connection name, optional user, and optional password.
// Formats: /login <conn>  |  /login <conn> <password>  |  /login <conn> <user> <password>
func parseLoginArgs(args string) (connName, user, password string) {
	parts := strings.Fields(strings.TrimSpace(args))
	switch len(parts) {
	case 1:
		connName = parts[0]
	case 2:
		connName = parts[0]
		password = parts[1]
	default: // 3+
		connName = parts[0]
		user = parts[1]
		password = parts[2]
	}
	return
}

// connect establishes a connection using the connection manager.
func (s *LoginSkill) connect(ctx context.Context, connName, user, password string) (*skill.Result, error) {
	if err := s.manager.ConnectWithOverride(ctx, connName, user, password); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     fmt.Sprintf("Connected to %s", connName),
		Rendered: fmt.Sprintf("Connected to %s", connName),
		Summary:  connName,
	}, nil
}
