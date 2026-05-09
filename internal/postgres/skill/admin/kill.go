/*-------------------------------------------------------------------------
 *
 * kill.go
 *	  kill — KillSkill plus helpers (NewKillSkill) used by the admin
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/admin/kill.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const pgSessionDetailSQL = `SELECT pid, usename, client_addr, datname, state,
       EXTRACT(EPOCH FROM (now() - backend_start))::int AS uptime_sec,
       wait_event_type, wait_event,
       LEFT(query, 80) AS query_text
FROM pg_stat_activity WHERE pid = %s`

type KillSkill struct{ driver db.Driver }

func NewKillSkill(driver db.Driver) *KillSkill { return &KillSkill{driver: driver} }

func (s *KillSkill) Name() string                      { return "kill" }
func (s *KillSkill) Description() string                { return "终止会话或取消查询" }
func (s *KillSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelOperator }

func (s *KillSkill) Validate(_ skill.Params) error { return nil }

func (s *KillSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/kill <pid> [cancel] [confirm]",
		Examples: []string{"/kill 12345", "/kill 12345 cancel", "/kill 12345 confirm"},
	}
}

func (s *KillSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "kill",
		Description: "Terminate a PostgreSQL backend or cancel its current query",
		Parameters: map[string]any{
			"pid": map[string]any{"type": "string", "description": "Backend PID"},
		},
	}
}

func (s *KillSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	raw := strings.TrimSpace(params.StringOr("args", ""))
	if raw == "" {
		raw = params.StringOr("pid", "")
	}

	pid, mode, confirm := parsePGKillArgs(raw)
	if pid == "" {
		return &skill.Result{Type: skill.ResultError, Summary: "usage: /kill <pid> [cancel] [confirm]"}, nil
	}

	// Without confirm: show session info panel.
	if !confirm {
		return s.showConfirmation(ctx, pid, mode)
	}

	// With confirm: execute terminate or cancel.
	var sqlStr, action string
	if mode == "cancel" {
		sqlStr = fmt.Sprintf("SELECT pg_cancel_backend(%s)", pid)
		action = "查询已取消"
	} else {
		sqlStr = fmt.Sprintf("SELECT pg_terminate_backend(%s)", pid)
		action = "会话已终止"
	}

	if _, err := s.driver.Query(ctx, sqlStr); err != nil {
		msg := fmt.Sprintf("终止进程失败: %v", err)
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "\033[33m⚠\033[0m " + msg,
			Summary:  msg,
		}, nil
	}

	rendered := fmt.Sprintf("\033[1;32m✓\033[0m %s (pid=%s)", action, pid)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%s (pid=%s)", action, pid),
	}, nil
}

func (s *KillSkill) showConfirmation(ctx context.Context, pid, mode string) (*skill.Result, error) {
	const (
		ansiYellow = "\033[33m"
		ansiReset  = "\033[0m"
		ansiDim    = "\033[90m"
	)

	query := fmt.Sprintf(pgSessionDetailSQL, pid)
	detail, err := s.driver.Query(ctx, query)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: fmt.Sprintf("查询会话信息: %v", err)}, nil
	}
	if len(detail.Rows) == 0 {
		msg := fmt.Sprintf("会话 PID %s 不存在", pid)
		return &skill.Result{Type: skill.ResultText, Rendered: "\033[33m⚠\033[0m " + msg, Summary: msg}, nil
	}

	row := detail.Rows[0]
	user := pgValOrDash(row, 1)
	clientAddr := pgValOrDash(row, 2)
	dbName := pgValOrDash(row, 3)
	state := pgValOrDash(row, 4)
	uptimeSec := pgValOrDash(row, 5)
	waitType := pgValOrDash(row, 6)
	waitEvent := pgValOrDash(row, 7)
	queryText := pgValOrDash(row, 8)

	waitStr := waitEvent
	if waitType != "-" && waitEvent != "-" {
		waitStr = waitType + ": " + waitEvent
	}

	infoLines := []string{
		fmt.Sprintf("PID: %s    用户: %s", pid, user),
		fmt.Sprintf("客户端: %s    数据库: %s", clientAddr, dbName),
		fmt.Sprintf("状态: %s    运行: %s s", state, uptimeSec),
		fmt.Sprintf("等待: %s", waitStr),
	}
	if queryText != "-" {
		infoLines = append(infoLines, fmt.Sprintf("SQL:  %s", queryText))
	}

	action := "终止会话"
	if mode == "cancel" {
		action = "取消当前查询"
	}

	warnLines := []string{
		ansiYellow + "⚠ 此操作将" + action + ansiReset,
		"",
	}
	if mode == "cancel" {
		warnLines = append(warnLines,
			ansiDim+"/kill "+pid+" cancel confirm      取消当前查询 (pg_cancel_backend)"+ansiReset,
		)
	} else {
		warnLines = append(warnLines,
			ansiDim+"/kill "+pid+" confirm              终止会话 (pg_terminate_backend)"+ansiReset,
			ansiDim+"/kill "+pid+" cancel confirm       仅取消当前查询 (pg_cancel_backend)"+ansiReset,
		)
	}

	sections := []format.PanelSection{
		{Lines: infoLines},
		{Lines: warnLines},
	}

	rendered := format.Panel(fmt.Sprintf("Kill Session (PID %s)", pid), sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("PID %s (%s) 待确认", pid, user),
	}, nil
}

// parsePGKillArgs extracts pid, mode (cancel), and confirm flag from args.
func parsePGKillArgs(args string) (pid, mode string, confirm bool) {
	tokens := strings.Fields(args)
	if len(tokens) == 0 {
		return "", "", false
	}
	pid = tokens[0]
	for _, t := range tokens[1:] {
		switch strings.ToLower(t) {
		case "cancel":
			mode = "cancel"
		case "confirm", "yes", "y":
			confirm = true
		}
	}
	return pid, mode, confirm
}

// pgValOrDash returns the string value at index i, or "-" if nil/empty.
func pgValOrDash(row []any, i int) string {
	if i >= len(row) || row[i] == nil {
		return "-"
	}
	v := fmt.Sprintf("%v", row[i])
	if v == "" || v == "<nil>" {
		return "-"
	}
	return v
}
