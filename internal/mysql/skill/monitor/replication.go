/*-------------------------------------------------------------------------
 *
 * replication.go
 *	  ReplicationSkill shows MySQL replica status.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/replication.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// ReplicationSkill shows MySQL replica status.
type ReplicationSkill struct {
	driver db.Driver
}

// NewReplicationSkill creates a ReplicationSkill backed by the given driver.
func NewReplicationSkill(driver db.Driver) *ReplicationSkill {
	return &ReplicationSkill{driver: driver}
}

func (s *ReplicationSkill) Name() string                       { return "replication" }
func (s *ReplicationSkill) Description() string                { return "复制状态" }
func (s *ReplicationSkill) Category() string                   { return "monitor" }
func (s *ReplicationSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ReplicationSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/replication", Examples: []string{"/replication"}}
}

func (s *ReplicationSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "replication",
		Description: "Show MySQL replica status including IO/SQL threads, lag, GTID",
	}
}

func (s *ReplicationSkill) Validate(_ skill.Params) error { return nil }

func (s *ReplicationSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, "SHOW REPLICA STATUS")
	if err != nil {
		// Fallback for MySQL < 8.0.22
		result, err = s.driver.Query(ctx, "SHOW SLAVE STATUS")
		if err != nil {
			msg := "查询复制状态失败: " + err.Error()
			return &skill.Result{Type: skill.ResultError, Data: msg, Summary: msg}, nil
		}
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前实例非副本 (无复制配置)",
			Summary:  "not a replica",
		}, nil
	}

	rendered := s.buildPanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "replica status",
	}, nil
}

func (s *ReplicationSkill) buildPanel(result *db.QueryResult) string {
	// Build column-to-index map for easy lookup.
	colIdx := make(map[string]int, len(result.Columns))
	for i, col := range result.Columns {
		colIdx[strings.ToUpper(col)] = i
	}

	row := result.Rows[0]
	get := func(names ...string) string {
		for _, name := range names {
			if idx, ok := colIdx[strings.ToUpper(name)]; ok && idx < len(row) {
				return asStr(row[idx])
			}
		}
		return "N/A"
	}

	ioRunning := get("Replica_IO_Running", "Slave_IO_Running")
	sqlRunning := get("Replica_SQL_Running", "Slave_SQL_Running")
	secsBehind := get("Seconds_Behind_Source", "Seconds_Behind_Master")
	sourceHost := get("Source_Host", "Master_Host")
	sourcePort := get("Source_Port", "Master_Port")
	sourceLogFile := get("Source_Log_File", "Master_Log_File")
	readLogPos := get("Read_Source_Log_Pos", "Read_Master_Log_Pos")
	execLogFile := get("Relay_Source_Log_File", "Relay_Master_Log_File")
	execLogPos := get("Exec_Source_Log_Pos", "Exec_Master_Log_Pos")
	gtidSet := get("Executed_Gtid_Set")
	lastErrno := get("Last_Errno")
	lastError := get("Last_Error")
	ioErrno := get("Last_IO_Errno")
	ioError := get("Last_IO_Error")
	sqlErrno := get("Last_SQL_Errno")
	sqlError := get("Last_SQL_Error")

	ioIcon := format.StatusIcon("OK")
	if ioRunning != "Yes" {
		ioIcon = format.StatusIcon("FAIL")
	}
	sqlIcon := format.StatusIcon("OK")
	if sqlRunning != "Yes" {
		sqlIcon = format.StatusIcon("FAIL")
	}

	sections := []format.PanelSection{
		{
			Header: "Thread Status",
			Lines: []string{
				fmt.Sprintf("IO  Thread      : %s %s", ioIcon, ioRunning),
				fmt.Sprintf("SQL Thread      : %s %s", sqlIcon, sqlRunning),
				fmt.Sprintf("Seconds Behind  : %s", secsBehind),
			},
		},
		{
			Header: "Source",
			Lines: []string{
				fmt.Sprintf("Source Host     : %s:%s", sourceHost, sourcePort),
				fmt.Sprintf("Source Log File : %s", sourceLogFile),
				fmt.Sprintf("Read Log Pos    : %s", readLogPos),
				fmt.Sprintf("Exec Log File   : %s", execLogFile),
				fmt.Sprintf("Exec Log Pos    : %s", execLogPos),
			},
		},
	}

	if gtidSet != "" && gtidSet != "N/A" {
		// Truncate long GTID sets for display
		display := gtidSet
		if len(display) > 80 {
			display = display[:77] + "..."
		}
		sections = append(sections, format.PanelSection{
			Header: "GTID",
			Lines: []string{
				fmt.Sprintf("Executed GTID   : %s", display),
			},
		})
	}

	// Errors section
	var errorLines []string
	if lastErrno != "0" && lastErrno != "" && lastErrno != "N/A" {
		errorLines = append(errorLines, fmt.Sprintf("Last Error      : [%s] %s", lastErrno, lastError))
	}
	if ioErrno != "0" && ioErrno != "" && ioErrno != "N/A" {
		errorLines = append(errorLines, fmt.Sprintf("IO Error        : [%s] %s", ioErrno, ioError))
	}
	if sqlErrno != "0" && sqlErrno != "" && sqlErrno != "N/A" {
		errorLines = append(errorLines, fmt.Sprintf("SQL Error       : [%s] %s", sqlErrno, sqlError))
	}
	if len(errorLines) == 0 {
		errorLines = append(errorLines, fmt.Sprintf("Errors          : %s None", format.StatusIcon("OK")))
	}
	sections = append(sections, format.PanelSection{
		Header: "Errors",
		Lines:  errorLines,
	})

	return format.Panel("Replication Status", sections)
}
