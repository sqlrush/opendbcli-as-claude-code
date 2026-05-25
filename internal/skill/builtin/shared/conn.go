/*-------------------------------------------------------------------------
 *
 * conn.go
 *	  ConnSkill handles ad-hoc connections and the connection creation
 *	  wizard.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/conn.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/brand"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// ConnSkill handles ad-hoc connections and the connection creation wizard.
type ConnSkill struct {
	manager *connection.Manager
}

// NewConnSkill creates a ConnSkill that uses the given connection manager.
func NewConnSkill(manager *connection.Manager) *ConnSkill {
	return &ConnSkill{manager: manager}
}

func (s *ConnSkill) Name() string        { return "conn" }
func (s *ConnSkill) Description() string  { return "Create connection or connect directly" }
func (s *ConnSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ConnSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "conn",
		Description: "Create connection or connect directly with connection string",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Connection string: user/pass@host:port/service [as sysdba]",
				},
			},
		},
	}
}

func (s *ConnSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "conn",
		Usage:   "/conn [user/pass@host:port/service] [as sysdba]",
		Examples: []string{
			"/conn",
			"/conn admin/secret@10.0.1.1:1521/orcl",
			"/conn / as sysdba",
			"/conn sys/pass@localhost/orcl as sysdba",
		},
	}
}

func (s *ConnSkill) Validate(_ skill.Params) error { return nil }

func (s *ConnSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	// No args → show current connection info + how to add a new one.
	// (The in-REPL wizard was removed — adding connections goes through
	// `<binary> configure` so there's one place to manage state.)
	if args == "" {
		var b strings.Builder
		if info := s.manager.CurrentInfo(); info != nil {
			name := s.manager.CurrentName()
			b.WriteString(fmt.Sprintf("  连接: %s — %s (%s)\n", name, info.InstanceName, info.Version))
		} else {
			b.WriteString("  当前未连接任何数据库\n")
		}
		b.WriteString(fmt.Sprintf("\n  添加/编辑连接: %s configure (在另一个 shell 中运行)\n", brand.Current().BinaryName))
		b.WriteString("  连接已保存的: /login <name>\n")
		b.WriteString("  临时连接: /conn user/pass@host:port/service")
		text := b.String()
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     text,
			Rendered: text,
			Summary:  "",
		}, nil
	}

	// Parse connection string.
	parsed, err := connection.ParseConnString(args)
	if err != nil {
		return nil, fmt.Errorf("invalid connection string: %w", err)
	}

	// Build ConnectionConfig.
	cfg := db.ConnectionConfig{
		DBType:    "oracle",
		Host:      parsed.Host,
		Port:      parsed.Port,
		Service:   parsed.Service,
		User:      parsed.User,
		Password:  parsed.Password,
		Privilege: parsed.Privilege,
		Options:   make(map[string]string),
	}

	if parsed.IsOSAuth {
		cfg.Options["AUTH TYPE"] = "OS"
		cfg.User = ""
		cfg.Password = ""
	}

	// If no password provided and not OS auth, prompt.
	if !parsed.HasPassword && !parsed.IsOSAuth {
		// Resolve via prompt provider.
		promptResult, err := s.manager.PromptPassword(parsed.User + "@" + parsed.Host)
		if err != nil {
			return nil, fmt.Errorf("password prompt: %w", err)
		}
		cfg.Password = promptResult
	}

	displayName := fmt.Sprintf("%s@%s:%d/%s", cfg.User, cfg.Host, cfg.Port, cfg.Service)
	if parsed.IsOSAuth {
		displayName = fmt.Sprintf("/@%s:%d/%s", cfg.Host, cfg.Port, cfg.Service)
	}

	if err := s.manager.ConnectDirect(ctx, cfg, displayName); err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     fmt.Sprintf("Connected to %s", displayName),
		Rendered: fmt.Sprintf("Connected to %s", displayName),
		Summary:  displayName,
	}, nil
}
