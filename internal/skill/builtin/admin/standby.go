/*-------------------------------------------------------------------------
 *
 * standby.go
 *	  StandbyDatabaseInfo is the structured representation of v$database
 *	  Data Guard info.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/standby.go
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

const standbyDatabaseSQL = `SELECT database_role, db_unique_name, protection_mode,
       switchover_status, dataguard_broker
FROM v$database`

const standbyArchiveDestSQL = `SELECT dest_name, status, type,
       NVL(srl, '-') AS srl,
       NVL(gap_status, '-') AS gap_status
FROM v$archive_dest_status
WHERE status != 'INACTIVE'`

// StandbyDatabaseInfo is the structured representation of v$database Data Guard info.
type StandbyDatabaseInfo struct {
	Role             string `json:"role"`
	DBUniqueName     string `json:"db_unique_name"`
	ProtectionMode   string `json:"protection_mode"`
	SwitchoverStatus string `json:"switchover_status"`
	DataguardBroker  string `json:"dataguard_broker"`
}

// StandbyArchiveDest is the structured representation of an archive destination.
type StandbyArchiveDest struct {
	DestName  string `json:"dest_name"`
	Status    string `json:"status"`
	Type      string `json:"type"`
	SRL       string `json:"srl"`
	GapStatus string `json:"gap_status"`
}

// StandbyData is the structured output of the standby skill for LLM consumption.
type StandbyData struct {
	Database     *StandbyDatabaseInfo `json:"database"`
	ArchiveDests []StandbyArchiveDest `json:"archive_dests"`
}

// StandbySkill shows Data Guard / standby database status.
type StandbySkill struct {
	driver db.Driver
}

// NewStandbySkill creates a StandbySkill backed by the given driver.
func NewStandbySkill(driver db.Driver) *StandbySkill {
	return &StandbySkill{driver: driver}
}

func (s *StandbySkill) Name() string        { return "standby" }
func (s *StandbySkill) Description() string { return "Show Data Guard standby status" }
func (s *StandbySkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelReadOnly
}

func (s *StandbySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "standby",
		Description: "Show Data Guard standby status",
	}
}

func (s *StandbySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "standby",
		Usage:    "/standby",
		Examples: []string{"/standby"},
	}
}

func (s *StandbySkill) Validate(_ skill.Params) error { return nil }

func (s *StandbySkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	dbInfo, err := s.driver.Query(ctx, standbyDatabaseSQL)
	if err != nil {
		return nil, fmt.Errorf("querying database info: %w", err)
	}

	archiveDest, err := s.driver.Query(ctx, standbyArchiveDestSQL)
	if err != nil {
		return nil, fmt.Errorf("querying archive dest status: %w", err)
	}

	output := formatStandbyOutput(dbInfo, archiveDest)
	data := buildStandbyData(dbInfo, archiveDest)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &data,
		Rendered: output,
		Summary:  "Data Guard status",
	}, nil
}

func buildStandbyData(dbInfo, archiveDest *db.QueryResult) StandbyData {
	data := StandbyData{
		ArchiveDests: make([]StandbyArchiveDest, 0, len(archiveDest.Rows)),
	}
	if len(dbInfo.Rows) > 0 {
		row := dbInfo.Rows[0]
		data.Database = &StandbyDatabaseInfo{
			Role:             valStr(row, 0),
			DBUniqueName:     valStr(row, 1),
			ProtectionMode:   valStr(row, 2),
			SwitchoverStatus: valStr(row, 3),
			DataguardBroker:  valStr(row, 4),
		}
	}
	for _, row := range archiveDest.Rows {
		data.ArchiveDests = append(data.ArchiveDests, StandbyArchiveDest{
			DestName:  valStr(row, 0),
			Status:    valStr(row, 1),
			Type:      valStr(row, 2),
			SRL:       valStr(row, 3),
			GapStatus: valStr(row, 4),
		})
	}
	return data
}

func valStr(row []any, idx int) string {
	if idx < 0 || idx >= len(row) || row[idx] == nil {
		return ""
	}
	return fmt.Sprintf("%v", row[idx])
}

// formatStandbyOutput combines database info and archive dest results
// into a human-readable text block.
func formatStandbyOutput(dbInfo, archiveDest *db.QueryResult) string {
	var b strings.Builder

	b.WriteString("── Database Info ──\n")
	if len(dbInfo.Rows) > 0 {
		for i, col := range dbInfo.Columns {
			if i < len(dbInfo.Rows[0]) {
				val := dbInfo.Rows[0][i]
				if val == nil {
					val = "-"
				}
				fmt.Fprintf(&b, "  %-20s %v\n", col+":", val)
			}
		}
	} else {
		b.WriteString("  No data available\n")
	}

	b.WriteString("\n── Archive Dest Status ──\n")
	if len(archiveDest.Rows) > 0 {
		opts := format.TableOptions{MaxRows: 50, TermWidth: 120}
		b.WriteString(format.FormatTableOpts(archiveDest, opts))
	} else {
		b.WriteString("  No active archive destinations\n")
	}

	return b.String()
}
