/*-------------------------------------------------------------------------
 *
 * cancel.go
 *	  CancelSkill cancels a running query using pg_cancel_backend
 *	  (lighter than kill/terminate).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/admin/cancel.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// CancelSkill cancels a running query using pg_cancel_backend (lighter than kill/terminate).
type CancelSkill struct{ driver db.Driver }

// NewCancelSkill creates a CancelSkill backed by the given driver.
func NewCancelSkill(driver db.Driver) *CancelSkill { return &CancelSkill{driver: driver} }

func (s *CancelSkill) Name() string                       { return "cancel" }
func (s *CancelSkill) Description() string                { return "取消查询 (pg_cancel_backend)" }
func (s *CancelSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelOperator }
func (s *CancelSkill) Validate(_ skill.Params) error      { return nil }

func (s *CancelSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/cancel <pid>",
		Examples: []string{"/cancel 12345"},
	}
}

func (s *CancelSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "cancel",
		Description: "Cancel a running query using pg_cancel_backend() (lighter than pg_terminate_backend)",
		Parameters: map[string]any{
			"pid": map[string]any{"type": "string", "description": "Backend PID to cancel"},
		},
	}
}

func (s *CancelSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	pid := strings.TrimSpace(params.StringOr("args", ""))
	if pid == "" {
		pid = params.StringOr("pid", "")
	}
	if pid == "" {
		return &skill.Result{Type: skill.ResultError, Summary: "usage: /cancel <pid>"}, nil
	}

	sqlStr := fmt.Sprintf("SELECT pg_cancel_backend(%s)", pid)
	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	cancelled := "true"
	if len(result.Rows) > 0 && len(result.Rows[0]) > 0 {
		cancelled = asStr(result.Rows[0][0])
	}

	if cancelled == "true" {
		msg := fmt.Sprintf("查询已取消 (pid=%s)", pid)
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "\033[1;32m✓\033[0m " + msg,
			Summary:  msg,
		}, nil
	}
	msg := fmt.Sprintf("取消请求已发送但未成功 (pid=%s), 会话可能已结束", pid)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: "\033[33m⚠\033[0m " + msg,
		Summary:  msg,
	}, nil
}
