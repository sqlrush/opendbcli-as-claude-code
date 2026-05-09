/*-------------------------------------------------------------------------
 *
 * register.go
 *	  DriverFactory creates an OpenGauss driver from a ConnectionConfig.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/register.go
 *
 *-------------------------------------------------------------------------
 */
package opengauss

import (
	"path/filepath"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/model"
	ogdriver "github.com/sqlrush/opendb/internal/opengauss/driver"
	"github.com/sqlrush/opendb/internal/opengauss/skill/admin"
	"github.com/sqlrush/opendb/internal/opengauss/skill/ai"
	"github.com/sqlrush/opendb/internal/opengauss/skill/monitor"
	"github.com/sqlrush/opendb/internal/opengauss/skill/query"
	"github.com/sqlrush/opendb/internal/opengauss/skill/schema"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
)

// DriverFactory creates an OpenGauss driver from a ConnectionConfig.
func DriverFactory(cfg db.ConnectionConfig) (db.Driver, error) {
	return ogdriver.NewDriver(cfg)
}

// RegisterSkills registers all OpenGauss-specific skills into the registry.
func RegisterSkills(
	registry *skill.Registry,
	driver db.Driver,
	connMgr *connection.Manager,
	history *session.SessionHistory,
	cfg *config.Config,
	modelMgr *model.Manager,
	opendbDir string,
) {
	reg := func(s skill.Skill) { registry.RegisterForDB("opengauss", s) }

	// Monitor — session & lock
	reg(monitor.NewSessionsSkill(driver))
	reg(monitor.NewActiveSessionsSkill(driver))
	reg(monitor.NewLocksSkill(driver))
	reg(monitor.NewBlockTreeSkill(driver))
	reg(monitor.NewLWLocksSkill(driver)) // NEW P0: LWLock contention profile

	// Monitor — MVCC / XID / VACUUM
	reg(monitor.NewVacuumSkill(driver))
	reg(monitor.NewAutoVacuumSkill(driver)) // NEW P0: autovacuum progress + dead tuple top
	reg(monitor.NewXIDSkill(driver))
	reg(monitor.NewLongTxSkill(driver))
	reg(monitor.NewBloatSkill(driver))

	// Monitor — wait / waits / trace
	reg(monitor.NewWaitsSkill(driver))
	reg(monitor.NewTraceSkill(connMgr.CurrentHost(), &cfg.Trace)) // NEW P0: OS stack / flamegraph (Linux)

	// Monitor — memory / buffers
	reg(monitor.NewGSMemSkill(driver))
	reg(monitor.NewSessionMemSkill(driver))
	// /sharedbufs removed — merged into /gsmem (5c)

	// Monitor — WAL / replication / backup
	reg(monitor.NewWALSkill(driver))
	reg(monitor.NewWALSummarySkill(driver))  // NEW P2: archiver detail
	reg(monitor.NewCheckpointSkill(driver))  // NEW P1: checkpoint frequency + WAL amplification
	reg(monitor.NewBgWorkerSkill(driver))    // NEW P1: background processes
	reg(monitor.NewReplicationSkill(driver))
	reg(monitor.NewSlotsSkill(driver))
	reg(monitor.NewLogicalSlotsSkill(driver)) // NEW P1: logical slots (retained WAL)
	reg(monitor.NewPubSubSkill(driver))       // NEW P2: logical pub/sub
	reg(monitor.NewCMHASkill(driver))         // NEW P2: cluster HA state

	// Monitor — space / temp / hot tables
	reg(monitor.NewResourceSkill(driver))
	reg(monitor.NewSegmentsSkill(driver))
	reg(monitor.NewTempUsageSkill(driver)) // NEW P1: temp file usage
	reg(monitor.NewHotKeySkill(driver))    // NEW P1: hotspot table ranking
	reg(monitor.NewMOTSkill(driver))       // NEW P2: MOT engine state

	// Monitor — system / users / meta
	reg(monitor.NewOSSkill(driver, connMgr.CurrentHost()))
	reg(monitor.NewIndexHealthSkill(driver))
	reg(monitor.NewUsersSkill(driver))
	reg(monitor.NewHealthSkill(driver))
	// /extensions removed — merged into /health (5c)
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
	reg(query.NewWDRSkill(driver))         // NEW P0: WDR snapshot (parallels Oracle AWR)
	reg(query.NewPlanHistorySkill(driver)) // NEW P0: plan regression detection
	// /sqltune: 复杂 SQL 调优 (LLM + 真实数据 + 验证闭环).
	// memStore=nil for now — memory 注入是 optional, P5 阶段统一通过 SetContextStores 接入.
	reg(query.NewSQLTuneSkill(driver, modelMgr, nil))
	// /sqlfetch: 按 SQL_ID 拉取可 EXPLAIN 的完整 SQL，桥接 /llm → /sqltune
	// 路由失败的根因（实测 4 个 LLM 都不知道 og 元数据列名）。先调它再调 sqltune。
	reg(query.NewSQLFetchSkill(driver))

	// Admin
	reg(admin.NewKillSkill(driver)) // /kill now handles both terminate and cancel
	reg(admin.NewSpaceSkill(driver))
	reg(admin.NewParamsSkill(driver))
	reg(admin.NewAlterSkill(driver))
	reg(admin.NewAlertSkill(driver))
	reg(admin.NewBackupSkill(driver))
	reg(admin.NewJobsSkill(driver))
	reg(admin.NewGatherSkill(driver))
	// /cancel removed — functionality absorbed into /kill <pid> cancel confirm (5c)

	// Schema
	reg(schema.NewTableInfoSkill(driver))
	reg(schema.NewIndexAdviseSkill(driver))
	reg(schema.NewToastTableSkill(driver)) // NEW P2: TOAST storage ranking
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
	reg := func(s skill.Skill) { registry.RegisterForDB("opengauss", s) }

	sentinelSkill := ai.NewSentinelSkill(driver, cfg.Sentinel)
	reg(sentinelSkill)

	ruleSkill := ai.NewRuleSkill(sentinelSkill, driver)
	reg(ruleSkill)

	diagnoseSkill := ai.NewDiagnoseSkill(modelMgr, executor, registry, sentinelSkill, ruleSkill)
	reg(diagnoseSkill)

	return sentinelSkill, diagnoseSkill
}
