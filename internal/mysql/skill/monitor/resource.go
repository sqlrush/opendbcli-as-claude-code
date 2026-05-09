/*-------------------------------------------------------------------------
 *
 * resource.go
 *	  ResourceSkill shows MySQL resource usage vs limits.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/resource.go
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

// ResourceSkill shows MySQL resource usage vs limits.
type ResourceSkill struct {
	driver db.Driver
}

// NewResourceSkill creates a ResourceSkill backed by the given driver.
func NewResourceSkill(driver db.Driver) *ResourceSkill {
	return &ResourceSkill{driver: driver}
}

func (s *ResourceSkill) Name() string                       { return "resource" }
func (s *ResourceSkill) Description() string                { return "资源使用 vs 限制" }
func (s *ResourceSkill) Category() string                   { return "monitor" }
func (s *ResourceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ResourceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/resource", Examples: []string{"/resource"}}
}

func (s *ResourceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "resource",
		Description: "Show MySQL resource limits vs current usage: connections, table_open_cache, thread_cache",
	}
}

func (s *ResourceSkill) Validate(_ skill.Params) error { return nil }

func (s *ResourceSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	statusMap, err := s.queryStatus(ctx)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	varsMap, err := s.queryVars(ctx)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := s.buildPanel(statusMap, varsMap)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "resource usage vs limits",
	}, nil
}

func (s *ResourceSkill) queryStatus(ctx context.Context) (map[string]string, error) {
	result, err := s.driver.Query(ctx, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, fmt.Errorf("SHOW GLOBAL STATUS: %w", err)
	}
	return rowsToMap(result), nil
}

func (s *ResourceSkill) queryVars(ctx context.Context) (map[string]string, error) {
	result, err := s.driver.Query(ctx, "SHOW GLOBAL VARIABLES")
	if err != nil {
		return nil, fmt.Errorf("SHOW GLOBAL VARIABLES: %w", err)
	}
	return rowsToMap(result), nil
}

func (s *ResourceSkill) buildPanel(status, vars map[string]string) string {
	maxConn := parseFloat(vars["max_connections"])
	curConn := parseFloat(status["Threads_connected"])
	maxUsedConn := parseFloat(status["Max_used_connections"])
	connPct := safePct(curConn, maxConn)
	peakPct := safePct(maxUsedConn, maxConn)

	tableOpenCache := parseFloat(vars["table_open_cache"])
	openTables := parseFloat(status["Open_tables"])
	openedTables := parseFloat(status["Opened_tables"])
	tablePct := safePct(openTables, tableOpenCache)

	threadCacheSize := parseFloat(vars["thread_cache_size"])
	threadsCached := parseFloat(status["Threads_cached"])
	threadsCreated := parseFloat(status["Threads_created"])
	threadCachePct := safePct(threadsCached, threadCacheSize)

	openFilesLimit := parseFloat(vars["open_files_limit"])
	openFiles := parseFloat(status["Open_files"])
	filesPct := safePct(openFiles, openFilesLimit)

	tmpTableSize := parseFloat(vars["tmp_table_size"])
	tmpTableSizeMB := tmpTableSize / 1048576
	createdTmpTables := parseFloat(status["Created_tmp_tables"])
	createdTmpDiskTables := parseFloat(status["Created_tmp_disk_tables"])
	tmpDiskPct := safePct(createdTmpDiskTables, createdTmpTables)

	sections := []format.PanelSection{
		{
			Header: "Connections",
			Lines: []string{
				fmt.Sprintf("Current / Max   : %.0f / %.0f (%.1f%%)", curConn, maxConn, connPct),
				fmt.Sprintf("Peak            : %.0f (%.1f%%)", maxUsedConn, peakPct),
				fmt.Sprintf("                  %s", format.ProgressBar(connPct, 30)),
			},
		},
		{
			Header: "Table Cache",
			Lines: []string{
				fmt.Sprintf("Open / Cache    : %.0f / %.0f (%.1f%%)", openTables, tableOpenCache, tablePct),
				fmt.Sprintf("Opened (total)  : %.0f", openedTables),
				fmt.Sprintf("                  %s", format.ProgressBar(tablePct, 30)),
			},
		},
		{
			Header: "Thread Cache",
			Lines: []string{
				fmt.Sprintf("Cached / Size   : %.0f / %.0f (%.1f%%)", threadsCached, threadCacheSize, threadCachePct),
				fmt.Sprintf("Created (total) : %.0f", threadsCreated),
				fmt.Sprintf("                  %s", format.ProgressBar(threadCachePct, 30)),
			},
		},
		{
			Header: "Files",
			Lines: []string{
				fmt.Sprintf("Open / Limit    : %.0f / %.0f (%.1f%%)", openFiles, openFilesLimit, filesPct),
				fmt.Sprintf("                  %s", format.ProgressBar(filesPct, 30)),
			},
		},
		{
			Header: "Temp Tables",
			Lines: []string{
				fmt.Sprintf("tmp_table_size  : %.0f MB", tmpTableSizeMB),
				fmt.Sprintf("Created (mem)   : %.0f", createdTmpTables),
				fmt.Sprintf("Created (disk)  : %.0f (%.1f%% disk)", createdTmpDiskTables, tmpDiskPct),
			},
		},
	}

	return format.Panel("Resource Usage", sections)
}
