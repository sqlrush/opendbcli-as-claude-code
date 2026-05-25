/*-------------------------------------------------------------------------
 *
 * binlog.go
 *	  BinlogSkill shows binary log status and configuration.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/binlog.go
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

// BinlogSkill shows binary log status and configuration.
type BinlogSkill struct {
	driver db.Driver
}

// NewBinlogSkill creates a BinlogSkill backed by the given driver.
func NewBinlogSkill(driver db.Driver) *BinlogSkill {
	return &BinlogSkill{driver: driver}
}

func (s *BinlogSkill) Name() string                       { return "binlog" }
func (s *BinlogSkill) Description() string                { return "Binary Log 状态" }
func (s *BinlogSkill) Category() string                   { return "monitor" }
func (s *BinlogSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *BinlogSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/binlog", Examples: []string{"/binlog"}}
}

func (s *BinlogSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "binlog",
		Description: "Show binary log status, log list, and expiry configuration",
	}
}

func (s *BinlogSkill) Validate(_ skill.Params) error { return nil }

func (s *BinlogSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	sections := []format.PanelSection{}

	// Binary log status (current position)
	statusResult, err := s.queryBinlogStatus(ctx)
	if err == nil && len(statusResult.Rows) > 0 {
		sections = append(sections, s.buildStatusSection(statusResult))
	}

	// Expiry settings
	expiryResult, err := s.driver.Query(ctx, "SHOW VARIABLES LIKE 'expire_logs%'")
	if err == nil {
		expiryMap := rowsToMap(expiryResult)

		// Also check binlog_expire_logs_seconds (MySQL 8.0+)
		binlogExpResult, err := s.driver.Query(ctx, "SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'")
		if err == nil {
			for k, v := range rowsToMap(binlogExpResult) {
				expiryMap[k] = v
			}
		}

		var expiryLines []string
		for key, val := range expiryMap {
			expiryLines = append(expiryLines, fmt.Sprintf("%-30s : %s", key, val))
		}
		if len(expiryLines) > 0 {
			sections = append(sections, format.PanelSection{
				Header: "Expiry Settings",
				Lines:  expiryLines,
			})
		}
	}

	// Binary logs list
	logsResult, err := s.driver.Query(ctx, "SHOW BINARY LOGS")
	if err == nil && len(logsResult.Rows) > 0 {
		sections = append(sections, s.buildLogsSection(logsResult))
	}

	if len(sections) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "Binary logging 未启用或无权限查看",
			Summary:  "binary logging not enabled",
		}, nil
	}

	rendered := format.Panel("Binary Log", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "binary log status",
	}, nil
}

func (s *BinlogSkill) queryBinlogStatus(ctx context.Context) (*db.QueryResult, error) {
	// MySQL 8.4+ uses SHOW BINARY LOG STATUS
	result, err := s.driver.Query(ctx, "SHOW BINARY LOG STATUS")
	if err != nil {
		// Fallback for older MySQL
		result, err = s.driver.Query(ctx, "SHOW MASTER STATUS")
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *BinlogSkill) buildStatusSection(result *db.QueryResult) format.PanelSection {
	var lines []string
	if len(result.Rows) > 0 && len(result.Columns) > 0 {
		row := result.Rows[0]
		for i, col := range result.Columns {
			if i < len(row) {
				lines = append(lines, fmt.Sprintf("%-20s : %s", col, asStr(row[i])))
			}
		}
	}
	return format.PanelSection{
		Header: "Current Position",
		Lines:  lines,
	}
}

func (s *BinlogSkill) buildLogsSection(result *db.QueryResult) format.PanelSection {
	var lines []string
	var totalSizeMB float64

	for _, row := range result.Rows {
		name := ""
		sizeStr := ""
		if len(row) >= 1 {
			name = asStr(row[0])
		}
		if len(row) >= 2 {
			sizeBytes := parseFloat(asStr(row[1]))
			sizeMB := sizeBytes / 1048576
			totalSizeMB += sizeMB
			sizeStr = fmt.Sprintf("%.2f MB", sizeMB)
		}
		lines = append(lines, fmt.Sprintf("%-30s  %s", name, sizeStr))
	}

	lines = append(lines, fmt.Sprintf("%-30s  %.2f MB", "TOTAL", totalSizeMB))
	lines = append(lines, fmt.Sprintf("Files: %d", len(result.Rows)))

	return format.PanelSection{
		Header: "Log Files",
		Lines:  lines,
	}
}
