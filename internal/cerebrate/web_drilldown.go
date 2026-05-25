/*-------------------------------------------------------------------------
 *
 * web_drilldown.go
 *	  web_drilldown.go provides drill-down API handlers for worker
 *	  detail and region report views. Uses cached FleetManager data for
 *	  fast responses.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/web_drilldown.go
 *
 *-------------------------------------------------------------------------
 */
// web_drilldown.go provides drill-down API handlers for worker detail and
// region report views. Uses cached FleetManager data for fast responses.
package cerebrate

import (
	"encoding/json"
	"net/http"
	"time"
)

// WorkerDetail is the JSON response for a single worker drill-down.
type WorkerDetail struct {
	WorkerID    string `json:"worker_id"`
	OverlordID  string `json:"overlord_id"`
	Region      string `json:"region"`
	Health      int32  `json:"health"`
	State       string `json:"state"`
	DBType      string `json:"db_type"`
	Instance    string `json:"instance"`
	LastAnomaly string `json:"last_anomaly,omitempty"`
	Uptime      string `json:"uptime"`
}

// RegionReportEntry is the JSON response for a region drill-down report.
type RegionReportEntry struct {
	OverlordID        string            `json:"overlord_id"`
	Region            string            `json:"region"`
	TotalWorkers      int32             `json:"total_workers"`
	OnlineWorkers     int32             `json:"online_workers"`
	HealthScore       int32             `json:"health_score"`
	HealthSummary     string            `json:"health_summary"`
	HealthDistribution map[string]int   `json:"health_distribution"` // "healthy", "degraded", "offline"
	DBTypeBreakdown   map[string]int    `json:"db_type_breakdown"`
	AnomalyCount      int               `json:"anomaly_count"`
	Workers           []WorkerSummary   `json:"workers"`
	GeneratedAt       string            `json:"generated_at"`
}

// WorkerSummary is a brief worker entry within a region report.
type WorkerSummary struct {
	WorkerID string `json:"worker_id"`
	State    string `json:"state"`
	DBType   string `json:"db_type"`
	Health   int32  `json:"health"`
}

// handleWorkerDetail serves GET /api/worker/{worker_id}
// Returns per-worker detail from FleetManager's cached status.
func (wd *WebDashboard) handleWorkerDetail(w http.ResponseWriter, r *http.Request) {
	workerID := extractPathSuffix(r.URL.Path, "/api/worker/")
	if workerID == "" {
		http.Error(w, "worker_id required", http.StatusBadRequest)
		return
	}

	// Search across all overlords' cached data for this worker.
	detail := wd.findWorkerInCache(workerID)
	if detail == nil {
		http.Error(w, "worker not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(detail)
}

// findWorkerInCache searches cached overlord data for a specific worker.
// Returns nil if the worker is not found.
func (wd *WebDashboard) findWorkerInCache(workerID string) *WorkerDetail {
	cached := wd.fleet.GetCachedStatus()

	for _, region := range cached {
		workers, ok := region["workers"].([]map[string]any)
		if !ok {
			continue
		}
		overlordID, _ := region["overlord_id"].(string)
		regionName, _ := region["region"].(string)

		for _, wk := range workers {
			id, _ := wk["id"].(string)
			if id != workerID {
				continue
			}

			health := int32(0)
			if h, ok := wk["health"].(float64); ok {
				health = int32(h)
			}
			if h, ok := wk["health"].(int32); ok {
				health = h
			}

			return &WorkerDetail{
				WorkerID:   workerID,
				OverlordID: overlordID,
				Region:     regionName,
				Health:     health,
				State:      stringFromAny(wk["state"]),
				DBType:     stringFromAny(wk["db_type"]),
				Instance:   stringFromAny(wk["instance"]),
				Uptime:     stringFromAny(wk["uptime"]),
			}
		}
	}
	return nil
}

// handleRegionReport serves GET /api/report/{overlord_id}
// Returns a structured region report from cached fleet data.
func (wd *WebDashboard) handleRegionReport(w http.ResponseWriter, r *http.Request) {
	overlordID := extractPathSuffix(r.URL.Path, "/api/report/")
	if overlordID == "" {
		http.Error(w, "overlord_id required", http.StatusBadRequest)
		return
	}

	report := wd.buildRegionReport(overlordID)
	if report == nil {
		http.Error(w, "overlord not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// buildRegionReport constructs a region report from cached overlord data.
func (wd *WebDashboard) buildRegionReport(overlordID string) *RegionReportEntry {
	cached := wd.fleet.GetCachedStatus()

	for _, region := range cached {
		id, _ := region["overlord_id"].(string)
		if id != overlordID {
			continue
		}

		regionName, _ := region["region"].(string)
		workerCount := int32FromAny(region["worker_count"])
		onlineCount := int32FromAny(region["online"])

		healthScore := int32(0)
		healthSummary := "unknown"
		if h, ok := region["health"].(map[string]any); ok {
			healthScore = int32FromAny(h["score"])
			healthSummary = stringFromAny(h["summary"])
		}

		report := &RegionReportEntry{
			OverlordID:         overlordID,
			Region:             regionName,
			TotalWorkers:       workerCount,
			OnlineWorkers:      onlineCount,
			HealthScore:        healthScore,
			HealthSummary:      healthSummary,
			HealthDistribution: map[string]int{"healthy": int(onlineCount), "offline": int(workerCount - onlineCount)},
			DBTypeBreakdown:    make(map[string]int),
			AnomalyCount:       0,
			Workers:            make([]WorkerSummary, 0),
			GeneratedAt:        time.Now().Format(time.RFC3339),
		}

		// Populate worker list if available.
		if workers, ok := region["workers"].([]map[string]any); ok {
			for _, wk := range workers {
				dbType := stringFromAny(wk["db_type"])
				report.DBTypeBreakdown[dbType]++
				report.Workers = append(report.Workers, WorkerSummary{
					WorkerID: stringFromAny(wk["id"]),
					State:    stringFromAny(wk["state"]),
					DBType:   dbType,
					Health:   int32FromAny(wk["health"]),
				})
			}
		}

		return report
	}
	return nil
}

// stringFromAny safely extracts a string from an any value.
func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// int32FromAny safely extracts an int32 from an any value.
func int32FromAny(v any) int32 {
	switch n := v.(type) {
	case int32:
		return n
	case int:
		return int32(n)
	case float64:
		return int32(n)
	case int64:
		return int32(n)
	}
	return 0
}
