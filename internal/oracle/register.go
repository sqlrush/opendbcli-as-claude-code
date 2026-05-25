/*-------------------------------------------------------------------------
 *
 * register.go
 *	  DriverFactory creates an Oracle driver from a ConnectionConfig.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/register.go
 *
 *-------------------------------------------------------------------------
 */
package oracle

import (
	"path/filepath"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	oracledriver "github.com/sqlrush/opendb/internal/oracle/driver"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/builtin/admin"
	"github.com/sqlrush/opendb/internal/oracle/skill/ai"
	"github.com/sqlrush/opendb/internal/oracle/skill/monitor"
	"github.com/sqlrush/opendb/internal/oracle/skill/query"
	"github.com/sqlrush/opendb/internal/oracle/skill/schema"
)

// DriverFactory creates an Oracle driver from a ConnectionConfig.
func DriverFactory(cfg db.ConnectionConfig) (db.Driver, error) {
	return oracledriver.NewDriver(cfg)
}

// RegisterSkills registers all Oracle-specific skills into the registry.
func RegisterSkills(
	registry *skill.Registry,
	driver db.Driver,
	connMgr *connection.Manager,
	history *session.SessionHistory,
	cfg *config.Config,
	modelMgr *model.Manager,
	opendbDir string,
) {
	reg := func(s skill.Skill) { registry.RegisterForDB("oracle", s) }

	// Admin — driver needed (Oracle-specific SQL)
	reg(admin.NewKillSkill(driver))
	reg(admin.NewSpaceSkill(driver))
	reg(admin.NewParamsSkill(driver))
	reg(admin.NewAlertSkill(driver))
	reg(admin.NewBackupSkill(driver))
	reg(admin.NewStandbySkill(driver))
	reg(admin.NewAlterSkill(driver))
	reg(admin.NewResizeSkill(driver))
	reg(admin.NewJobsSkill(driver))
	reg(admin.NewGatherSkill(driver))

	// Query (Oracle-specific SQL)
	reg(query.NewSlowSQLSkill(driver))
	reg(query.NewExplainSkill(driver))
	reg(query.NewSQLSkill(driver))
	reg(query.NewTopSQLSkill(driver))
	reg(query.NewAWRSkill(driver))
	reg(query.NewASHSkill(driver))
	reg(query.NewPlanHistorySkill(driver))
	reg(query.NewORASkill(driver))
	// /sqlfetch: 按 SQL_ID 拉 SQL 全文（V$SQL.SQL_FULLTEXT 通常带字面量）
	reg(query.NewSQLFetchSkill(driver))

	// /sqltune (M5.1): Oracle EXPLAIN PLAN + 10053 CBO trace MVP.
	// memStore=nil — memory injection optional, same as og/mysql/pg.
	reg(query.NewSQLTuneSkill(driver, modelMgr, nil))
	// 暂时关闭 /sqladvisor 注册 — 现有规则引擎版本被新的 /sqltune (规则+LLM 混合)
	// 取代, 不再向用户展示. 代码保留供 v0.2 anti-pattern 规则层复用.
	// reg(query.NewSQLAdvisorSkill(driver))
	_ = query.NewSQLAdvisorSkill // suppress unused import warning

	// Monitor (Oracle-specific SQL)
	reg(monitor.NewSessionsSkill(driver))
	reg(monitor.NewActiveSessionsSkill(driver))
	reg(monitor.NewWaitsSkill(driver))
	reg(monitor.NewLocksSkill(driver))
	reg(monitor.NewLatchesSkill(driver))
	reg(monitor.NewMutexesSkill(driver))
	reg(monitor.NewHealthSkill(driver))
	reg(monitor.NewDBTopSkill(driver))
	reg(monitor.NewTempSessSkill(driver))
	reg(monitor.NewUndoSessSkill(driver))
	reg(monitor.NewPGASkill(driver))
	reg(monitor.NewSGASkill(driver))
	reg(monitor.NewRedoSkill(driver))
	reg(monitor.NewFRASkill(driver))
	reg(monitor.NewASMSkill(driver))
	reg(monitor.NewSortUsageSkill(driver))
	reg(monitor.NewResourceSkill(driver))
	reg(monitor.NewBlockTreeSkill(driver))
	reg(monitor.NewSegmentsSkill(driver))
	reg(monitor.NewOSSkill(driver))
	reg(monitor.NewTraceSkill(connMgr.CurrentHost(), &cfg.Trace))

	// Schema (Oracle-specific SQL)
	reg(schema.NewTableInfoSkill(driver))
	reg(schema.NewIndexAdviseSkill(driver))

	// Performance + Health
	perfSnapDir := filepath.Join(opendbDir, "scheduler")
	reg(monitor.NewPerfSnapSkill(driver, perfSnapDir))
	reg(monitor.NewIndexHealthSkill(driver))
	reg(monitor.NewUsersSkill(driver))
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
	reg := func(s skill.Skill) { registry.RegisterForDB("oracle", s) }

	collector := dbtop.NewCollector(driver)
	sentinelSkill := ai.NewSentinelSkill(driver, collector, cfg.Sentinel)
	reg(sentinelSkill)

	diagnoseSkill := ai.NewDiagnoseSkill(modelMgr, executor, registry, sentinelSkill)
	reg(diagnoseSkill)

	ruleSkill := ai.NewRuleSkill(sentinelSkill, driver)
	reg(ruleSkill)

	return sentinelSkill, diagnoseSkill
}
