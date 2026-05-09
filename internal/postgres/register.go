/*-------------------------------------------------------------------------
 *
 * register.go
 *	  DriverFactory creates a PostgreSQL driver from a ConnectionConfig.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/register.go
 *
 *-------------------------------------------------------------------------
 */
package postgres

import (
	"path/filepath"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/model"
	pgdriver "github.com/sqlrush/opendb/internal/postgres/driver"
	"github.com/sqlrush/opendb/internal/postgres/skill/admin"
	"github.com/sqlrush/opendb/internal/postgres/skill/ai"
	"github.com/sqlrush/opendb/internal/postgres/skill/monitor"
	"github.com/sqlrush/opendb/internal/postgres/skill/query"
	"github.com/sqlrush/opendb/internal/postgres/skill/schema"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
)

// DriverFactory creates a PostgreSQL driver from a ConnectionConfig.
func DriverFactory(cfg db.ConnectionConfig) (db.Driver, error) {
	return pgdriver.NewDriver(cfg)
}

// RegisterSkills registers all PostgreSQL-specific skills into the registry.
func RegisterSkills(
	registry *skill.Registry,
	driver db.Driver,
	connMgr *connection.Manager,
	history *session.SessionHistory,
	cfg *config.Config,
	modelMgr *model.Manager,
	opendbDir string,
) {
	reg := func(s skill.Skill) { registry.RegisterForDB("postgres", s) }

	// Monitor
	reg(monitor.NewSessionsSkill(driver))
	reg(monitor.NewActiveSessionsSkill(driver))
	reg(monitor.NewLocksSkill(driver))
	reg(monitor.NewVacuumSkill(driver))
	reg(monitor.NewXIDSkill(driver))
	reg(monitor.NewHealthSkill(driver))
	reg(monitor.NewWaitsSkill(driver))
	reg(monitor.NewBlockTreeSkill(driver))
	reg(monitor.NewSharedBufsSkill(driver))
	reg(monitor.NewWALSkill(driver))
	reg(monitor.NewReplicationSkill(driver))
	reg(monitor.NewResourceSkill(driver))
	reg(monitor.NewSegmentsSkill(driver))
	reg(monitor.NewLongTxSkill(driver))
	reg(monitor.NewBloatSkill(driver))
	reg(monitor.NewOSSkill(driver))
	reg(monitor.NewIndexHealthSkill(driver))
	reg(monitor.NewUsersSkill(driver))
	reg(monitor.NewSlotsSkill(driver))
	reg(monitor.NewExtensionsSkill(driver))
	reg(monitor.NewPGDBTopSkill(driver))
	reg(monitor.NewPlanChangeSkill(driver))
	reg(monitor.NewSSLSkill(driver))
	reg(monitor.NewTriggersSkill(driver))
	reg(monitor.NewPrivilegesSkill(driver))
	reg(monitor.NewVacuumProgressSkill(driver))
	reg(monitor.NewTraceSkill(connMgr.CurrentHost(), &cfg.Trace))

	// Performance
	perfSnapDir := filepath.Join(opendbDir, "scheduler")
	reg(monitor.NewPGPerfSnapSkill(driver, perfSnapDir))

	// Query
	reg(query.NewTopSQLSkill(driver))
	reg(query.NewSlowSQLSkill(driver))
	reg(query.NewSQLSkill(driver))
	reg(query.NewExplainSkill(driver))
	// /sqlfetch: 按 queryid 拉 SQL 文本（pg_stat_statements 归一化版本 + 占位符提示）
	reg(query.NewSQLFetchSkill(driver))
	reg(query.NewASHSkill(driver))
	reg(query.NewPGErrSkill())
	// 暂时关闭 /sqladvisor 注册 — 见 oracle/register.go 注释.
	// reg(query.NewSQLAdvisorSkill(driver))
	_ = query.NewSQLAdvisorSkill

	// Admin
	reg(admin.NewKillSkill(driver))
	reg(admin.NewSpaceSkill(driver))
	reg(admin.NewParamsSkill(driver))
	reg(admin.NewAlterSkill(driver))
	reg(admin.NewAlertSkill(driver))
	reg(admin.NewBackupSkill(driver))
	reg(admin.NewJobsSkill(driver))
	reg(admin.NewGatherSkill(driver))
	reg(admin.NewCancelSkill(driver))

	// Schema
	reg(schema.NewTableInfoSkill(driver))
	reg(schema.NewIndexAdviseSkill(driver))
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
	reg := func(s skill.Skill) { registry.RegisterForDB("postgres", s) }

	sentinelSkill := ai.NewSentinelSkill(driver, cfg.Sentinel)
	reg(sentinelSkill)

	ruleSkill := ai.NewRuleSkill(sentinelSkill, driver)
	reg(ruleSkill)

	diagnoseSkill := ai.NewDiagnoseSkill(modelMgr, executor, registry, sentinelSkill, ruleSkill)
	reg(diagnoseSkill)

	return sentinelSkill, diagnoseSkill
}
