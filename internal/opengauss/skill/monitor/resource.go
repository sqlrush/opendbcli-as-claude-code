/*-------------------------------------------------------------------------
 *
 * resource.go
 *	  ResourceSkill shows resource usage vs limits.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/resource.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const resourceConnSQL = `SELECT
  (SELECT setting::int FROM pg_settings WHERE name = 'max_connections') AS max_conn,
  (SELECT count(*) FROM pg_stat_activity) AS current_conn,
  (SELECT setting::int FROM pg_settings WHERE name = 'superuser_reserved_connections') AS reserved`

const resourceWalSenderSQL = `SELECT
  (SELECT setting::int FROM pg_settings WHERE name = 'max_wal_senders') AS max_senders,
  (SELECT count(*) FROM pg_stat_replication) AS current_senders`

const resourceCheckpointSQL = `SELECT
  COALESCE(checkpoints_timed, 0) AS checkpoints_timed,
  COALESCE(checkpoints_req, 0) AS checkpoints_req,
  COALESCE(checkpoint_write_time, 0) AS checkpoint_write_time,
  COALESCE(checkpoint_sync_time, 0) AS checkpoint_sync_time,
  COALESCE(buffers_checkpoint, 0) AS buffers_checkpoint,
  COALESCE(buffers_clean, 0) AS buffers_clean,
  COALESCE(buffers_backend, 0) AS buffers_backend
FROM pg_stat_bgwriter`

const resourceWorkerSQL = `SELECT
  (SELECT setting::int FROM pg_settings WHERE name = 'max_worker_processes') AS max_workers,
  (SELECT setting::int FROM pg_settings WHERE name = 'max_parallel_workers') AS max_parallel,
  (SELECT setting::int FROM pg_settings WHERE name = 'autovacuum_max_workers') AS autovac_workers`

// ResourceSkill shows resource usage vs limits.
type ResourceSkill struct{ driver db.Driver }

// NewResourceSkill creates a ResourceSkill backed by the given driver.
func NewResourceSkill(driver db.Driver) *ResourceSkill {
	return &ResourceSkill{driver: driver}
}

func (s *ResourceSkill) Name() string                       { return "resource" }
func (s *ResourceSkill) Description() string                { return "资源使用 vs 限制" }
func (s *ResourceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ResourceSkill) Validate(_ skill.Params) error      { return nil }

func (s *ResourceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/resource", Examples: []string{"/resource"}}
}

func (s *ResourceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "resource",
		Description: "Show OpenGauss resource usage vs limits: connections, WAL senders, checkpoints, workers",
	}
}

func (s *ResourceSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	sections := []format.PanelSection{}

	// Connections
	connResult, err := s.driver.Query(ctx, resourceConnSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(connResult.Rows) > 0 {
		row := connResult.Rows[0]
		maxConn := toFloat64Val(row[0])
		curConn := toFloat64Val(row[1])
		reserved := toFloat64Val(row[2])
		connPct := safePct(curConn, maxConn)

		sections = append(sections, format.PanelSection{
			Header: "Connections",
			Lines: []string{
				fmt.Sprintf("Current / Max   : %.0f / %.0f (%.1f%%)", curConn, maxConn, connPct),
				fmt.Sprintf("Reserved (SU)   : %.0f", reserved),
				fmt.Sprintf("                  %s", format.ProgressBar(connPct, 30)),
			},
		})
	}

	// WAL Senders
	walResult, err := s.driver.Query(ctx, resourceWalSenderSQL)
	if err == nil && len(walResult.Rows) > 0 {
		row := walResult.Rows[0]
		maxSenders := toFloat64Val(row[0])
		curSenders := toFloat64Val(row[1])
		senderPct := safePct(curSenders, maxSenders)

		sections = append(sections, format.PanelSection{
			Header: "WAL Senders",
			Lines: []string{
				fmt.Sprintf("Current / Max   : %.0f / %.0f (%.1f%%)", curSenders, maxSenders, senderPct),
				fmt.Sprintf("                  %s", format.ProgressBar(senderPct, 30)),
			},
		})
	}

	// Workers — OG does not expose max_worker_processes / max_parallel_workers
	// (those are PG 9.4+ GUCs). Show the Autovacuum value we have and tag
	// the others as N/A so users don't see a bare empty field.
	workerResult, err := s.driver.Query(ctx, resourceWorkerSQL)
	if err == nil && len(workerResult.Rows) > 0 {
		row := workerResult.Rows[0]
		sections = append(sections, format.PanelSection{
			Header: "Workers",
			Lines: []string{
				fmt.Sprintf("Max Workers     : %s", nonEmptyOr(asStr(row[0]), "N/A (GUC not in OG)")),
				fmt.Sprintf("Max Parallel    : %s", nonEmptyOr(asStr(row[1]), "N/A (GUC not in OG)")),
				fmt.Sprintf("Autovac Workers : %s", asStr(row[2])),
			},
		})
	}

	// Checkpoints
	cpResult, err := s.driver.Query(ctx, resourceCheckpointSQL)
	if err == nil && len(cpResult.Rows) > 0 {
		row := cpResult.Rows[0]
		timed := toFloat64Val(row[0])
		req := toFloat64Val(row[1])
		totalCP := timed + req
		timedPct := safePct(timed, totalCP)

		sections = append(sections, format.PanelSection{
			Header: "Checkpoints",
			Lines: []string{
				fmt.Sprintf("Timed           : %.0f (%.1f%%)", timed, timedPct),
				fmt.Sprintf("Requested       : %.0f", req),
				fmt.Sprintf("Write Time      : %.0f ms", toFloat64Val(row[2])),
				fmt.Sprintf("Sync Time       : %.0f ms", toFloat64Val(row[3])),
				fmt.Sprintf("Buffers (ckpt)  : %.0f", toFloat64Val(row[4])),
				fmt.Sprintf("Buffers (clean) : %.0f", toFloat64Val(row[5])),
				fmt.Sprintf("Buffers (backend): %.0f", toFloat64Val(row[6])),
			},
		})
	}

	rendered := format.Panel("Resource Usage", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "resource usage vs limits",
	}, nil
}
