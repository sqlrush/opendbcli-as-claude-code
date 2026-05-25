/*-------------------------------------------------------------------------
 *
 * kill.go
 *	  KillSkill terminates a database session by SID.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/kill.go
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

const serialLookupSQL = `SELECT serial# FROM v$session WHERE sid = :1`

const sessionDetailSQL = `SELECT s.sid, s.serial#, s.username, s.status, s.sql_id,
       NVL(CASE WHEN s.wait_class = 'Idle' THEN 'Idle: '||s.event ELSE s.event END, 'ON CPU') AS event,
       s.program,
       ROUND((SYSDATE - s.logon_time) * 24 * 60, 1) AS login_min,
       SUBSTR(q.sql_text, 1, 80) AS sql_text
FROM v$session s
LEFT JOIN v$sql q ON s.sql_id = q.sql_id AND q.child_number = 0
WHERE s.sid = :1`

// KillSkill terminates a database session by SID.
type KillSkill struct {
	driver db.Driver
}

// NewKillSkill creates a KillSkill backed by the given driver.
func NewKillSkill(driver db.Driver) *KillSkill {
	return &KillSkill{driver: driver}
}

func (s *KillSkill) Name() string        { return "kill" }
func (s *KillSkill) Description() string { return "Kill a database session by SID" }
func (s *KillSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelOperator
}

func (s *KillSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "kill",
		Description: "Kill a database session by SID",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "SID to kill, optionally followed by 'immediate'",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *KillSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "kill",
		Usage:    "/kill <sid> [immediate]",
		Examples: []string{"/kill 142", "/kill 142 immediate"},
	}
}

func (s *KillSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("SID is required: /kill <sid> [immediate]")
	}
	sid, _, _ := parseKillArgs(args)
	if sid == "" {
		return fmt.Errorf("invalid SID: %q", args)
	}
	return nil
}

func (s *KillSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	sid, immediate, confirm := parseKillArgs(args)

	// Query session details for display.
	detail, err := s.driver.Query(ctx, sessionDetailSQL, sid)
	if err != nil {
		return nil, fmt.Errorf("查询会话信息: %w", err)
	}
	if len(detail.Rows) == 0 {
		return nil, fmt.Errorf("会话 SID %s 不存在", sid)
	}

	row := detail.Rows[0]
	serial := fmt.Sprintf("%v", row[1])
	username := fmt.Sprintf("%v", row[2])
	status := fmt.Sprintf("%v", row[3])
	sqlID := fmt.Sprintf("%v", row[4])
	event := fmt.Sprintf("%v", row[5])
	program := fmt.Sprintf("%v", row[6])
	loginMin := fmt.Sprintf("%v", row[7])
	sqlText := ""
	if len(row) > 8 && row[8] != nil {
		sqlText = fmt.Sprintf("%v", row[8])
	}

	if username == "<nil>" || username == "" {
		username = "-"
	}
	if sqlID == "<nil>" || sqlID == "" {
		sqlID = "-"
	}
	if sqlText == "" || sqlText == "<nil>" {
		sqlText = "-"
	}

	// Without confirm: show session info and ask for confirmation.
	if !confirm {
		return s.showConfirmation(sid, serial, username, status, sqlID, event, program, loginMin, sqlText, immediate)
	}

	// With confirm: execute kill.
	killSQL := buildKillSQL(sid, serial, immediate)
	if _, err := s.driver.Exec(ctx, killSQL); err != nil {
		return nil, fmt.Errorf("终止会话失败 (%s,%s): %w", sid, serial, err)
	}

	mode := ""
	if immediate {
		mode = " IMMEDIATE"
	}

	rendered := fmt.Sprintf("\033[1;32m✓\033[0m 会话 '%s,%s' (%s) 已终止%s", sid, serial, username, mode)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("killed SID %s (%s)", sid, username),
	}, nil
}

func (s *KillSkill) showConfirmation(sid, serial, username, status, sqlID, event, program, loginMin, sqlText string, immediate bool) (*skill.Result, error) {
	const (
		ansiYellow = "\033[33m"
		ansiReset  = "\033[0m"
		ansiDim    = "\033[90m"
	)

	infoLines := []string{
		fmt.Sprintf("SID: %s    Serial#: %s", sid, serial),
		fmt.Sprintf("用户: %s          程序: %s", username, program),
		fmt.Sprintf("状态: %s            登录: %s 分钟", status, loginMin),
		fmt.Sprintf("等待: %s", event),
	}
	if sqlText != "-" {
		infoLines = append(infoLines, fmt.Sprintf("SQL:  %s", sqlText))
	}

	warnLines := []string{
		ansiYellow + "⚠ 此操作将立即断开该会话的数据库连接" + ansiReset,
		"",
		ansiDim + "/kill " + sid + " confirm             终止 (等待事务完成)" + ansiReset,
	}
	if !immediate {
		warnLines = append(warnLines, ansiDim+"/kill "+sid+" immediate confirm   立即终止 (回滚未提交事务)"+ansiReset)
	} else {
		warnLines = append(warnLines, ansiDim+"/kill "+sid+" immediate confirm"+ansiReset)
	}

	sections := []format.PanelSection{
		{Lines: infoLines},
		{Lines: warnLines},
	}

	rendered := format.Panel(fmt.Sprintf("Kill Session (SID %s)", sid), sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("SID %s (%s) 待确认", sid, username),
	}, nil
}

// parseKillArgs splits args into SID, immediate flag, and confirm flag.
func parseKillArgs(args string) (sid string, immediate bool, confirm bool) {
	tokens := strings.Fields(args)
	if len(tokens) == 0 {
		return "", false, false
	}
	sid = tokens[0]
	for _, t := range tokens[1:] {
		switch strings.ToLower(t) {
		case "immediate":
			immediate = true
		case "confirm", "yes", "y":
			confirm = true
		}
	}
	return sid, immediate, confirm
}

// buildKillSQL constructs the ALTER SYSTEM KILL SESSION statement.
func buildKillSQL(sid, serial string, immediate bool) string {
	stmt := fmt.Sprintf("ALTER SYSTEM KILL SESSION '%s,%s'", sid, serial)
	if immediate {
		stmt += " IMMEDIATE"
	}
	return stmt
}
