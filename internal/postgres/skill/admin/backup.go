/*-------------------------------------------------------------------------
 *
 * backup.go
 *	  BackupSkill shows WAL archiver status.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/admin/backup.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const backupArchiverSQL = `SELECT
  archived_count,
  last_archived_wal,
  last_archived_time::text,
  failed_count,
  last_failed_wal,
  last_failed_time::text,
  stats_reset::text
FROM pg_stat_archiver`

const backupSettingsSQL = `SELECT
  current_setting('archive_mode') AS archive_mode,
  current_setting('archive_command') AS archive_command,
  current_setting('wal_level') AS wal_level`

// BackupSkill shows WAL archiver status.
type BackupSkill struct {
	driver db.Driver
}

// NewBackupSkill creates a BackupSkill backed by the given driver.
func NewBackupSkill(driver db.Driver) *BackupSkill {
	return &BackupSkill{driver: driver}
}

func (s *BackupSkill) Name() string                       { return "backup" }
func (s *BackupSkill) Description() string                { return "WAL 归档状态" }
func (s *BackupSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *BackupSkill) Validate(_ skill.Params) error      { return nil }

func (s *BackupSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/backup", Examples: []string{"/backup"}}
}

func (s *BackupSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "backup",
		Description: "Show WAL archiver status and configuration",
	}
}

func (s *BackupSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	sections := []format.PanelSection{}

	// Settings
	settingsResult, err := s.driver.Query(ctx, backupSettingsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(settingsResult.Rows) > 0 {
		row := settingsResult.Rows[0]
		archiveMode := asStr(row[0])
		archiveCmd := asStr(row[1])
		walLevel := asStr(row[2])

		if len(archiveCmd) > 80 {
			archiveCmd = archiveCmd[:77] + "..."
		}

		sections = append(sections, format.PanelSection{
			Header: "Configuration",
			Lines: []string{
				fmt.Sprintf("archive_mode    : %s", archiveMode),
				fmt.Sprintf("archive_command : %s", archiveCmd),
				fmt.Sprintf("wal_level       : %s", walLevel),
			},
		})
	}

	// Archiver stats
	archResult, err := s.driver.Query(ctx, backupArchiverSQL)
	if err == nil && len(archResult.Rows) > 0 {
		row := archResult.Rows[0]
		archivedCount := asStr(row[0])
		failedCount := asStr(row[3])

		lines := []string{
			fmt.Sprintf("Archived Count  : %s", archivedCount),
			fmt.Sprintf("Last Archived   : %s", asStr(row[1])),
			fmt.Sprintf("Last Archive At : %s", asStr(row[2])),
			fmt.Sprintf("Failed Count    : %s", failedCount),
		}

		if failedCount != "0" && failedCount != "" {
			lines = append(lines,
				fmt.Sprintf("Last Failed WAL : %s", asStr(row[4])),
				fmt.Sprintf("Last Fail At    : %s", asStr(row[5])),
			)
		}

		lines = append(lines, fmt.Sprintf("Stats Reset     : %s", asStr(row[6])))

		sections = append(sections, format.PanelSection{
			Header: "Archiver Statistics",
			Lines:  lines,
		})
	}

	rendered := format.Panel("WAL Archiver", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "WAL archiver status",
	}, nil
}
