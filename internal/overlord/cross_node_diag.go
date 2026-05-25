/*-------------------------------------------------------------------------
 *
 * cross_node_diag.go
 *	  cross_node_diag.go implements Overlord cross-node LLM diagnosis.
 *	  When an escalated alert arrives (cross-node or LLM-takeover), the
 *	  Overlord: 1. Collects diagnostic data from multiple Worker
 *	  databases 2. Gathers Worker memory/reports for context 3. Feeds
 *	  everything to its own LLM for comprehensive analysis 4. Pushes the
 *	  conclusion back to affected Workers via PushDiagnosis
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/cross_node_diag.go
 *
 *-------------------------------------------------------------------------
 */
// cross_node_diag.go implements Overlord cross-node LLM diagnosis.
// When an escalated alert arrives (cross-node or LLM-takeover), the Overlord:
//   1. Collects diagnostic data from multiple Worker databases
//   2. Gathers Worker memory/reports for context
//   3. Feeds everything to its own LLM for comprehensive analysis
//   4. Pushes the conclusion back to affected Workers via PushDiagnosis
package overlord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
	"github.com/sqlrush/opendb/internal/db"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// CrossNodeDiagnoser orchestrates cross-node LLM diagnosis.
type CrossNodeDiagnoser struct {
	workerMgr   *WorkerManager
	dbAccess    *DBAccessManager
	correlation *CorrelationEngine
	logger      *slog.Logger

	// diagnoseFn runs LLM diagnosis with collected context.
	// Provided by the startup code that wires the model manager.
	diagnoseFn func(ctx context.Context, prompt string) (string, error)
}

// NewCrossNodeDiagnoser creates a cross-node diagnoser.
func NewCrossNodeDiagnoser(wm *WorkerManager, dam *DBAccessManager, ce *CorrelationEngine) *CrossNodeDiagnoser {
	return &CrossNodeDiagnoser{
		workerMgr:   wm,
		dbAccess:    dam,
		correlation: ce,
		logger:      cluster.DefaultLogger("cross-diag"),
	}
}

// SetDiagnoseFn sets the LLM diagnosis function.
func (cnd *CrossNodeDiagnoser) SetDiagnoseFn(fn func(ctx context.Context, prompt string) (string, error)) {
	cnd.diagnoseFn = fn
}

// DiagnoseEscalatedAlert handles an escalated alert from a Worker.
// It collects cross-node data and runs LLM analysis.
func (cnd *CrossNodeDiagnoser) DiagnoseEscalatedAlert(ctx context.Context, alert *pb.AlertEvent, reason string) (*CrossNodeDiagResult, error) {
	start := time.Now()
	cnd.logger.Info("Starting cross-node diagnosis",
		slog.String("worker_id", alert.WorkerId),
		slog.String("cause", alert.CauseName),
		slog.String("reason", reason))

	// Step 1: Collect diagnostic data from the source Worker's DB.
	sourceData, sourceErr := cnd.collectWorkerData(ctx, alert.WorkerId)

	// Step 2: For cross-node causes, also collect from related Workers.
	var relatedData map[string]*WorkerDiagData
	if reason == "cross_node" {
		relatedData = cnd.collectRelatedWorkerData(ctx, alert.WorkerId, alert.CauseName)
	}

	// Step 3: Build LLM prompt with all collected data.
	prompt := cnd.buildDiagnosisPrompt(alert, reason, sourceData, sourceErr, relatedData)

	// Step 4: Run LLM diagnosis.
	if cnd.diagnoseFn == nil {
		return nil, fmt.Errorf("no LLM diagnosis function configured")
	}

	diagnosis, err := cnd.diagnoseFn(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM diagnosis failed: %w", err)
	}

	elapsed := time.Since(start)
	cnd.logger.Info("Cross-node diagnosis complete",
		slog.String("elapsed", elapsed.String()),
		slog.Int("diagnosis_length", len(diagnosis)))

	result := &CrossNodeDiagResult{
		WorkerID:     alert.WorkerId,
		CauseName:    alert.CauseName,
		Reason:       reason,
		Diagnosis:    diagnosis,
		AffectedIDs:  cnd.getAffectedWorkerIDs(alert, relatedData),
		Elapsed:      elapsed,
		OverridesLocal: reason == "cross_node" || reason == "llm_unavailable",
		Source:       cnd.diagSourceLabel(reason),
	}

	// Step 5: Push diagnosis back to affected Workers.
	cnd.pushDiagnosisToWorkers(ctx, result)

	return result, nil
}

// WorkerDiagData holds diagnostic data collected from a Worker's database.
type WorkerDiagData struct {
	WorkerID string
	DBType   string
	Instance string
	// Collected SQL results
	ActiveSessions *db.QueryResult
	Replication    *db.QueryResult
	WALStats       *db.QueryResult
	Locks          *db.QueryResult
	Error          error
}

// CrossNodeDiagResult holds the result of a cross-node diagnosis.
type CrossNodeDiagResult struct {
	WorkerID       string
	CauseName      string
	Reason         string
	Diagnosis      string
	AffectedIDs    []string
	Elapsed        time.Duration
	OverridesLocal bool
	Source         string
}

// collectWorkerData gathers diagnostic SQL data from a single Worker's database.
func (cnd *CrossNodeDiagnoser) collectWorkerData(ctx context.Context, workerID string) (*WorkerDiagData, error) {
	data := &WorkerDiagData{WorkerID: workerID}

	// Get Worker metadata.
	cnd.workerMgr.mu.RLock()
	if wc, exists := cnd.workerMgr.workers[workerID]; exists {
		data.DBType = wc.DBType
		data.Instance = wc.Instance
	}
	cnd.workerMgr.mu.RUnlock()

	// Collect key diagnostic queries.
	queries := map[string]*struct {
		sql    string
		target **db.QueryResult
	}{
		"active_sessions": {
			sql:    "SELECT pid, usename, state, COALESCE(wait_event_type,'') AS wait_type, COALESCE(wait_event,'') AS wait_event, LEFT(query,100) AS query FROM pg_stat_activity WHERE backend_type='client backend' AND pid!=pg_backend_pid() LIMIT 30",
			target: &data.ActiveSessions,
		},
		"replication": {
			sql:    "SELECT application_name, state, sent_lsn, write_lsn, flush_lsn, replay_lsn, EXTRACT(EPOCH FROM replay_lag)::numeric(10,1) AS replay_lag_sec FROM pg_stat_replication",
			target: &data.Replication,
		},
		"wal": {
			sql:    "SELECT pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0')) AS total_wal, (SELECT count(*) FROM pg_ls_waldir()) AS wal_files",
			target: &data.WALStats,
		},
		"locks": {
			sql:    "SELECT blocked_pid, blocking_pid, blocked_activity, blocking_activity FROM (SELECT blocked_locks.pid AS blocked_pid, blocking_locks.pid AS blocking_pid, LEFT(blocked_activity.query,80) AS blocked_activity, LEFT(blocking_activity.query,80) AS blocking_activity FROM pg_catalog.pg_locks blocked_locks JOIN pg_catalog.pg_stat_activity blocked_activity ON blocked_activity.pid=blocked_locks.pid JOIN pg_catalog.pg_locks blocking_locks ON blocking_locks.locktype=blocked_locks.locktype AND blocking_locks.database IS NOT DISTINCT FROM blocked_locks.database AND blocking_locks.relation IS NOT DISTINCT FROM blocked_locks.relation AND blocking_locks.page IS NOT DISTINCT FROM blocked_locks.page AND blocking_locks.tuple IS NOT DISTINCT FROM blocked_locks.tuple AND blocking_locks.virtualxid IS NOT DISTINCT FROM blocked_locks.virtualxid AND blocking_locks.transactionid IS NOT DISTINCT FROM blocked_locks.transactionid AND blocking_locks.classid IS NOT DISTINCT FROM blocked_locks.classid AND blocking_locks.objid IS NOT DISTINCT FROM blocked_locks.objid AND blocking_locks.objsubid IS NOT DISTINCT FROM blocked_locks.objsubid AND blocking_locks.pid!=blocked_locks.pid JOIN pg_catalog.pg_stat_activity blocking_activity ON blocking_activity.pid=blocking_locks.pid WHERE NOT blocked_locks.granted) sub LIMIT 10",
			target: &data.Locks,
		},
	}

	for name, q := range queries {
		result, err := cnd.dbAccess.ExecuteSQL(ctx, workerID, q.sql)
		if err != nil {
			cnd.logger.Warn("Failed to collect data",
				slog.String("worker_id", workerID),
				slog.String("query", name),
				slog.String("error", err.Error()))
			continue
		}
		*q.target = result
	}

	return data, nil
}

// collectRelatedWorkerData collects data from Workers in the same region
// that might be affected by the same cross-node issue.
func (cnd *CrossNodeDiagnoser) collectRelatedWorkerData(ctx context.Context, sourceWorkerID, causeName string) map[string]*WorkerDiagData {
	results := make(map[string]*WorkerDiagData)
	workerIDs := cnd.workerMgr.WorkerIDs()

	for _, wid := range workerIDs {
		if wid == sourceWorkerID {
			continue
		}
		data, err := cnd.collectWorkerData(ctx, wid)
		if err != nil {
			cnd.logger.Warn("Failed to collect related worker data",
				slog.String("worker_id", wid), slog.String("error", err.Error()))
			continue
		}
		results[wid] = data
	}

	return results
}

// buildDiagnosisPrompt builds the LLM prompt with all collected cross-node data.
func (cnd *CrossNodeDiagnoser) buildDiagnosisPrompt(
	alert *pb.AlertEvent,
	reason string,
	sourceData *WorkerDiagData,
	sourceErr error,
	relatedData map[string]*WorkerDiagData,
) string {
	var sb strings.Builder

	sb.WriteString("## Overlord 跨节点诊断任务\n\n")
	sb.WriteString(fmt.Sprintf("**告警来源：** Worker %s\n", alert.WorkerId))
	sb.WriteString(fmt.Sprintf("**异常类型：** %s\n", alert.CauseName))
	sb.WriteString(fmt.Sprintf("**异常描述：** %s\n", alert.Description))
	sb.WriteString(fmt.Sprintf("**上报原因：** %s\n", reason))
	sb.WriteString(fmt.Sprintf("**严重度：** %s\n\n", alert.Severity))

	// Source Worker data.
	if sourceData != nil && sourceErr == nil {
		sb.WriteString(fmt.Sprintf("### 来源节点数据 (Worker %s, %s)\n\n", sourceData.WorkerID, sourceData.Instance))
		if sourceData.ActiveSessions != nil {
			sb.WriteString(fmt.Sprintf("**活跃会话：** %d 行\n", len(sourceData.ActiveSessions.Rows)))
		}
		if sourceData.Replication != nil {
			sb.WriteString(fmt.Sprintf("**复制状态：** %d 行\n", len(sourceData.Replication.Rows)))
		}
		if sourceData.Locks != nil && len(sourceData.Locks.Rows) > 0 {
			sb.WriteString(fmt.Sprintf("**锁等待：** %d 行\n", len(sourceData.Locks.Rows)))
		}
		sb.WriteString("\n")
	}

	// Related Workers data.
	if len(relatedData) > 0 {
		sb.WriteString(fmt.Sprintf("### 关联节点数据 (%d 个)\n\n", len(relatedData)))
		for wid, data := range relatedData {
			sb.WriteString(fmt.Sprintf("**Worker %s (%s):**\n", wid, data.Instance))
			if data.ActiveSessions != nil {
				sb.WriteString(fmt.Sprintf("  - 活跃会话: %d\n", len(data.ActiveSessions.Rows)))
			}
			if data.Replication != nil {
				sb.WriteString(fmt.Sprintf("  - 复制状态: %d\n", len(data.Replication.Rows)))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### 诊断要求\n\n")
	sb.WriteString("1. 综合多个节点的数据，分析根因\n")
	sb.WriteString("2. 如果是跨节点关联问题，说明因果关系\n")
	sb.WriteString("3. 给出覆盖全部受影响节点的修复方案\n")
	sb.WriteString("4. 每个修复方案附带风险评估、前置检查和回滚方案\n")

	return sb.String()
}

func (cnd *CrossNodeDiagnoser) getAffectedWorkerIDs(alert *pb.AlertEvent, relatedData map[string]*WorkerDiagData) []string {
	ids := []string{alert.WorkerId}
	for wid := range relatedData {
		ids = append(ids, wid)
	}
	return ids
}

func (cnd *CrossNodeDiagnoser) diagSourceLabel(reason string) string {
	switch reason {
	case "cross_node":
		return "overlord_cross_node"
	case "llm_unavailable":
		return "overlord_llm_takeover"
	case "unknown_dual_path":
		return "overlord_review"
	case "force_escalate":
		return "overlord_failover_eval"
	default:
		return "overlord_diagnosis"
	}
}

// pushDiagnosisToWorkers sends the diagnosis result to all affected Workers.
func (cnd *CrossNodeDiagnoser) pushDiagnosisToWorkers(ctx context.Context, result *CrossNodeDiagResult) {
	for _, wid := range result.AffectedIDs {
		cnd.workerMgr.mu.RLock()
		wc, exists := cnd.workerMgr.workers[wid]
		cnd.workerMgr.mu.RUnlock()

		if !exists || wc.client == nil {
			cnd.logger.Warn("Cannot push diagnosis: worker not connected",
				slog.String("worker_id", wid))
			continue
		}

		resp, err := wc.client.PushDiagnosis(ctx, &pb.PushDiagnosisRequest{
			WorkerId:          wid,
			AlertId:           fmt.Sprintf("cross-%d", time.Now().UnixMilli()),
			Diagnosis:         result.Diagnosis,
			RecommendedAction: "",
			OverridesLocal:    result.OverridesLocal,
			Source:            result.Source,
		})
		if err != nil {
			cnd.logger.Warn("PushDiagnosis failed",
				slog.String("worker_id", wid), slog.String("error", err.Error()))
			continue
		}
		cnd.logger.Info("Diagnosis pushed to worker",
			slog.String("worker_id", wid),
			slog.Bool("accepted", resp.Accepted))
	}
}
