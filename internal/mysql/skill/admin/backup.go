/*-------------------------------------------------------------------------
 *
 * backup.go
 *	  BackupSkill shows backup-related info: binary logs and current
 *	  position.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/admin/backup.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// BackupSkill shows backup-related info: binary logs and current position.
type BackupSkill struct {
	driver db.Driver
}

// NewBackupSkill creates a BackupSkill backed by the given driver.
func NewBackupSkill(driver db.Driver) *BackupSkill {
	return &BackupSkill{driver: driver}
}

func (s *BackupSkill) Name() string                       { return "backup" }
func (s *BackupSkill) Description() string                { return "备份状态 (Binlog)" }
func (s *BackupSkill) Category() string                   { return "admin" }
func (s *BackupSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *BackupSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/backup", Examples: []string{"/backup"}}
}

func (s *BackupSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "backup",
		Description: "Show backup status: binary log list and current position",
	}
}

func (s *BackupSkill) Validate(_ skill.Params) error { return nil }

func (s *BackupSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	// Try SHOW BINARY LOGS first.
	result, err := s.driver.Query(ctx, "SHOW BINARY LOGS")
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "Binary logging 未启用或无 binlog 文件",
			Summary:  "no binary logs",
		}, nil
	}

	// Calculate total size.
	var totalBytes float64
	for _, row := range result.Rows {
		if len(row) >= 2 {
			totalBytes += toFloat64(row[1])
		}
	}

	// Add total size as summary.
	summary := fmt.Sprintf("binary logs: %d files, %.2f MB total", len(result.Rows), totalBytes/1048576)
	rendered := fmt.Sprintf("Binary Logs — %d 个文件, 共 %.2f MB", len(result.Rows), totalBytes/1048576)

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

// toFloat64 converts a value to float64.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}
