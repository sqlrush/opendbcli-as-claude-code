/*-------------------------------------------------------------------------
 *
 * replication.go
 *	  ReplicationSkill shows streaming replication status.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/replication.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const replPrimarySQL = `SELECT
  pid,
  usename,
  client_addr::text,
  state,
  sender_sent_location::text AS sent_lsn,
  receiver_write_location::text AS write_lsn,
  receiver_flush_location::text AS flush_lsn,
  receiver_replay_location::text AS replay_lsn,
  ''::text AS replay_lag,
  sync_state
FROM pg_stat_replication
ORDER BY client_addr`

const replStandbySQL = `SELECT
  pid,
  status,
  receive_start_lsn::text,
  receive_start_tli,
  received_lsn::text,
  received_tli,
  last_msg_send_time::text,
  last_msg_receipt_time::text,
  latest_end_lsn::text,
  latest_end_time::text,
  sender_host,
  sender_port,
  conninfo
FROM pg_stat_wal_receiver`

// ReplicationSkill shows streaming replication status.
type ReplicationSkill struct{ driver db.Driver }

// NewReplicationSkill creates a ReplicationSkill backed by the given driver.
func NewReplicationSkill(driver db.Driver) *ReplicationSkill {
	return &ReplicationSkill{driver: driver}
}

func (s *ReplicationSkill) Name() string                       { return "replication" }
func (s *ReplicationSkill) Description() string                { return "流复制状态" }
func (s *ReplicationSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ReplicationSkill) Validate(_ skill.Params) error      { return nil }

func (s *ReplicationSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/replication", Examples: []string{"/replication"}}
}

func (s *ReplicationSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "replication",
		Description: "Show streaming replication status for primary or standby",
	}
}

func (s *ReplicationSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	// Check if we are in recovery (standby) or primary
	recoveryResult, err := s.driver.Query(ctx, "SELECT pg_is_in_recovery()")
	if err != nil {
		msg := "查询复制状态失败: " + err.Error()
		return &skill.Result{Type: skill.ResultError, Data: msg, Summary: msg}, nil
	}

	isStandby := false
	if len(recoveryResult.Rows) > 0 {
		isStandby = asStr(recoveryResult.Rows[0][0]) == "true"
	}

	if isStandby {
		return s.standbyPanel(ctx)
	}
	return s.primaryPanel(ctx)
}

func (s *ReplicationSkill) primaryPanel(ctx context.Context) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, replPrimarySQL)
	if err != nil {
		msg := "查询复制状态失败: " + err.Error()
		return &skill.Result{Type: skill.ResultError, Data: msg, Summary: msg}, nil
	}

	if len(result.Rows) == 0 {
		msg := "当前无复制配置 (Primary, 无 streaming replicas)"
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "\033[33m⚠\033[0m " + msg,
			Summary:  msg,
		}, nil
	}

	sections := []format.PanelSection{
		{
			Header: "Role",
			Lines:  []string{"Current Role    : Primary"},
		},
	}

	for i, row := range result.Rows {
		if len(row) < 10 {
			continue
		}
		replicaLines := []string{
			fmt.Sprintf("PID             : %s", asStr(row[0])),
			fmt.Sprintf("User            : %s", asStr(row[1])),
			fmt.Sprintf("Client          : %s", asStr(row[2])),
			fmt.Sprintf("State           : %s", asStr(row[3])),
			fmt.Sprintf("Sent LSN        : %s", asStr(row[4])),
			fmt.Sprintf("Write LSN       : %s", asStr(row[5])),
			fmt.Sprintf("Flush LSN       : %s", asStr(row[6])),
			fmt.Sprintf("Replay LSN      : %s", asStr(row[7])),
			fmt.Sprintf("Replay Lag      : %s", asStr(row[8])),
			fmt.Sprintf("Sync State      : %s", asStr(row[9])),
		}
		sections = append(sections, format.PanelSection{
			Header: fmt.Sprintf("Replica #%d", i+1),
			Lines:  replicaLines,
		})
	}

	rendered := format.Panel("Replication Status", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  fmt.Sprintf("primary with %d replicas", len(result.Rows)),
	}, nil
}

func (s *ReplicationSkill) standbyPanel(ctx context.Context) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, replStandbySQL)
	if err != nil {
		msg := "查询 WAL receiver 状态失败: " + err.Error()
		return &skill.Result{Type: skill.ResultError, Data: msg, Summary: msg}, nil
	}

	sections := []format.PanelSection{
		{
			Header: "Role",
			Lines:  []string{"Current Role    : Standby"},
		},
	}

	if len(result.Rows) == 0 {
		sections = append(sections, format.PanelSection{
			Header: "WAL Receiver",
			Lines:  []string{"Status          : not running"},
		})
	} else {
		row := result.Rows[0]
		lines := []string{
			fmt.Sprintf("PID             : %s", asStr(row[0])),
			fmt.Sprintf("Status          : %s", asStr(row[1])),
			fmt.Sprintf("Received LSN    : %s", asStr(row[4])),
			fmt.Sprintf("Latest End LSN  : %s", asStr(row[8])),
			fmt.Sprintf("Latest End Time : %s", asStr(row[9])),
			fmt.Sprintf("Sender Host     : %s", asStr(row[10])),
			fmt.Sprintf("Sender Port     : %s", asStr(row[11])),
		}
		sections = append(sections, format.PanelSection{
			Header: "WAL Receiver",
			Lines:  lines,
		})
	}

	rendered := format.Panel("Replication Status", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "standby replication status",
	}, nil
}
