/*-------------------------------------------------------------------------
 *
 * web.go
 *	  web.go implements the Cerebrate Web dashboard. Provides a HTTP
 *	  server for global topology, health status, and report drill-down.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/web.go
 *
 *-------------------------------------------------------------------------
 */
// web.go implements the Cerebrate Web dashboard.
// Provides a HTTP server for global topology, health status, and report drill-down.
package cerebrate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
	"github.com/sqlrush/opendb/internal/odberr"
)

// WebDashboard serves the Cerebrate monitoring dashboard via HTTP.
type WebDashboard struct {
	fleet      *FleetManager
	listenAddr string
	server     *http.Server
	pullServer *PullInstallServer
	timeline   *TimelineStore
	reports    *ReportStore
	logger     *slog.Logger
}

// NewWebDashboard creates a new web dashboard.
func NewWebDashboard(fleet *FleetManager, listenAddr string) *WebDashboard {
	return &WebDashboard{
		fleet:      fleet,
		listenAddr: listenAddr,
		logger:     cluster.DefaultLogger("web"),
	}
}

// SetTimeline configures the timeline store for recent events.
func (wd *WebDashboard) SetTimeline(ts *TimelineStore) {
	wd.timeline = ts
}

// SetReports configures the report store for fault reports.
func (wd *WebDashboard) SetReports(rs *ReportStore) {
	wd.reports = rs
}

// RegisterPullInstall adds Pull install routes to the dashboard's HTTP mux.
func (wd *WebDashboard) RegisterPullInstall(ps *PullInstallServer) {
	wd.pullServer = ps
}

// Start launches the HTTP server. Blocks until ctx is cancelled.
func (wd *WebDashboard) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// API endpoints.
	mux.HandleFunc("/api/fleet", wd.handleFleetStatus)
	mux.HandleFunc("/api/overlords", wd.handleOverlords)
	mux.HandleFunc("/api/topology", wd.handleTopology)
	mux.HandleFunc("/api/health", wd.handleHealth)

	// Pull install endpoints (#5).
	if wd.pullServer != nil {
		wd.pullServer.RegisterRoutes(mux)
	}

	// Drill-down API (#10).
	mux.HandleFunc("/api/region/", wd.handleRegionDetail)

	// Timeline, reports, and drill-down APIs (V0.7 Phase 5).
	mux.HandleFunc("/api/timeline", wd.handleTimeline)
	mux.HandleFunc("/api/reports", wd.handleReportList)
	mux.HandleFunc("/api/reports/", wd.handleReportDetail)
	mux.HandleFunc("/api/worker/", wd.handleWorkerDetail)
	mux.HandleFunc("/api/report/", wd.handleRegionReport)

	// Dashboard HTML page.
	mux.HandleFunc("/", wd.handleDashboard)

	wd.server = &http.Server{
		Addr:    wd.listenAddr,
		Handler: mux,
	}

	// Graceful shutdown on context cancel.
	odberr.SafeGo(odberr.ErrClusterRPC, func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		wd.server.Shutdown(shutdownCtx)
	})

	wd.logger.Info("Dashboard listening", slog.String("addr", "http://"+wd.listenAddr))
	if err := wd.server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleFleetStatus returns fleet-wide status as JSON (uses cached data, fast).
func (wd *WebDashboard) handleFleetStatus(w http.ResponseWriter, _ *http.Request) {
	cached := wd.fleet.GetCachedStatus()

	data := map[string]any{
		"timestamp":      time.Now().Format(time.RFC3339),
		"overlords":      wd.fleet.OverlordCount(),
		"total_workers":  wd.fleet.TotalWorkerCount(),
		"online_workers": wd.fleet.TotalOnlineCount(),
		"regions":        cached,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// handleOverlords returns all Overlord statuses as JSON (cached).
func (wd *WebDashboard) handleOverlords(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(wd.fleet.GetCachedStatus())
}

// handleTopology returns the global topology as JSON (cached).
func (wd *WebDashboard) handleTopology(w http.ResponseWriter, _ *http.Request) {
	cached := wd.fleet.GetCachedStatus()

	// Cached topology — uses heartbeat data, no live queries.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cached)
}

// handleHealth returns a simple health check.
func (wd *WebDashboard) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "healthy",
		"role":      "manager",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// handleRegionDetail returns detailed status for a specific Overlord/region (#10 drill-down).
func (wd *WebDashboard) handleRegionDetail(w http.ResponseWriter, r *http.Request) {
	// Extract overlord ID from URL: /api/region/{overlord_id}
	overlordID := r.URL.Path[len("/api/region/"):]
	if overlordID == "" {
		http.Error(w, "overlord_id required", http.StatusBadRequest)
		return
	}

	// Try live query for detailed data (with timeout).
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp, err := wd.fleet.GetRegionStatus(ctx, overlordID)
	if err != nil {
		// Fallback to cached data.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"overlord_id": overlordID,
			"error":       err.Error(),
			"source":      "cache_fallback",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleDashboard serves the main HTML dashboard page.
func (wd *WebDashboard) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboardHTML)
}
