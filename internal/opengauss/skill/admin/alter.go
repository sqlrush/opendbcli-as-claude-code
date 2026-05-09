/*-------------------------------------------------------------------------
 *
 * alter.go
 *	  AlterSkill modifies OpenGauss system parameters via ALTER SYSTEM
 *	  SET.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/admin/alter.go
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

const alterParamInfoSQL = `SELECT
  name, setting, unit, context, short_desc
FROM pg_settings
WHERE name = '%s'`

// AlterSkill modifies OpenGauss system parameters via ALTER SYSTEM SET.
type AlterSkill struct {
	driver db.Driver
}

// NewAlterSkill creates an AlterSkill backed by the given driver.
func NewAlterSkill(driver db.Driver) *AlterSkill {
	return &AlterSkill{driver: driver}
}

func (s *AlterSkill) Name() string                       { return "alter" }
func (s *AlterSkill) Description() string                { return "修改系统参数 (ALTER SYSTEM)" }
func (s *AlterSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelOperator }

func (s *AlterSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage: "/alter <param_name> [new_value]",
		Examples: []string{
			"/alter work_mem",
			"/alter work_mem 64MB",
			"/alter shared_buffers 2GB",
		},
		ArgCompletions: []string{
			"shared_buffers", "effective_cache_size", "work_mem",
			"maintenance_work_mem", "max_connections",
			"max_wal_senders", "max_worker_processes",
			"checkpoint_completion_target", "wal_buffers",
			"random_page_cost", "effective_io_concurrency",
			"autovacuum_max_workers", "log_min_duration_statement",
			"statement_timeout", "idle_in_transaction_session_timeout",
		},
	}
}

func (s *AlterSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "alter",
		Description: "Alter a OpenGauss system parameter via ALTER SYSTEM SET, or show current value",
		Parameters: map[string]any{
			"args": map[string]any{
				"type":        "string",
				"description": "Parameter name, optionally followed by new value",
			},
		},
	}
}

func (s *AlterSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("请指定参数名, 如: /alter work_mem [新值]")
	}
	return nil
}

func (s *AlterSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return &skill.Result{Type: skill.ResultError, Summary: "usage: /alter <param> [value]"}, nil
	}

	paramName := strings.ToLower(parts[0])
	paramValue := ""
	if len(parts) > 1 {
		paramValue = strings.Join(parts[1:], " ")
	}

	// Query current param info
	info, err := s.queryParam(ctx, paramName)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	if paramValue == "" {
		return s.showParam(paramName, info)
	}
	return s.alterParam(ctx, paramName, paramValue, info)
}

type pgParamInfo struct {
	name      string
	setting   string
	unit      string
	context   string
	shortDesc string
}

func (s *AlterSkill) queryParam(ctx context.Context, name string) (pgParamInfo, error) {
	sqlStr := fmt.Sprintf(alterParamInfoSQL, name)
	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return pgParamInfo{}, fmt.Errorf("查询参数: %w", err)
	}
	if len(result.Rows) == 0 {
		return pgParamInfo{}, fmt.Errorf("参数 '%s' 不存在, 使用 /params %s 搜索", name, name)
	}
	row := result.Rows[0]
	return pgParamInfo{
		name:      asStr(row[0]),
		setting:   asStr(row[1]),
		unit:      asStr(row[2]),
		context:   asStr(row[3]),
		shortDesc: asStr(row[4]),
	}, nil
}

func (s *AlterSkill) showParam(paramName string, info pgParamInfo) (*skill.Result, error) {
	unitStr := ""
	if info.unit != "" {
		unitStr = " " + info.unit
	}

	lines := []string{
		fmt.Sprintf("当前值:    %s%s", info.setting, unitStr),
		fmt.Sprintf("Context:   %s | %s", info.context, contextDesc(info.context)),
	}
	if info.shortDesc != "" {
		lines = append(lines, fmt.Sprintf("说明:      %s", info.shortDesc))
	}
	lines = append(lines, "", fmt.Sprintf("修改: /alter %s <新值>", paramName))

	sections := []format.PanelSection{{Lines: lines}}
	rendered := format.Panel("Parameter: "+paramName, sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%s = %s%s", info.name, info.setting, unitStr),
	}, nil
}

func (s *AlterSkill) alterParam(ctx context.Context, paramName, paramValue string, info pgParamInfo) (*skill.Result, error) {
	// Write to postgresql.auto.conf
	alterSQL := fmt.Sprintf("ALTER SYSTEM SET %s = '%s'", paramName, paramValue)
	_, err := s.driver.Exec(ctx, alterSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	const (
		ansiReset = "\033[0m"
		ansiGreen = "\033[32m"
		ansiBold  = "\033[1m"
		ansiDim   = "\033[90m"
		ansiWarn  = "\033[1;33m"
	)

	infoLines := []string{
		fmt.Sprintf("参数名:  %s%s%s", ansiBold, paramName, ansiReset),
		fmt.Sprintf("修改前:  %s", info.setting),
		fmt.Sprintf("目标值:  %s", paramValue),
	}

	resultLines := []string{
		fmt.Sprintf("%s✓%s %s%s%s", ansiGreen, ansiReset, ansiDim, alterSQL, ansiReset),
	}

	// Reload if sighup context
	if info.context == "sighup" || info.context == "superuser" {
		_, reloadErr := s.driver.Query(ctx, "SELECT pg_reload_conf()")
		if reloadErr == nil {
			resultLines = append(resultLines,
				fmt.Sprintf("%s✓%s pg_reload_conf() 已执行", ansiGreen, ansiReset))

			// Verify new value
			newInfo, verifyErr := s.queryParam(ctx, paramName)
			if verifyErr == nil {
				if newInfo.setting != info.setting {
					resultLines = append(resultLines,
						fmt.Sprintf("验证:    %s → %s%s%s %s✓%s",
							info.setting, ansiBold, newInfo.setting, ansiReset, ansiGreen, ansiReset))
				} else {
					resultLines = append(resultLines,
						fmt.Sprintf("%s注意: 值未变化, 可能需要重启或检查输入%s", ansiWarn, ansiReset))
				}
			}
		} else {
			resultLines = append(resultLines,
				fmt.Sprintf("%s⚠ pg_reload_conf() 失败: %s%s", ansiWarn, reloadErr, ansiReset))
		}
	} else if info.context == "postmaster" {
		resultLines = append(resultLines,
			fmt.Sprintf("%s⚠ context=postmaster, 需重启 OpenGauss 生效%s", ansiWarn, ansiReset))
	} else {
		resultLines = append(resultLines,
			fmt.Sprintf("context=%s, 参数已写入 postgresql.auto.conf", info.context))
	}

	sections := []format.PanelSection{
		{Lines: infoLines},
		{Header: "Result", Lines: resultLines},
	}
	rendered := format.Panel("参数修改", sections)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("ALTER SYSTEM SET %s = '%s'", paramName, paramValue),
	}, nil
}

func contextDesc(ctx string) string {
	switch ctx {
	case "sighup":
		return "可在线 reload 生效"
	case "superuser":
		return "超级用户可在线修改"
	case "user":
		return "用户级别可修改"
	case "postmaster":
		return "需重启生效"
	case "internal":
		return "内部参数, 不可修改"
	default:
		return ctx
	}
}

// asStr safely converts any value to string.
func asStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
