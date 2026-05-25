/*-------------------------------------------------------------------------
 *
 * history.go
 *	  HistorySkill displays session command history.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/history.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
)

// HistorySkill displays session command history.
type HistorySkill struct {
	history *session.SessionHistory
}

// NewHistorySkill creates a HistorySkill that reads from the given history.
func NewHistorySkill(history *session.SessionHistory) *HistorySkill {
	return &HistorySkill{history: history}
}

func (s *HistorySkill) Name() string        { return "history" }
func (s *HistorySkill) Description() string  { return "Show command history" }
func (s *HistorySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *HistorySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "history",
		Description: "Show command history",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *HistorySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "history",
		Aliases:  []string{"hist"},
		Usage:    "/history",
		Examples: []string{"/history"},
	}
}

func (s *HistorySkill) Validate(_ skill.Params) error { return nil }

func (s *HistorySkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	entries := s.history.Entries()
	if len(entries) == 0 {
		return &skill.Result{
			Type: skill.ResultText,
			Data: "No history entries",
		}, nil
	}

	var b strings.Builder
	b.WriteString("Session history:\n")
	for i, e := range entries {
		ts := e.Timestamp.Format("15:04:05")
		fmt.Fprintf(&b, "  %3d  [%s]  %s\n", i+1, ts, e.Command)
	}

	return &skill.Result{
		Type:    skill.ResultText,
		Data:    b.String(),
		Summary: fmt.Sprintf("%d entries", len(entries)),
	}, nil
}
