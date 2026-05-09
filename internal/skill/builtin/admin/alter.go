/*-------------------------------------------------------------------------
 *
 * alter.go
 *	  AlterSkill modifies Oracle system parameters via ALTER SYSTEM SET,
 *	  or shows current value and modifiability info when no value is
 *	  given.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/alter.go
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

const alterParamInfoSQL = `SELECT name, value, display_value, type,
       issys_modifiable, isses_modifiable,
       description
FROM v$parameter
WHERE name = :1`

// AlterSkill modifies Oracle system parameters via ALTER SYSTEM SET,
// or shows current value and modifiability info when no value is given.
type AlterSkill struct {
	driver db.Driver
}

// NewAlterSkill creates an AlterSkill backed by the given driver.
func NewAlterSkill(driver db.Driver) *AlterSkill {
	return &AlterSkill{driver: driver}
}

func (s *AlterSkill) Name() string        { return "alter" }
func (s *AlterSkill) Description() string { return "Alter Oracle system parameter" }
func (s *AlterSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelAdmin
}

func (s *AlterSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "alter",
		Description: "Alter Oracle system parameter or show current value",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Parameter name, optionally followed by new value",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *AlterSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "alter",
		Usage:   "/alter <param_name> [new_value]",
		Examples: []string{
			"/alter pga_aggregate_target",
			"/alter pga_aggregate_target 2G",
			"/alter open_cursors 500",
		},
		ArgCompletions: []string{
			"pga_aggregate_target", "sga_target", "sga_max_size",
			"db_cache_size", "shared_pool_size", "large_pool_size",
			"java_pool_size", "streams_pool_size",
			"open_cursors", "session_cached_cursors",
			"processes", "sessions",
			"cursor_sharing", "optimizer_mode", "optimizer_index_cost_adj",
			"db_file_multiblock_read_count", "db_files",
			"log_buffer", "sort_area_size", "hash_area_size",
			"parallel_max_servers", "parallel_min_servers",
			"undo_retention", "undo_tablespace",
			"archive_lag_target", "log_archive_max_processes",
			"result_cache_max_size", "inmemory_size",
			"temp_undo_enabled", "deferred_segment_creation",
		},
	}
}

func (s *AlterSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("请指定参数名, 如: /alter pga_aggregate_target [新值]")
	}
	return nil
}

func (s *AlterSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return nil, fmt.Errorf("请指定参数名")
	}

	paramName := strings.ToLower(parts[0])
	paramValue := ""
	if len(parts) > 1 {
		paramValue = strings.Join(parts[1:], " ")
	}

	info, err := s.queryParamInfo(ctx, paramName)
	if err != nil {
		return nil, err
	}

	if paramValue == "" {
		return s.showParamInfo(paramName, info)
	}
	return s.alterParam(ctx, paramName, paramValue, info)
}

// paramInfo holds the queried v$parameter row.
type paramInfo struct {
	name            string
	value           string
	displayValue    string
	paramType       string
	issysModifiable string
	issesModifiable string
	description     string
}

func (s *AlterSkill) queryParamInfo(ctx context.Context, paramName string) (paramInfo, error) {
	qr, err := s.driver.Query(ctx, alterParamInfoSQL, paramName)
	if err != nil {
		return paramInfo{}, fmt.Errorf("查询参数信息: %w", err)
	}
	if len(qr.Rows) == 0 {
		return paramInfo{}, fmt.Errorf("参数 '%s' 不存在, 使用 /params %s 搜索", paramName, paramName)
	}

	row := qr.Rows[0]
	return paramInfo{
		name:            asString(row[0]),
		value:           asString(row[1]),
		displayValue:    asString(row[2]),
		paramType:       asString(row[3]),
		issysModifiable: asString(row[4]),
		issesModifiable: asString(row[5]),
		description:     asString(row[6]),
	}, nil
}

func (s *AlterSkill) showParamInfo(paramName string, info paramInfo) (*skill.Result, error) {
	typeName := paramTypeName(info.paramType)
	modifiable, scope := modifiableInfo(info.issysModifiable)

	displayVal := info.displayValue
	if isMemoryParam(paramName) {
		displayVal = humanWithRaw(info.displayValue)
	}

	lines := []string{
		fmt.Sprintf("当前值:    %s", displayVal),
		fmt.Sprintf("类型:      %s", typeName),
		fmt.Sprintf("可修改:    %s %s", modifiable, modifiableLabel(info.issysModifiable)),
		fmt.Sprintf("SCOPE:     %s", scope),
	}
	if info.description != "" {
		lines = append(lines, fmt.Sprintf("说明:      %s", info.description))
	}
	lines = append(lines, "", fmt.Sprintf("修改: /alter %s <新值>", paramName))

	sections := []format.PanelSection{{Lines: lines}}
	rendered := format.Panel("Parameter: "+paramName, sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("parameter %s = %s", info.name, info.displayValue),
	}, nil
}

func (s *AlterSkill) alterParam(ctx context.Context, paramName, paramValue string, info paramInfo) (*skill.Result, error) {
	scope := determineScope(info.issysModifiable)

	alterSQL := fmt.Sprintf("ALTER SYSTEM SET %s = %s SCOPE=%s", paramName, paramValue, scope)

	if _, err := s.driver.Exec(ctx, alterSQL); err != nil {
		return nil, fmt.Errorf("执行 ALTER SYSTEM SET: %w", err)
	}

	const (
		ansiReset = "\033[0m"
		ansiGreen = "\033[32m"
		ansiDim   = "\033[90m"
		ansiBold  = "\033[1m"
		ansiRed   = "\033[1;31m"
	)

	oldDisplay := info.displayValue
	if isMemoryParam(paramName) {
		oldDisplay = humanWithRaw(info.displayValue)
	}

	infoLines := []string{
		fmt.Sprintf("参数名:  %s%s%s", ansiBold, paramName, ansiReset),
		fmt.Sprintf("修改前:  %s", oldDisplay),
		fmt.Sprintf("目标值:  %s", paramValue),
		fmt.Sprintf("SCOPE:   %s", scope),
	}

	resultLines := []string{
		fmt.Sprintf("%s✓%s %s%s%s", ansiGreen, ansiReset, ansiDim, alterSQL, ansiReset),
	}

	// Re-query to verify the new value.
	if info.issysModifiable != "FALSE" {
		newInfo, err := s.queryParamInfo(ctx, paramName)
		if err == nil {
			if newInfo.displayValue != info.displayValue {
				newDisplay := newInfo.displayValue
				if isMemoryParam(paramName) {
					newDisplay = humanWithRaw(newInfo.displayValue)
				}
				resultLines = append(resultLines,
					fmt.Sprintf("验证:    %s → %s%s%s %s✓%s",
						oldDisplay, ansiBold, newDisplay, ansiReset, ansiGreen, ansiReset))
			} else {
				resultLines = append(resultLines,
					fmt.Sprintf("%s验证: 值未变化, 可能需延迟生效或检查输入%s", ansiRed, ansiReset))
			}
		}
	} else {
		resultLines = append(resultLines,
			fmt.Sprintf("%s⚠ 参数已写入 SPFILE, 需重启数据库生效%s", ansiRed, ansiReset))
	}

	sections := []format.PanelSection{
		{Lines: infoLines},
		{Header: "Result", Lines: resultLines},
	}
	rendered := format.Panel("参数修改", sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("ALTER SYSTEM SET %s = %s SCOPE=%s", paramName, paramValue, scope),
	}, nil
}

// asString safely converts an any value to string.
func asString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// paramTypeName maps Oracle v$parameter type codes to human-readable names.
func paramTypeName(typeCode string) string {
	switch typeCode {
	case "1":
		return "布尔值"
	case "2":
		return "字符串"
	case "3":
		return "整数"
	case "4":
		return "参数文件"
	case "5":
		return "保留"
	case "6":
		return "大整数"
	default:
		return "未知(" + typeCode + ")"
	}
}

// modifiableInfo returns a display indicator and scope string for info mode.
func modifiableInfo(issysModifiable string) (indicator string, scope string) {
	switch issysModifiable {
	case "IMMEDIATE":
		return "✓", "BOTH"
	case "DEFERRED":
		return "✓ (延迟)", "BOTH"
	case "FALSE":
		return "✗", "SPFILE"
	default:
		return "?", issysModifiable
	}
}

// determineScope returns the SCOPE clause value for ALTER SYSTEM SET.
func determineScope(issysModifiable string) string {
	switch issysModifiable {
	case "IMMEDIATE", "DEFERRED":
		return "BOTH"
	default:
		return "SPFILE"
	}
}

// modifiableLabel returns a human-readable label for the current modifiability.
func modifiableLabel(issysModifiable string) string {
	switch issysModifiable {
	case "IMMEDIATE":
		return "可在线修改"
	case "DEFERRED":
		return "可在线修改(延迟生效)"
	case "FALSE":
		return "不可在线修改"
	default:
		return issysModifiable
	}
}

// isMemoryParam returns true if the parameter is a memory-size parameter.
func isMemoryParam(name string) bool {
	memParams := []string{
		"sga_target", "sga_max_size", "pga_aggregate_target",
		"db_cache_size", "shared_pool_size", "large_pool_size",
		"java_pool_size", "streams_pool_size", "result_cache_max_size",
		"inmemory_size", "log_buffer", "sort_area_size", "hash_area_size",
	}
	for _, p := range memParams {
		if name == p {
			return true
		}
	}
	return false
}

// humanWithRaw formats "2G (2147483648)" style display for memory parameters.
// If the value is a pure number, converts to human-readable with raw value.
// If it already has a unit suffix, returns as-is.
func humanWithRaw(displayValue string) string {
	f, err := strconv.ParseFloat(displayValue, 64)
	if err != nil {
		// Not a pure number — return as-is (already has unit or is non-numeric).
		return displayValue
	}
	if f == 0 {
		return "0"
	}
	human := format.HumanBytes(f)
	return fmt.Sprintf("%s (%s)", human, displayValue)
}
