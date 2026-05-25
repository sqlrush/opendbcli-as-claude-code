/*-------------------------------------------------------------------------
 *
 * standby.go
 *	  standby — StandbySkill plus helpers (NewStandbySkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/standby.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// V$DATABASE 实测列 (DM 8.1.4.200):
// NAME, CREATE_TIME, ARCH_MODE, LAST_CKPT_TIME, STATUS$, ROLE$, MAX_SIZE,
// TOTAL_SIZE, DSC_NODES, OPEN_COUNT, STARTUP_COUNT, LAST_STARTUP_TIME
// 注意: 没有 DBID 列 (Oracle 才有), 用 NAME + STATUS$ + ROLE$
// V$RLOG 实测列: CUR_FILE, FILE_LSN, CKPT_LSN, FREE_SPACE, ...
// V$ARCH_SEND_INFO 实测列: DEST, ARCH_STATUS, LAST_SEND_FILE, LAST_SEND_LSN, ...
const standbyDbSQL = `SELECT NAME, ROLE$ AS ROLE, STATUS$ AS STATUS, ARCH_MODE,
       LAST_CKPT_TIME, LAST_STARTUP_TIME
FROM V$DATABASE`

const standbyLsnSQL = `SELECT CUR_FILE, FILE_LSN, CKPT_LSN, FREE_SPACE
FROM V$RLOG`

const standbySendSQL = `SELECT DEST, ARCH_STATUS, LAST_SEND_FILE, LAST_SEND_LSN
FROM V$ARCH_SEND_INFO`

type StandbySkill struct{ driver db.Driver }

func NewStandbySkill(driver db.Driver) *StandbySkill { return &StandbySkill{driver: driver} }

func (s *StandbySkill) Name() string                       { return "standby" }
func (s *StandbySkill) Description() string                { return "主备状态 (V$DATABASE.ROLE / V$RLOG / V$ARCH_SEND_INFO)" }
func (s *StandbySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *StandbySkill) Validate(_ skill.Params) error      { return nil }

func (s *StandbySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "standby", Description: "Show DM primary/standby state and LSN sync"}
}
func (s *StandbySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "standby", Usage: "/standby"}
}

func (s *StandbySkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	dbInfo, err := s.driver.Query(ctx, standbyDbSQL)
	if err != nil {
		return nil, fmt.Errorf("dm standby db: %w", err)
	}
	lsn, err := s.driver.Query(ctx, standbyLsnSQL)
	if err != nil {
		return nil, fmt.Errorf("dm standby lsn: %w", err)
	}
	send, sendErr := s.driver.Query(ctx, standbySendSQL)
	// send 可能因为不是主库而无数据，不致命

	var b strings.Builder
	b.WriteString("=== 数据库角色 ===\n")
	b.WriteString(format.FormatTable(dbInfo))
	b.WriteString("\n=== 当前 LSN ===\n")
	b.WriteString(format.FormatTable(lsn))
	if sendErr == nil && send != nil {
		b.WriteString("\n=== 归档发送状态 (主库才有) ===\n")
		b.WriteString(format.FormatTable(send))
	}

	entries := []dmutil.SummaryEntry{}
	role := "unknown"
	if len(dbInfo.Rows) > 0 && len(dbInfo.Rows[0]) >= 4 {
		// 列顺序: NAME, ROLE, STATUS, ARCH_MODE, LAST_CKPT_TIME, LAST_STARTUP_TIME
		// V$DATABASE.ROLE$ 是 TINYINT, 翻译为字符串方便 LLM 理解
		rawRole := fmt.Sprintf("%v", dbInfo.Rows[0][1])
		role = translateDBRole(rawRole)
		rawStatus := fmt.Sprintf("%v", dbInfo.Rows[0][2])
		entries = append(entries,
			dmutil.SummaryEntry{Key: "db_name", Val: fmt.Sprintf("%v", dbInfo.Rows[0][0])},
			dmutil.SummaryEntry{Key: "role", Val: role},
			dmutil.SummaryEntry{Key: "role_raw", Val: rawRole},
			dmutil.SummaryEntry{Key: "status", Val: translateDBStatus(rawStatus)},
			dmutil.SummaryEntry{Key: "status_raw", Val: rawStatus},
			dmutil.SummaryEntry{Key: "arch_mode", Val: fmt.Sprintf("%v", dbInfo.Rows[0][3])},
		)
	}
	if len(lsn.Rows) > 0 && len(lsn.Rows[0]) >= 3 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "current_lsn", Val: fmt.Sprintf("%v", lsn.Rows[0][1])},
			dmutil.SummaryEntry{Key: "ckpt_lsn", Val: fmt.Sprintf("%v", lsn.Rows[0][2])},
		)
	}
	if sendErr == nil && send != nil {
		entries = append(entries, dmutil.SummaryEntry{Key: "standby_dest_count", Val: len(send.Rows)})
		if len(send.Rows) > 0 && len(send.Rows[0]) >= 2 {
			entries = append(entries, dmutil.SummaryEntry{
				Key: "first_dest_status",
				Val: fmt.Sprintf("%v", send.Rows[0][1]),
			})
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     dbInfo,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  fmt.Sprintf("角色 %s", role),
	}, nil
}

// translateDBRole 将 V$DATABASE.ROLE$ 数值翻译为可读字符串.
// 参考 DM 8 文档: 0=PRIMARY 1=STANDBY 2=DBSTANDBY 3=BACKUP_PENDING.
func translateDBRole(raw string) string {
	switch raw {
	case "0":
		return "PRIMARY"
	case "1":
		return "STANDBY"
	case "2":
		return "DBSTANDBY"
	case "3":
		return "BACKUP_PENDING"
	default:
		return raw
	}
}

// translateDBStatus 将 V$DATABASE.STATUS$ 数值翻译为可读字符串.
// 参考 DM 8 文档: 1=STARTUP 2=AFTER_REDO 3=BACKUP 4=OPEN 5=SUSPEND.
func translateDBStatus(raw string) string {
	switch raw {
	case "1":
		return "STARTUP"
	case "2":
		return "AFTER_REDO"
	case "3":
		return "BACKUP"
	case "4":
		return "OPEN"
	case "5":
		return "SUSPEND"
	default:
		return raw
	}
}
