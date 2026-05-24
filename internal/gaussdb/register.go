/*-------------------------------------------------------------------------
 *
 * register.go
 *	  Package gaussdb registers the GaussDB Centralized product into
 *	  opendb.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/register.go
 *
 *-------------------------------------------------------------------------
 */
// Package gaussdb registers the GaussDB Centralized product into opendb.
//
// GaussDB(for openGauss) Centralized V2.0-8.x is binary-compatible with
// openGauss for skill purposes (same system views, same SQL dialect), but
// requires a distinct wire driver due to SCRAM-SHA256(10) auth differences
// (see internal/gaussdb/driver). Skills are reused from internal/opengauss
// for now and may diverge in the future.
package gaussdb

import (
	"path/filepath"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	gaussdriver "github.com/sqlrush/opendb/internal/gaussdb/driver"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/opengauss/skill/admin"
	"github.com/sqlrush/opendb/internal/opengauss/skill/ai"
	"github.com/sqlrush/opendb/internal/opengauss/skill/monitor"
	"github.com/sqlrush/opendb/internal/opengauss/skill/query"
	"github.com/sqlrush/opendb/internal/opengauss/skill/schema"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
)

// DBType is the connection.db_type value that routes to GaussDB.
const DBType = "gaussdb"

// DriverFactory creates a GaussDB driver from a ConnectionConfig.
func DriverFactory(cfg db.ConnectionConfig) (db.Driver, error) {
	return gaussdriver.NewDriver(cfg)
}

// RegisterSkills registers GaussDB-applicable skills into the registry.
//
// Reuses OpenGauss skill implementations because the system views are the
// same. If GaussDB-specific divergence is needed later, fork into
// internal/gaussdb/skill/ and replace the imports above.
func RegisterSkills(
	registry *skill.Registry,
	driver db.Driver,
	connMgr *connection.Manager,
	history *session.SessionHistory,
	cfg *config.Config,
	modelMgr *model.Manager,
	opendbDir string,
) {
	reg := func(s skill.Skill) { registry.RegisterForDB(DBType, s) }

	// Monitor — session & lock
	reg(monitor.NewSessionsSkill(driver))
	reg(monitor.NewActiveSessionsSkill(driver))
	reg(monitor.NewLocksSkill(driver))
	reg(monitor.NewBlockTreeSkill(driver))
	reg(monitor.NewLWLocksSkill(driver))

	// Monitor — MVCC / XID / VACUUM
	reg(monitor.NewVacuumSkill(driver))
	reg(monitor.NewAutoVacuumSkill(driver))
	reg(monitor.NewXIDSkill(driver))
	reg(monitor.NewLongTxSkill(driver))
	reg(monitor.NewBloatSkill(driver))

	// Monitor — wait / waits / trace
	reg(monitor.NewWaitsSkill(driver))
	reg(monitor.NewTraceSkillForDB(DBType, "GaussDB", connMgr.CurrentHost(), &cfg.Trace))
	reg(monitor.NewDiagTraceSkill())

	// Monitor — memory / buffers
	reg(monitor.NewGSMemSkill(driver))
	reg(monitor.NewSessionMemSkill(driver))

	// Monitor — WAL / replication / backup
	reg(monitor.NewWALSkill(driver))
	reg(monitor.NewWALSummarySkill(driver))
	reg(monitor.NewCheckpointSkill(driver))
	reg(monitor.NewBgWorkerSkill(driver))
	reg(monitor.NewReplicationSkill(driver))
	reg(monitor.NewSlotsSkill(driver))
	reg(monitor.NewLogicalSlotsSkill(driver))
	reg(monitor.NewPubSubSkill(driver))
	reg(monitor.NewCMHASkill(driver))

	// Monitor — space / temp / hot tables
	reg(monitor.NewResourceSkill(driver))
	reg(monitor.NewSegmentsSkill(driver))
	reg(monitor.NewTempUsageSkill(driver))
	reg(monitor.NewHotKeySkill(driver))
	reg(monitor.NewMOTSkill(driver))

	// Monitor — system / users / meta
	reg(monitor.NewOSSkill(driver, connMgr.CurrentHost()))
	reg(monitor.NewIndexHealthSkill(driver))
	reg(monitor.NewUsersSkill(driver))
	reg(monitor.NewHealthSkill(driver))
	reg(monitor.NewOGDBTopSkill(driver))
	reg(monitor.NewSQLCountSkill(driver))
	reg(monitor.NewResPoolSkill(driver))

	// Monitor — performance snapshot
	perfSnapDir := filepath.Join(opendbDir, "scheduler")
	reg(monitor.NewOGPerfSnapSkill(driver, perfSnapDir))

	// Query
	reg(query.NewTopSQLSkill(driver))
	reg(query.NewSlowSQLSkill(driver))
	reg(query.NewSQLSkill(driver))
	reg(query.NewExplainSkill(driver))
	reg(query.NewASHSkill(driver))
	reg(query.NewOGErrSkill())
	reg(query.NewWDRSkill(driver))
	reg(query.NewPlanHistorySkill(driver))
	// /sqltune: reuse og's skill directly. GaussDB shares 99% of og's
	// system catalog (dbe_perf.statement_history, EXPLAIN PERFORMANCE,
	// pg_stats), and the dropped capability (GS_PLAN_TRACE — CBO decision
	// dump) is not in current customer scope. Going through og keeps a
	// single source of truth and inherits og's SQL_ID branch
	// (looksLikeSQLID + fetchLiteralSQLByID) so /sqltune <numeric_id>
	// works on GaussDB the same way it does on og.
	reg(query.NewSQLTuneSkill(driver, modelMgr, nil))
	sqlfetchSkill := query.NewSQLFetchSkill(driver)
	reg(sqlfetchSkill)
	reg(query.NewWDRAnalyzeSkill(driver, sqlfetchSkill, modelMgr, nil))

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
	reg(schema.NewToastTableSkill(driver))
}

// RegisterAISkills registers sentinel, diagnose, and rule skills.
//
// Returns sentinel and diagnose skills so REPL can use them for auto-start
// and alerts. Mirrors opengauss.RegisterAISkills 1:1; the underlying skills
// are the same OG implementations, only the registration tag is "gaussdb".
func RegisterAISkills(
	registry *skill.Registry,
	driver db.Driver,
	executor *skill.Executor,
	cfg *config.Config,
	modelMgr *model.Manager,
) (*ai.SentinelSkill, *ai.DiagnoseSkill) {
	reg := func(s skill.Skill) { registry.RegisterForDB(DBType, s) }

	sentinelSkill := ai.NewSentinelSkill(driver, cfg.Sentinel)
	reg(sentinelSkill)

	ruleSkill := ai.NewRuleSkill(sentinelSkill, driver)
	reg(ruleSkill)

	diagnoseSkill := ai.NewDiagnoseSkill(modelMgr, executor, registry, sentinelSkill, ruleSkill)
	reg(diagnoseSkill)

	return sentinelSkill, diagnoseSkill
}
