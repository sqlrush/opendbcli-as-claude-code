/*-------------------------------------------------------------------------
 *
 * correlation.go
 *	  correlation.go implements cross-worker anomaly correlation
 *	  analysis. When multiple Workers report similar anomalies within a
 *	  short window, Overlord identifies common patterns (shared storage,
 *	  same subnet, same cluster).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/correlation.go
 *
 *-------------------------------------------------------------------------
 */
// correlation.go implements cross-worker anomaly correlation analysis.
// When multiple Workers report similar anomalies within a short window,
// Overlord identifies common patterns (shared storage, same subnet, same cluster).
package overlord

import (
	"fmt"
	"strings"
	"sync"
	"time"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// CorrelationWindow is the time window for grouping related alerts.
const CorrelationWindow = 30 * time.Second

// AlertRecord holds a received alert with timestamp.
type AlertRecord struct {
	WorkerID  string
	Alert     *pb.AlertEvent
	Received  time.Time
}

// CorrelationResult describes a detected correlation pattern.
type CorrelationResult struct {
	Pattern       string   // "shared_storage", "same_subnet", "same_cluster", "same_metric"
	Description   string   // human-readable description
	AffectedIDs   []string // worker IDs involved
	CommonFeature string   // the common feature (e.g., storage name, subnet)
	Severity      string   // "warning" or "critical"
}

// CorrelationEngine detects patterns across multiple Worker alerts.
type CorrelationEngine struct {
	mu       sync.Mutex
	alerts   []AlertRecord
	window   time.Duration
	workerMgr *WorkerManager
}

// NewCorrelationEngine creates a new correlation engine.
func NewCorrelationEngine(wm *WorkerManager) *CorrelationEngine {
	return &CorrelationEngine{
		window:    CorrelationWindow,
		workerMgr: wm,
	}
}

// AddAlert records an alert and checks for correlations.
// Returns correlation results if a pattern is detected, nil otherwise.
func (ce *CorrelationEngine) AddAlert(record AlertRecord) []CorrelationResult {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	ce.alerts = append(ce.alerts, record)

	// Prune old alerts outside the window.
	cutoff := time.Now().Add(-ce.window)
	pruned := ce.alerts[:0]
	for _, a := range ce.alerts {
		if a.Received.After(cutoff) {
			pruned = append(pruned, a)
		}
	}
	ce.alerts = pruned

	// Need at least 3 alerts in the window to trigger correlation.
	if len(ce.alerts) < 3 {
		return nil
	}

	return ce.analyze()
}

// analyze checks the current alert window for correlation patterns.
func (ce *CorrelationEngine) analyze() []CorrelationResult {
	var results []CorrelationResult

	// Pattern 1: Same cause across multiple workers.
	causeCounts := make(map[string][]string) // causeName → []workerID
	for _, a := range ce.alerts {
		cause := a.Alert.CauseName
		if cause != "" {
			causeCounts[cause] = appendUnique(causeCounts[cause], a.WorkerID)
		}
	}

	for cause, workers := range causeCounts {
		if len(workers) >= 3 {
			results = append(results, CorrelationResult{
				Pattern:       "same_metric",
				Description:   fmt.Sprintf("%d workers同时出现 %s 异常", len(workers), cause),
				AffectedIDs:   workers,
				CommonFeature: cause,
				Severity:      classifyCorrelationSeverity(len(workers)),
			})
		}
	}

	// Pattern 2: Check for common DB type (possible platform-level issue).
	dbTypeCounts := make(map[string][]string)
	ce.workerMgr.mu.RLock()
	for _, a := range ce.alerts {
		if wc, exists := ce.workerMgr.workers[a.WorkerID]; exists {
			dbTypeCounts[wc.DBType] = appendUnique(dbTypeCounts[wc.DBType], a.WorkerID)
		}
	}
	ce.workerMgr.mu.RUnlock()

	for dbType, workers := range dbTypeCounts {
		if len(workers) >= 5 {
			results = append(results, CorrelationResult{
				Pattern:       "same_dbtype",
				Description:   fmt.Sprintf("%d 个 %s 节点同时异常，可能是平台级问题", len(workers), dbType),
				AffectedIDs:   workers,
				CommonFeature: dbType,
				Severity:      "critical",
			})
		}
	}

	return results
}

// FormatCorrelation returns a human-readable correlation report.
func FormatCorrelation(results []CorrelationResult) string {
	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("检测到 %d 个关联模式:\n", len(results)))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, r.Severity, r.Description))
		sb.WriteString(fmt.Sprintf("     影响节点: %s\n", strings.Join(r.AffectedIDs, ", ")))
		sb.WriteString(fmt.Sprintf("     共同特征: %s (%s)\n", r.CommonFeature, r.Pattern))
	}
	return sb.String()
}

func classifyCorrelationSeverity(count int) string {
	if count >= 10 {
		return "critical"
	}
	if count >= 5 {
		return "warning"
	}
	return "info"
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}
