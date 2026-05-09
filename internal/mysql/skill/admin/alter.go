/*-------------------------------------------------------------------------
 *
 * alter.go
 *	  AlterSkill modifies a MySQL global system variable.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/admin/alter.go
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
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// AlterSkill modifies a MySQL global system variable.
type AlterSkill struct {
	driver db.Driver
}

// NewAlterSkill creates an AlterSkill backed by the given driver.
func NewAlterSkill(driver db.Driver) *AlterSkill {
	return &AlterSkill{driver: driver}
}

func (s *AlterSkill) Name() string                       { return "alter" }
func (s *AlterSkill) Description() string                { return "修改系统变量" }
func (s *AlterSkill) Category() string                   { return "admin" }
func (s *AlterSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelOperator }

func (s *AlterSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage: "/alter <variable> <value>",
		Examples: []string{
			"/alter max_connections 500",
			"/alter innodb_buffer_pool_size 2G",
			"/alter long_query_time 1",
		},
		ArgCompletions: []string{
			"max_connections", "innodb_buffer_pool_size",
			"long_query_time", "slow_query_log",
			"innodb_flush_log_at_trx_commit", "sync_binlog",
			"wait_timeout", "interactive_timeout",
			"thread_cache_size", "table_open_cache",
			"sort_buffer_size", "join_buffer_size",
			"tmp_table_size", "max_heap_table_size",
		},
	}
}

func (s *AlterSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "alter",
		Description: "Modify a MySQL global system variable (SET GLOBAL)",
		Parameters: map[string]any{
			"args": map[string]any{
				"type":        "string",
				"description": "Variable name followed by new value, e.g. 'max_connections 500'",
			},
		},
	}
}

func (s *AlterSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("请指定变量名和值, 如: /alter max_connections 500")
	}
	return nil
}

func (s *AlterSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	parts := strings.Fields(args)
	if len(parts) < 1 {
		return &skill.Result{Type: skill.ResultError, Summary: "usage: /alter <variable> <value>"}, nil
	}

	varName := parts[0]

	// Show current value if no value provided.
	if len(parts) < 2 {
		return s.showCurrent(ctx, varName)
	}

	newValue := strings.Join(parts[1:], " ")
	return s.alterVariable(ctx, varName, newValue)
}

func (s *AlterSkill) showCurrent(ctx context.Context, varName string) (*skill.Result, error) {
	sqlStr := fmt.Sprintf("SELECT VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME = '%s'", varName)
	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{Type: skill.ResultError, Summary: fmt.Sprintf("变量 '%s' 不存在", varName)}, nil
	}

	currentValue := asString(result.Rows[0][0])
	displayValue := mysqlHumanDisplay(currentValue, varName)

	sections := []format.PanelSection{{Lines: []string{
		fmt.Sprintf("当前值: %s", displayValue),
		"",
		fmt.Sprintf("修改: /alter %s <新值>", varName),
	}}}
	rendered := format.Panel("Variable: "+varName, sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%s = %s", varName, currentValue),
	}, nil
}

func (s *AlterSkill) alterVariable(ctx context.Context, varName, newValue string) (*skill.Result, error) {
	// Query current value first.
	sqlStr := fmt.Sprintf("SELECT VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME = '%s'", varName)
	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	oldValue := "(unknown)"
	if len(result.Rows) > 0 {
		oldValue = asString(result.Rows[0][0])
	}

	// Execute SET GLOBAL.
	setSQL := fmt.Sprintf("SET GLOBAL %s = %s", varName, newValue)
	_, err = s.driver.Exec(ctx, setSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	// Verify new value.
	result, err = s.driver.Query(ctx, sqlStr)
	verifiedValue := newValue
	if err == nil && len(result.Rows) > 0 {
		verifiedValue = asString(result.Rows[0][0])
	}

	const (
		ansiReset = "\033[0m"
		ansiGreen = "\033[32m"
		ansiBold  = "\033[1m"
		ansiDim   = "\033[90m"
	)

	infoLines := []string{
		fmt.Sprintf("变量名:  %s%s%s", ansiBold, varName, ansiReset),
		fmt.Sprintf("修改前:  %s", mysqlHumanDisplay(oldValue, varName)),
		fmt.Sprintf("修改后:  %s%s%s", ansiBold, mysqlHumanDisplay(verifiedValue, varName), ansiReset),
	}

	resultLines := []string{
		fmt.Sprintf("%s✓%s %s%s%s", ansiGreen, ansiReset, ansiDim, setSQL, ansiReset),
	}

	sections := []format.PanelSection{
		{Lines: infoLines},
		{Header: "Result", Lines: resultLines},
	}
	rendered := format.Panel("变量修改", sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("SET GLOBAL %s = %s (was: %s)", varName, verifiedValue, oldValue),
	}, nil
}

// asString safely converts any value to string.
func asString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// mysqlMemoryVars are MySQL variables that store byte values.
var mysqlMemoryVars = map[string]bool{
	"innodb_buffer_pool_size": true, "innodb_log_buffer_size": true,
	"innodb_log_file_size": true, "key_buffer_size": true,
	"sort_buffer_size": true, "join_buffer_size": true,
	"read_buffer_size": true, "read_rnd_buffer_size": true,
	"tmp_table_size": true, "max_heap_table_size": true,
	"bulk_insert_buffer_size": true, "myisam_sort_buffer_size": true,
	"innodb_sort_buffer_size": true, "binlog_cache_size": true,
}

// mysqlHumanDisplay formats "16G (17179869184)" for memory vars, raw otherwise.
func mysqlHumanDisplay(value, varName string) string {
	if !mysqlMemoryVars[varName] {
		return value
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || f == 0 {
		return value
	}
	return fmt.Sprintf("%s (%s)", format.HumanBytes(f), value)
}
