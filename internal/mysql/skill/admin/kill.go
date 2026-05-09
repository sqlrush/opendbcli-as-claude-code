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
 *	  internal/mysql/skill/admin/kill.go
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

const mysqlProcessDetailSQL = `SELECT id, user, host, db, command, time, state,
       LEFT(info, 80) AS sql_text
FROM information_schema.processlist WHERE id = %s`

type KillSkill struct {
	driver db.Driver
}

func NewKillSkill(driver db.Driver) *KillSkill {
	return &KillSkill{driver: driver}
}

func (s *KillSkill) Name() string        { return "kill" }
func (s *KillSkill) Description() string { return "终止连接或查询" }
func (s *KillSkill) Category() string    { return "admin" }
func (s *KillSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelOperator
}

func (s *KillSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/kill <id> [query] [confirm]",
		Examples: []string{"/kill 142", "/kill 142 query", "/kill 142 confirm"},
	}
}

func (s *KillSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "kill",
		Description: "Kill a MySQL connection or cancel its current query",
		Parameters: map[string]any{
			"id":   map[string]any{"type": "string", "description": "Process ID to kill"},
			"mode": map[string]any{"type": "string", "description": "empty=kill connection, 'query'=kill query only"},
		},
	}
}

func (s *KillSkill) Validate(_ skill.Params) error { return nil }

func (s *KillSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := params.StringOr("args", "")
	id, mode, confirm := parseMySQLKillArgs(args, params)

	if id == "" {
		return &skill.Result{Type: skill.ResultError, Summary: "usage: /kill <id> [query] [confirm]"}, nil
	}

	// Without confirm: query process details and show confirmation panel.
	if !confirm {
		return s.showConfirmation(ctx, id, mode)
	}

	// With confirm: execute kill.
	var sqlStr string
	if mode == "query" {
		sqlStr = fmt.Sprintf("KILL QUERY %s", id)
	} else {
		sqlStr = fmt.Sprintf("KILL %s", id)
	}

	if _, err := s.driver.Exec(ctx, sqlStr); err != nil {
		msg := fmt.Sprintf("终止进程失败: %v", err)
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "\033[33m⚠\033[0m " + msg,
			Summary:  msg,
		}, nil
	}

	action := "连接已终止"
	if mode == "query" {
		action = "查询已取消"
	}
	rendered := fmt.Sprintf("\033[1;32m✓\033[0m %s (ID=%s)", action, id)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%s (ID=%s)", action, id),
	}, nil
}

func (s *KillSkill) showConfirmation(ctx context.Context, id, mode string) (*skill.Result, error) {
	const (
		ansiYellow = "\033[33m"
		ansiReset  = "\033[0m"
		ansiDim    = "\033[90m"
	)

	query := fmt.Sprintf(mysqlProcessDetailSQL, id)
	detail, err := s.driver.Query(ctx, query)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: fmt.Sprintf("查询进程信息: %v", err)}, nil
	}
	if len(detail.Rows) == 0 {
		msg := fmt.Sprintf("进程 ID %s 不存在", id)
		return &skill.Result{Type: skill.ResultText, Rendered: "\033[33m⚠\033[0m " + msg, Summary: msg}, nil
	}

	row := detail.Rows[0]
	user := valOrDash(row, 1)
	host := valOrDash(row, 2)
	dbName := valOrDash(row, 3)
	command := valOrDash(row, 4)
	timeStr := valOrDash(row, 5)
	state := valOrDash(row, 6)
	sqlText := valOrDash(row, 7)

	infoLines := []string{
		fmt.Sprintf("ID: %s    用户: %s", id, user),
		fmt.Sprintf("主机: %s", host),
		fmt.Sprintf("数据库: %s    命令: %s", dbName, command),
		fmt.Sprintf("状态: %s    时间: %s s", state, timeStr),
	}
	if sqlText != "-" {
		infoLines = append(infoLines, fmt.Sprintf("SQL:  %s", sqlText))
	}

	action := "终止连接"
	if mode == "query" {
		action = "取消查询"
	}

	warnLines := []string{
		ansiYellow + "⚠ 此操作将" + action + ansiReset,
		"",
	}
	if mode == "query" {
		warnLines = append(warnLines,
			ansiDim+"/kill "+id+" query confirm       取消当前查询"+ansiReset,
		)
	} else {
		warnLines = append(warnLines,
			ansiDim+"/kill "+id+" confirm              终止连接"+ansiReset,
			ansiDim+"/kill "+id+" query confirm        仅取消当前查询"+ansiReset,
		)
	}

	sections := []format.PanelSection{
		{Lines: infoLines},
		{Lines: warnLines},
	}

	rendered := format.Panel(fmt.Sprintf("Kill Process (ID %s)", id), sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("ID %s (%s) 待确认", id, user),
	}, nil
}

// parseMySQLKillArgs extracts id, mode, and confirm from CLI args or tool params.
func parseMySQLKillArgs(args string, params skill.Params) (id, mode string, confirm bool) {
	if args != "" {
		parts := strings.Fields(args)
		if len(parts) == 0 {
			return "", "", false
		}
		id = parts[0]
		for _, t := range parts[1:] {
			switch strings.ToLower(t) {
			case "query":
				mode = "query"
			case "confirm", "yes", "y":
				confirm = true
			}
		}
		return id, mode, confirm
	}
	id = params.StringOr("id", "")
	mode = params.StringOr("mode", "")
	return id, mode, false
}

// valOrDash returns the string value at index i, or "-" if nil/empty.
func valOrDash(row []any, i int) string {
	if i >= len(row) || row[i] == nil {
		return "-"
	}
	v := fmt.Sprintf("%v", row[i])
	if v == "" || v == "<nil>" {
		return "-"
	}
	return v
}
