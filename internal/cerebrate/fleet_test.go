/*-------------------------------------------------------------------------
 *
 * fleet_test.go
 *	  Test cases for fleet.go (cerebrate package):
 *	  TestFleetManager_Basic, TestFleetManager_GetCachedStatus,
 *	  TestFleetManager_OverlordIDs.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/fleet_test.go
 *
 *-------------------------------------------------------------------------
 */
package cerebrate

import (
	"testing"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

func TestFleetManager_Basic(t *testing.T) {
	fm := NewFleetManager("cerebrate-test")

	if fm.OverlordCount() != 0 {
		t.Errorf("initial count = %d, want 0", fm.OverlordCount())
	}
	if fm.TotalWorkerCount() != 0 {
		t.Errorf("initial workers = %d, want 0", fm.TotalWorkerCount())
	}
}

func TestFleetManager_GetCachedStatus(t *testing.T) {
	fm := NewFleetManager("cerebrate-test")

	// Simulate Overlord data (as if received via heartbeat).
	fm.mu.Lock()
	fm.overlords["overlord-1"] = &OverlordConnection{
		OverlordID:  "overlord-1",
		Region:      "china-east",
		Addr:        "overlord-1:9200",
		State:       pb.AgentState_STATE_RUNNING,
		Health:      &pb.HealthScore{Score: 95, Summary: "5/5 online"},
		WorkerCount: 5,
		OnlineCount: 5,
	}
	fm.overlords["overlord-2"] = &OverlordConnection{
		OverlordID:  "overlord-2",
		Region:      "china-north",
		Addr:        "overlord-2:9201",
		State:       pb.AgentState_STATE_RUNNING,
		Health:      &pb.HealthScore{Score: 80, Summary: "4/5 online"},
		WorkerCount: 5,
		OnlineCount: 4,
	}
	fm.mu.Unlock()

	if fm.OverlordCount() != 2 {
		t.Errorf("count = %d, want 2", fm.OverlordCount())
	}
	if fm.TotalWorkerCount() != 10 {
		t.Errorf("total workers = %d, want 10", fm.TotalWorkerCount())
	}
	if fm.TotalOnlineCount() != 9 {
		t.Errorf("online = %d, want 9", fm.TotalOnlineCount())
	}

	cached := fm.GetCachedStatus()
	if len(cached) != 2 {
		t.Fatalf("cached entries = %d, want 2", len(cached))
	}
}

func TestFleetManager_OverlordIDs(t *testing.T) {
	fm := NewFleetManager("cerebrate-test")

	fm.mu.Lock()
	fm.overlords["o1"] = &OverlordConnection{OverlordID: "o1"}
	fm.overlords["o2"] = &OverlordConnection{OverlordID: "o2"}
	fm.mu.Unlock()

	ids := fm.OverlordIDs()
	if len(ids) != 2 {
		t.Errorf("ids count = %d, want 2", len(ids))
	}
}
