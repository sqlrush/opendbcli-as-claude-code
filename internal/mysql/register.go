/*-------------------------------------------------------------------------
 *
 * register.go
 *	  DriverFactory creates a MySQL driver from a ConnectionConfig.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/register.go
 *
 *-------------------------------------------------------------------------
 */
package mysql

import (
	"path/filepath"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	mysqldriver "github.com/sqlrush/opendb/internal/mysql/driver"
	"github.com/sqlrush/opendb/internal/mysql/skill/admin"
	"github.com/sqlrush/opendb/internal/mysql/skill/ai"
	"github.com/sqlrush/opendb/internal/mysql/skill/monitor"
	"github.com/sqlrush/opendb/internal/mysql/skill/query"
	"github.com/sqlrush/opendb/internal/mysql/skill/schema"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
)

// DriverFactory creates a MySQL driver from a ConnectionConfig.
func DriverFactory(cfg db.ConnectionConfig) (db.Driver, error) {
	return mysqldriver.NewDriver(cfg)
}

// RegisterSkills registers all MySQL-specific skills into the registry.
func RegisterSkills(
	registry *skill.Registry,
	driver db.Driver,
	connMgr *connection.Manager,
	history *session.SessionHistory,
	cfg *config.Config,
	modelMgr *model.Manager,
	opendbDir string,
) {
	reg := func(s skill.Skill) { registry.RegisterForDB("mysql", s) }

	// Monitor
	reg(monitor.NewSessionsSkill(driver))
	reg(monitor.NewActiveSessionsSkill(driver))
	reg(monitor.NewLocksSkill(driver))
	reg(monitor.NewHealthSkill(driver))
	reg(monitor.NewWaitsSkill(driver))
	reg(monitor.NewBlockTreeSkill(driver))
	reg(monitor.NewBufferPoolSkill(driver))
	reg(monitor.NewReplicationSkill(driver))
	reg(monitor.NewResourceSkill(driver))
	reg(monitor.NewSegmentsSkill(driver))
	reg(monitor.NewOSSkill(driver))
	reg(monitor.NewIndexHealthSkill(driver))
	reg(monitor.NewUsersSkill(driver))
	reg(monitor.NewInnoDBSkill(driver))
	reg(monitor.NewDeadlockSkill(driver))
	reg(monitor.NewBinlogSkill(driver))
	reg(monitor.NewDBTopSkill(driver))
	reg(monitor.NewTraceSkill(connMgr.CurrentHost(), &cfg.Trace))

	// Performance
	perfSnapDir := filepath.Join(opendbDir, "scheduler")
	reg(monitor.NewPerfSnapSkill(driver, perfSnapDir))

	// Query
	reg(query.NewTopSQLSkill(driver))
	reg(query.NewSlowSQLSkill(driver))
	reg(query.NewSQLSkill(driver))
	reg(query.NewExplainSkill(driver))
	reg(query.NewASHSkill(driver))
	reg(query.NewMyErrSkill(driver))
	// /sqlfetch: 按 DIGEST 拉 SQL 文本（events_statements_summary_by_digest 归一化 + 占位符提示）
	reg(query.NewSQLFetchSkill(driver))

	// Admin
	reg(admin.NewKillSkill(driver))
	reg(admin.NewSpaceSkill(driver))
	reg(admin.NewParamsSkill(driver))
	reg(admin.NewAlterSkill(driver))
	reg(admin.NewAlertSkill(driver))
	reg(admin.NewBackupSkill(driver))
	reg(admin.NewJobsSkill(driver))
	reg(admin.NewGatherSkill(driver))

	// Schema
	reg(schema.NewTableInfoSkill(driver))
	reg(schema.NewIndexAdviseSkill(driver))

	// SQL Advisor — 暂时关闭, 见 oracle/register.go 注释
	// reg(query.NewSQLAdvisorSkill(driver))
	_ = query.NewSQLAdvisorSkill
}

// RegisterAISkills registers sentinel, diagnose, and rule skills.
// Returns sentinel and diagnose skills so REPL can use them for auto-start and alerts.
func RegisterAISkills(
	registry *skill.Registry,
	driver db.Driver,
	executor *skill.Executor,
	cfg *config.Config,
	modelMgr *model.Manager,
) (*ai.SentinelSkill, *ai.DiagnoseSkill) {
	reg := func(s skill.Skill) { registry.RegisterForDB("mysql", s) }

	sentinelSkill := ai.NewSentinelSkill(driver, cfg.Sentinel)
	reg(sentinelSkill)

	ruleSkill := ai.NewRuleSkill(sentinelSkill, driver)
	reg(ruleSkill)

	diagnoseSkill := ai.NewDiagnoseSkill(modelMgr, executor, registry, sentinelSkill, ruleSkill)
	reg(diagnoseSkill)

	return sentinelSkill, diagnoseSkill
}
