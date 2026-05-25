/*-------------------------------------------------------------------------
 *
 * register.go
 *	  Package dm registers Dameng (DM) database product into opendb.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/register.go
 *
 *-------------------------------------------------------------------------
 */
// Package dm registers Dameng (DM) database product into opendb.
//
// DM Oracle-compatible mode (COMPATIBLE_MODE=2) provides 100+ Oracle V$
// views. Skills here are DM-specific (not reused from oracle/) because DM
// has its own view names, system packages, and operational commands
// (SP_CLOSE_SESSION vs ALTER SYSTEM KILL SESSION).
//
// Platform: DM official Go driver only supports Linux/Windows. macOS
// clients cannot connect to DM. dbaa must be cross-compiled to Linux.
package dm

import (
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	dmdriver "github.com/sqlrush/opendb/internal/dm/driver"
	dmai "github.com/sqlrush/opendb/internal/dm/skill/ai"
	dmmonitor "github.com/sqlrush/opendb/internal/dm/skill/monitor"
	dmquery "github.com/sqlrush/opendb/internal/dm/skill/query"
	dmschema "github.com/sqlrush/opendb/internal/dm/skill/schema"
	"github.com/sqlrush/opendb/internal/model"
	oracleai "github.com/sqlrush/opendb/internal/oracle/skill/ai"
	oraclequery "github.com/sqlrush/opendb/internal/oracle/skill/query"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui"
)

// DBType is the connection.db_type value that routes to DM.
const DBType = "dm"

// DriverFactory creates a DM driver from a ConnectionConfig.
func DriverFactory(cfg db.ConnectionConfig) (db.Driver, error) {
	return dmdriver.NewDriver(cfg)
}

// RegisterSkills registers DM-applicable skills into the registry.
//
// Phase 1: 13 read-only skill (sql + 12 diagnostic).
// Each skill is real-machine validated against DM 8.1.4.200.
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

	// /sql — generic SQL execution (Oracle's impl is generic, DM-compatible)
	reg(oraclequery.NewSQLSkill(driver))

	// Monitor — instance/session/lock/wait/alert
	reg(dmmonitor.NewInfoSkill(driver))
	reg(dmmonitor.NewSessionsSkill(driver))
	reg(dmmonitor.NewActiveSessionsSkill(driver))
	reg(dmmonitor.NewLocksSkill(driver))
	reg(dmmonitor.NewBlockTreeSkill(driver))
	reg(dmmonitor.NewWaitsSkill(driver))
	reg(dmmonitor.NewDeadlockSkill(driver))
	reg(dmmonitor.NewHealthSkill(driver))
	reg(dmmonitor.NewAlertSkill(driver))
	reg(dmmonitor.NewAnomaliesSkill(driver))
	reg(dmmonitor.NewErrCodeSkill(driver))
	reg(dmmonitor.NewViewsSkill(driver))

	// Monitor — Phase 2 (10 个新 skill: 存储/资源/集群/性能基线)
	reg(dmmonitor.NewSegmentsSkill(driver))
	reg(dmmonitor.NewUsersSkill(driver))
	reg(dmmonitor.NewRedoSkill(driver))
	reg(dmmonitor.NewTempUsageSkill(driver))
	reg(dmmonitor.NewArchiveSkill(driver))
	reg(dmmonitor.NewStandbySkill(driver))
	reg(dmmonitor.NewResourceSkill(driver))
	reg(dmmonitor.NewIndexHealthSkill(driver))
	reg(dmmonitor.NewMemPoolSkill(driver))
	reg(dmmonitor.NewOSSkill(driver))
	reg(dmmonitor.NewClusterSkill(driver))
	reg(dmmonitor.NewPerfSnapSkill(driver))
	reg(dmmonitor.NewDbtopSkill(driver))

	// Query — top/slow/explain
	reg(dmquery.NewTopSQLSkill(driver))
	reg(dmquery.NewSlowSQLSkill(driver))
	reg(dmquery.NewExplainSkill(driver))

	// Schema — tableinfo
	reg(dmschema.NewTableInfoSkill(driver))
}

// RegisterAISkills 注册 DM 的 AI skill: sentinel + diag.
// Phase 2: DM SentinelSkill (MVP 阈值检测版) + 复用 oracle DiagnoseSkill.
// rule_skill 暂缓 (见 memory/decision-dm-rule-skill-deferred.md).
func RegisterAISkills(
	registry *skill.Registry,
	driver db.Driver,
	executor *skill.Executor,
	cfg *config.Config,
	modelMgr *model.Manager,
) (ui.SentinelAlertSource, ui.DiagAsyncSource) {
	reg := func(s skill.Skill) { registry.RegisterForDB(DBType, s) }

	// DM Sentinel: 阈值优先读 config.Sentinel.Thresholds, 0/未设保留默认.
	// 复用通用 ThresholdConfig 字段:
	//   LockAbsolute    → DM thrBlocked
	//   LongSQLAbsolute → DM thrLongSQL
	// ProbeInterval: Oracle sentinel 默认 1s 对 DM 太频繁,
	// 只在 cfg 显式 ≥ 5s 才覆盖 DM 默认 30s.
	// AutoStart: cfg.Sentinel.AutoStart=true 时, REPL switchAISkills
	// 后会自动调用 AutoStart(), 实现"重启后恢复采集".
	sentinel := dmai.NewSentinelSkill(driver).
		WithThresholds(int(cfg.Sentinel.Thresholds.LockAbsolute), int(cfg.Sentinel.Thresholds.LongSQLAbsolute))
	if probeSec := int(cfg.Sentinel.ProbeInterval.Seconds()); probeSec >= 5 {
		sentinel.WithInterval(probeSec)
	}
	reg(sentinel)

	diag := oracleai.NewDiagnoseSkill(modelMgr, executor, registry, nil)
	reg(diag)
	return sentinel, diag
}
