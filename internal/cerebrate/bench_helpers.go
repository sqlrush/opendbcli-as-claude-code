/*-------------------------------------------------------------------------
 *
 * bench_helpers.go
 *	  bench_helpers.go exports internal handlers and setup helpers for
 *	  external benchmark tests in tests/benchmark/. These thin wrappers
 *	  avoid exposing internal implementation details beyond what
 *	  benchmarks need.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/bench_helpers.go
 *
 *-------------------------------------------------------------------------
 */
// bench_helpers.go exports internal handlers and setup helpers for external
// benchmark tests in tests/benchmark/. These thin wrappers avoid exposing
// internal implementation details beyond what benchmarks need.
package cerebrate

import (
	"net/http"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// SetOverlordForBench inserts a pre-built OverlordConnection into the
// FleetManager. Intended for benchmark setup only (no gRPC dial).
func (fm *FleetManager) SetOverlordForBench(
	id, region, addr string,
	state pb.AgentState,
	health *pb.HealthScore,
	workerCount, onlineCount int32,
) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.overlords[id] = &OverlordConnection{
		OverlordID:  id,
		Region:      region,
		Addr:        addr,
		State:       state,
		Health:      health,
		WorkerCount: workerCount,
		OnlineCount: onlineCount,
	}
}

// HandleFleetStatusForBench exposes handleFleetStatus for external benchmarks.
func (wd *WebDashboard) HandleFleetStatusForBench(w http.ResponseWriter, r *http.Request) {
	wd.handleFleetStatus(w, r)
}

// HandleTopologyForBench exposes handleTopology for external benchmarks.
func (wd *WebDashboard) HandleTopologyForBench(w http.ResponseWriter, r *http.Request) {
	wd.handleTopology(w, r)
}

// HandleTimelineForBench exposes handleTimeline for external benchmarks.
func (wd *WebDashboard) HandleTimelineForBench(w http.ResponseWriter, r *http.Request) {
	wd.handleTimeline(w, r)
}

// HandleReportListForBench exposes handleReportList for external benchmarks.
func (wd *WebDashboard) HandleReportListForBench(w http.ResponseWriter, r *http.Request) {
	wd.handleReportList(w, r)
}

// MarkdownToHTMLForBench exposes the internal markdownToHTML converter for benchmarks.
func MarkdownToHTMLForBench(md string) string {
	return markdownToHTML(md)
}
