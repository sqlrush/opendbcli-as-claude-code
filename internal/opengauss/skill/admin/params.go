/*-------------------------------------------------------------------------
 *
 * params.go
 *	  params — ParamsSkill plus helpers (NewParamsSkill) used by the
 *	  admin package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/admin/params.go
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

type ParamsSkill struct{ driver db.Driver }

func NewParamsSkill(driver db.Driver) *ParamsSkill { return &ParamsSkill{driver: driver} }

func (s *ParamsSkill) Name() string                      { return "params" }
func (s *ParamsSkill) Description() string                { return "搜索OpenGauss参数" }
func (s *ParamsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ParamsSkill) Validate(_ skill.Params) error      { return nil }
func (s *ParamsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/params <pattern>", Examples: []string{"/params shared_buffers", "/params work_mem"}}
}
func (s *ParamsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "params", Description: "Search OpenGauss parameters by pattern",
		Parameters: map[string]any{"name_filter": map[string]any{"type": "string", "description": "Parameter name filter"}}}
}

// curatedParams is the list shown when /params is called without a
// pattern — a 50-ish parameter whitelist that covers the settings a DBA
// most often needs to see at a glance. Spelling out these lets us avoid
// dumping all ~750 GUCs.
const curatedParams = `
  'shared_buffers', 'work_mem', 'maintenance_work_mem', 'effective_cache_size',
  'max_connections', 'superuser_reserved_connections',
  'wal_level', 'max_wal_size', 'min_wal_size', 'wal_buffers', 'wal_writer_delay',
  'checkpoint_timeout', 'checkpoint_completion_target', 'full_page_writes',
  'synchronous_commit', 'fsync', 'wal_compression',
  'autovacuum', 'autovacuum_max_workers', 'autovacuum_naptime',
  'autovacuum_vacuum_scale_factor', 'autovacuum_freeze_max_age',
  'max_parallel_workers', 'max_parallel_workers_per_gather',
  'archive_mode', 'archive_command', 'archive_timeout',
  'log_min_messages', 'log_min_duration_statement', 'log_checkpoints',
  'statement_timeout', 'idle_in_transaction_session_timeout',
  'password_encryption_type', 'enable_wdr_snapshot',
  'max_process_memory', 'local_syscache_threshold',
  'effective_io_concurrency', 'random_page_cost', 'seq_page_cost',
  'default_statistics_target', 'tcp_keepalives_idle', 'tcp_keepalives_interval',
  'listen_addresses', 'port', 'ssl',
  'client_encoding', 'datestyle', 'timezone', 'lc_messages'
`

func (s *ParamsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	pattern := params.StringOr("args", "")

	var sqlStr string
	var header string
	if pattern == "" {
		// Empty pattern → curated whitelist instead of 750-row dump.
		sqlStr = "SELECT name, setting, unit, short_desc, context FROM pg_settings WHERE name IN (" + curatedParams + ") ORDER BY name"
		header = "常用参数速查（未指定 pattern；用 /params <name> 搜索具体参数）"
	} else {
		if pattern[0] != '%' {
			pattern = "%" + pattern + "%"
		}
		sqlStr = fmt.Sprintf(
			"SELECT name, setting, unit, short_desc, context FROM pg_settings WHERE name LIKE '%s' ORDER BY name",
			pattern,
		)
		header = fmt.Sprintf("参数搜索 '%s'", pattern)
	}

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("未找到匹配参数 '%s'", pattern),
			Summary:  "0 matches",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("%s — %d 条", header, len(result.Rows)),
		Summary:  fmt.Sprintf("%d 条参数", len(result.Rows)),
	}, nil
}
