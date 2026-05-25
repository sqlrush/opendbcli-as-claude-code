/*-------------------------------------------------------------------------
 *
 * session_mgmt.go
 *	  SessionSkill manages conversation sessions for /llm. Primary use
 *	  is `/session new` — clears the current instance's session
 *	  history so the next /llm invocation starts fresh, free of
 *	  prior-topic context.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/session_mgmt.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/skill"
)

// SessionSkill manages conversation sessions for /llm. Primary use is
// `/session new` — clears the current instance's session history so the
// next /llm invocation starts fresh, free of prior-topic context.
//
// Background: v1.1.08 made /llm sessions connection-scoped (24h resume),
// which is great for continuous work but causes topic drift when the user
// switches topics. `/session new` is the manual override.
type SessionSkill struct {
	sessionsBaseDir string // e.g. ~/.opendb/sessions
	connMgr         *connection.Manager
}

// NewSessionSkill creates a SessionSkill. sessionsBaseDir is the root
// directory containing per-instance session subdirs (one level up from
// the actual jsonl files).
func NewSessionSkill(sessionsBaseDir string, connMgr *connection.Manager) *SessionSkill {
	return &SessionSkill{sessionsBaseDir: sessionsBaseDir, connMgr: connMgr}
}

func (s *SessionSkill) Name() string                       { return "session" }
func (s *SessionSkill) Description() string                { return "管理 /llm 对话会话 (new 开新话题)" }
func (s *SessionSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SessionSkill) Validate(_ skill.Params) error      { return nil }

func (s *SessionSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "session",
		Usage:          "/session new | list",
		ArgCompletions: []string{"new", "list"},
		Examples:       []string{"/session new", "/session list"},
	}
}

func (s *SessionSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "session",
		Description: "Manage /llm conversation sessions (subcommands: new = clear for fresh topic, list = show current files)",
		Parameters: map[string]any{
			"args": map[string]any{
				"type":        "string",
				"description": "subcommand: new | list",
			},
		},
	}
}

func (s *SessionSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	action := args
	if action == "" {
		action = "list"
	}

	instance := ""
	if s.connMgr != nil {
		instance = s.connMgr.CurrentName()
	}
	if instance == "" {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "未连接到任何实例（先 /login）",
			Summary:  "no active connection",
		}, nil
	}

	instDir := filepath.Join(s.sessionsBaseDir, instance)

	switch action {
	case "new":
		// Delete all jsonl files for the instance so ResumeOrNew finds
		// nothing and mints a fresh SessionID on the next /llm.
		entries, err := os.ReadDir(instDir)
		if err != nil {
			if os.IsNotExist(err) {
				return &skill.Result{
					Type:     skill.ResultText,
					Rendered: "当前实例无会话历史，下次 /llm 会新建 session",
					Summary:  "no history",
				}, nil
			}
			return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
		}
		cleared := 0
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".jsonl") {
				if err := os.Remove(filepath.Join(instDir, e.Name())); err == nil {
					cleared++
				}
			}
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("已清除 %s 实例的 %d 个会话文件，下次 /llm 会新建 session", instance, cleared),
			Summary:  fmt.Sprintf("cleared %d sessions", cleared),
		}, nil

	case "list":
		entries, err := os.ReadDir(instDir)
		if err != nil {
			if os.IsNotExist(err) {
				return &skill.Result{
					Type:     skill.ResultText,
					Rendered: "当前实例无会话历史",
					Summary:  "empty",
				}, nil
			}
			return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("实例 %s 的会话历史：", instance))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".jsonl") {
				if info, err := e.Info(); err == nil {
					lines = append(lines, fmt.Sprintf("  %s  (%d bytes, %s)",
						e.Name(), info.Size(), info.ModTime().Format("2006-01-02 15:04")))
				}
			}
		}
		if len(lines) == 1 {
			lines = append(lines, "  (empty)")
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: strings.Join(lines, "\n"),
			Summary:  fmt.Sprintf("%d sessions", len(lines)-1),
		}, nil

	default:
		return &skill.Result{
			Type:     skill.ResultError,
			Summary:  fmt.Sprintf("unknown action %q, use: new | list", action),
			Rendered: "用法: /session new | list",
		}, nil
	}
}
