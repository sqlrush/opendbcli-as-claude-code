/*-------------------------------------------------------------------------
 *
 * backup.go
 *	  BackupSkill shows recent RMAN backup job details.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/backup.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const defaultBackupDays = 7

const backupSQL = `SELECT start_time, end_time, status, input_type,
       output_bytes_display, time_taken_display
FROM v$rman_backup_job_details
WHERE start_time > SYSDATE - :1
ORDER BY start_time DESC`

// BackupSkill shows recent RMAN backup job details.
type BackupSkill struct {
	driver db.Driver
}

// NewBackupSkill creates a BackupSkill backed by the given driver.
func NewBackupSkill(driver db.Driver) *BackupSkill {
	return &BackupSkill{driver: driver}
}

func (s *BackupSkill) Name() string        { return "backup" }
func (s *BackupSkill) Description() string { return "Show recent RMAN backup history" }
func (s *BackupSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelReadOnly
}

func (s *BackupSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "backup",
		Description: "Show recent RMAN backup history",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Days to look back (default 7)",
				},
			},
		},
	}
}

func (s *BackupSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "backup",
		Usage:    "/backup [days]",
		Examples: []string{"/backup", "/backup 30"},
	}
}

func (s *BackupSkill) Validate(_ skill.Params) error { return nil }

func (s *BackupSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	days := parseBackupDays(params.StringOr("args", ""))

	result, err := s.driver.Query(ctx, backupSQL, days)
	if err != nil {
		return nil, fmt.Errorf("querying backup history: %w", err)
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("最近 %d 天无备份记录", days),
			Summary:  fmt.Sprintf("最近 %d 天无备份", days),
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("RMAN 备份 (最近 %d 天) — %d 次", days, len(result.Rows)),
		Summary:  fmt.Sprintf("最近 %d 天 %d 次备份", days, len(result.Rows)),
		Metadata: map[string]string{
			"days": strconv.Itoa(days),
		},
	}, nil
}

// parseBackupDays extracts a positive integer from args.
// Falls back to defaultBackupDays if the value is empty or invalid.
func parseBackupDays(args string) int {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return defaultBackupDays
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n <= 0 {
		return defaultBackupDays
	}
	return n
}
