/*-------------------------------------------------------------------------
 *
 * redo.go
 *	  RedoLogGroup represents a redo log group row.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/redo.go
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

const redoLogGroupsSQL = `SELECT group#, thread#, sequence#,
       ROUND(bytes/1048576) AS size_mb,
       members, status
FROM v$log
ORDER BY group#`

const redoSwitchFreqSQL = `SELECT TO_CHAR(first_time, 'HH24') AS hour,
       COUNT(*) AS switches
FROM v$log_history
WHERE first_time > SYSDATE - 1
GROUP BY TO_CHAR(first_time, 'HH24')
ORDER BY hour`

const redoArchiveDestSQL = `SELECT dest_name, status, target, archiver
FROM v$archive_dest
WHERE status != 'INACTIVE'
AND rownum <= 5`

// RedoLogGroup represents a redo log group row.
type RedoLogGroup struct {
	Group    string `json:"group"`
	Thread   string `json:"thread"`
	Sequence string `json:"sequence"`
	SizeMB   int    `json:"size_mb"`
	Members  string `json:"members"`
	Status   string `json:"status"`
}

// RedoSwitchHour represents log switch count in one hour.
type RedoSwitchHour struct {
	Hour     string `json:"hour"`
	Switches int    `json:"switches"`
}

// RedoArchiveDest represents an archive destination row.
type RedoArchiveDest struct {
	DestName string `json:"dest_name"`
	Status   string `json:"status"`
	Target   string `json:"target"`
	Archiver string `json:"archiver"`
}

// RedoReport is the structured output for LLM consumption.
type RedoReport struct {
	LogGroups    []RedoLogGroup   `json:"log_groups"`
	SwitchFreq   []RedoSwitchHour `json:"switch_freq"`
	ArchiveDests []RedoArchiveDest `json:"archive_dests"`
}

// RedoSkill shows redo log status, switch frequency, and archive destinations.
type RedoSkill struct {
	driver db.Driver
}

// NewRedoSkill creates a RedoSkill backed by the given driver.
func NewRedoSkill(driver db.Driver) *RedoSkill {
	return &RedoSkill{driver: driver}
}

func (s *RedoSkill) Name() string                      { return "redo" }
func (s *RedoSkill) Description() string               { return "Show redo log status, switch frequency and archive destinations" }
func (s *RedoSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *RedoSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "redo",
		Description: "Show redo log status, switch frequency and archive destinations",
	}
}

func (s *RedoSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "redo",
		Usage:   "/redo",
	}
}

func (s *RedoSkill) Validate(_ skill.Params) error { return nil }

func (s *RedoSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	groupsResult, err := s.driver.Query(ctx, redoLogGroupsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying redo log groups: %w", err)
	}

	switchResult, err := s.driver.Query(ctx, redoSwitchFreqSQL)
	if err != nil {
		return nil, fmt.Errorf("querying log switch frequency: %w", err)
	}

	// Archive dest is supplementary — don't fail if query errors.
	archiveResult, err := s.driver.Query(ctx, redoArchiveDestSQL)
	if err != nil {
		archiveResult = &db.QueryResult{}
	}

	groups := make([]RedoLogGroup, 0, len(groupsResult.Rows))
	for _, row := range groupsResult.Rows {
		groups = append(groups, RedoLogGroup{
			Group:    rowStr(row, 0),
			Thread:   rowStr(row, 1),
			Sequence: rowStr(row, 2),
			SizeMB:   rowInt(row, 3),
			Members:  rowStr(row, 4),
			Status:   rowStr(row, 5),
		})
	}

	switches := make([]RedoSwitchHour, 0, len(switchResult.Rows))
	for _, row := range switchResult.Rows {
		switches = append(switches, RedoSwitchHour{
			Hour:     rowStr(row, 0),
			Switches: rowInt(row, 1),
		})
	}

	dests := make([]RedoArchiveDest, 0, len(archiveResult.Rows))
	for _, row := range archiveResult.Rows {
		dests = append(dests, RedoArchiveDest{
			DestName: rowStr(row, 0),
			Status:   rowStr(row, 1),
			Target:   rowStr(row, 2),
			Archiver: rowStr(row, 3),
		})
	}

	report := RedoReport{
		LogGroups:    groups,
		SwitchFreq:   switches,
		ArchiveDests: dests,
	}
	rendered := redoRender(groups, switches, dests)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &report,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d redo groups, %d switch hours, %d archive dests", len(groups), len(switches), len(dests)),
	}, nil
}

func redoRender(groups []RedoLogGroup, switches []RedoSwitchHour, dests []RedoArchiveDest) string {
	var b strings.Builder

	// Section 1: Log Groups
	b.WriteString("Redo 日志状态:\n\n")
	groupsQR := &db.QueryResult{
		Columns: []string{"GROUP", "SIZE_MB", "MEMBERS", "STATUS", "SEQUENCE"},
		Rows:    make([][]any, 0, len(groups)),
	}
	for _, g := range groups {
		groupsQR.Rows = append(groupsQR.Rows, []any{
			g.Group, fmt.Sprintf("%d", g.SizeMB), g.Members, g.Status, g.Sequence,
		})
	}
	b.WriteString(format.FormatTableOpts(groupsQR, format.TableOptions{MaxRows: 50, TermWidth: 120}))

	// Section 2: Switch Frequency
	b.WriteString("\n\n最近24小时日志切换 (按小时):\n\n")
	if len(switches) == 0 {
		b.WriteString("最近24小时无日志切换\n")
	} else {
		switchQR := &db.QueryResult{
			Columns: []string{"小时", "切换次数"},
			Rows:    make([][]any, 0, len(switches)),
		}
		for _, sw := range switches {
			switchQR.Rows = append(switchQR.Rows, []any{
				sw.Hour, fmt.Sprintf("%d", sw.Switches),
			})
		}
		b.WriteString(format.FormatTableOpts(switchQR, format.TableOptions{MaxRows: 50, TermWidth: 120}))
	}

	// Section 3: Archive Destinations
	if len(dests) > 0 {
		b.WriteString("\n\n归档目标:\n\n")
		destQR := &db.QueryResult{
			Columns: []string{"DEST", "STATUS", "TARGET", "ARCHIVER"},
			Rows:    make([][]any, 0, len(dests)),
		}
		for _, d := range dests {
			destQR.Rows = append(destQR.Rows, []any{
				d.DestName, d.Status, d.Target, d.Archiver,
			})
		}
		b.WriteString(format.FormatTableOpts(destQR, format.TableOptions{MaxRows: 50, TermWidth: 120}))
	}

	b.WriteByte('\n')
	return b.String()
}
