/*-------------------------------------------------------------------------
 *
 * bufferpool.go
 *	  BufferPoolSkill shows InnoDB Buffer Pool details.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/bufferpool.go
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

// BufferPoolSkill shows InnoDB Buffer Pool details.
type BufferPoolSkill struct {
	driver db.Driver
}

// NewBufferPoolSkill creates a BufferPoolSkill backed by the given driver.
func NewBufferPoolSkill(driver db.Driver) *BufferPoolSkill {
	return &BufferPoolSkill{driver: driver}
}

func (s *BufferPoolSkill) Name() string                       { return "bufferpool" }
func (s *BufferPoolSkill) Description() string                { return "InnoDB Buffer Pool 详情" }
func (s *BufferPoolSkill) Category() string                   { return "monitor" }
func (s *BufferPoolSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *BufferPoolSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/bufferpool", Examples: []string{"/bufferpool"}}
}

func (s *BufferPoolSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "bufferpool",
		Description: "Show InnoDB buffer pool usage, hit ratio, pages, and read/write stats",
	}
}

func (s *BufferPoolSkill) Validate(_ skill.Params) error { return nil }

func (s *BufferPoolSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	statusMap, err := s.queryBPStatus(ctx)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	varsMap, err := s.queryBPVars(ctx)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := s.buildPanel(statusMap, varsMap)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "InnoDB buffer pool details",
	}, nil
}

func (s *BufferPoolSkill) queryBPStatus(ctx context.Context) (map[string]string, error) {
	result, err := s.driver.Query(ctx, "SHOW GLOBAL STATUS LIKE 'Innodb_buffer_pool%'")
	if err != nil {
		return nil, fmt.Errorf("query buffer pool status: %w", err)
	}
	return rowsToMap(result), nil
}

func (s *BufferPoolSkill) queryBPVars(ctx context.Context) (map[string]string, error) {
	result, err := s.driver.Query(ctx, "SHOW GLOBAL VARIABLES LIKE 'innodb_buffer_pool%'")
	if err != nil {
		return nil, fmt.Errorf("query buffer pool variables: %w", err)
	}
	return rowsToMap(result), nil
}

func (s *BufferPoolSkill) buildPanel(status, vars map[string]string) string {
	poolSizeBytes := parseFloat(vars["innodb_buffer_pool_size"])
	poolSizeMB := poolSizeBytes / 1048576
	instances := vars["innodb_buffer_pool_instances"]

	pagesTotal := parseFloat(status["Innodb_buffer_pool_pages_total"])
	pagesFree := parseFloat(status["Innodb_buffer_pool_pages_free"])
	pagesDirty := parseFloat(status["Innodb_buffer_pool_pages_dirty"])
	pagesData := parseFloat(status["Innodb_buffer_pool_pages_data"])
	pagesMisc := parseFloat(status["Innodb_buffer_pool_pages_misc"])

	usagePct := safePct(pagesTotal-pagesFree, pagesTotal)
	dirtyPct := safePct(pagesDirty, pagesTotal)

	readReq := parseFloat(status["Innodb_buffer_pool_read_requests"])
	reads := parseFloat(status["Innodb_buffer_pool_reads"])
	hitPct := 0.0
	if readReq > 0 {
		hitPct = (1 - reads/readReq) * 100
	}

	writeReq := parseFloat(status["Innodb_buffer_pool_write_requests"])
	bytesData := parseFloat(status["Innodb_buffer_pool_bytes_data"])
	bytesDirty := parseFloat(status["Innodb_buffer_pool_bytes_dirty"])

	sections := []format.PanelSection{
		{
			Header: "Configuration",
			Lines: []string{
				fmt.Sprintf("Pool Size       : %.0f MB", poolSizeMB),
				fmt.Sprintf("Instances       : %s", instances),
			},
		},
		{
			Header: "Pages",
			Lines: []string{
				fmt.Sprintf("Total           : %.0f", pagesTotal),
				fmt.Sprintf("Data            : %.0f", pagesData),
				fmt.Sprintf("Free            : %.0f", pagesFree),
				fmt.Sprintf("Dirty           : %.0f (%.1f%%)", pagesDirty, dirtyPct),
				fmt.Sprintf("Misc            : %.0f", pagesMisc),
				fmt.Sprintf("Usage           : %.1f%%  %s", usagePct, format.ProgressBar(usagePct, 20)),
			},
		},
		{
			Header: "I/O",
			Lines: []string{
				fmt.Sprintf("Read Requests   : %.0f", readReq),
				fmt.Sprintf("Disk Reads      : %.0f", reads),
				fmt.Sprintf("Hit Ratio       : %.2f%%", hitPct),
				fmt.Sprintf("Write Requests  : %.0f", writeReq),
				fmt.Sprintf("Bytes Data      : %s", format.FormatBytes(bytesData/1048576)),
				fmt.Sprintf("Bytes Dirty     : %s", format.FormatBytes(bytesDirty/1048576)),
			},
		},
	}

	return format.Panel("InnoDB Buffer Pool", sections)
}
