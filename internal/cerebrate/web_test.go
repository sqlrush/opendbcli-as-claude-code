/*-------------------------------------------------------------------------
 *
 * web_test.go
 *	  Test cases for web.go (cerebrate package): TestHandleHealth,
 *	  TestHandleFleetStatus, TestHandleOverlords.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/web_test.go
 *
 *-------------------------------------------------------------------------
 */
package cerebrate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// newTestDashboard creates a WebDashboard with 2 overlords for testing.
func newTestDashboard() *WebDashboard {
	fm := NewFleetManager("cerebrate-test")

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

	return NewWebDashboard(fm, ":0")
}

func TestHandleHealth(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	wd.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}

	if body["status"] != "healthy" {
		t.Errorf("status = %v, want %q", body["status"], "healthy")
	}
	if body["role"] != "manager" {
		t.Errorf("role = %v, want %q", body["role"], "manager")
	}
	if _, ok := body["timestamp"]; !ok {
		t.Error("missing timestamp field")
	}
}

func TestHandleFleetStatus(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/fleet", nil)
	rec := httptest.NewRecorder()

	wd.handleFleetStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}

	// overlords count
	if v, ok := body["overlords"].(float64); !ok || int(v) != 2 {
		t.Errorf("overlords = %v, want 2", body["overlords"])
	}
	// total_workers
	if v, ok := body["total_workers"].(float64); !ok || int(v) != 10 {
		t.Errorf("total_workers = %v, want 10", body["total_workers"])
	}
	// online_workers
	if v, ok := body["online_workers"].(float64); !ok || int(v) != 9 {
		t.Errorf("online_workers = %v, want 9", body["online_workers"])
	}
	// regions array
	regions, ok := body["regions"].([]any)
	if !ok {
		t.Fatalf("regions is not an array: %T", body["regions"])
	}
	if len(regions) != 2 {
		t.Errorf("regions length = %d, want 2", len(regions))
	}
	// timestamp
	if _, ok := body["timestamp"]; !ok {
		t.Error("missing timestamp field")
	}
}

func TestHandleOverlords(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/overlords", nil)
	rec := httptest.NewRecorder()

	wd.handleOverlords(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body []any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}

	if len(body) != 2 {
		t.Errorf("overlords count = %d, want 2", len(body))
	}

	// Verify each entry has expected fields.
	for i, entry := range body {
		m, ok := entry.(map[string]any)
		if !ok {
			t.Errorf("entry %d is not an object", i)
			continue
		}
		if _, ok := m["overlord_id"]; !ok {
			t.Errorf("entry %d missing overlord_id", i)
		}
		if _, ok := m["region"]; !ok {
			t.Errorf("entry %d missing region", i)
		}
	}
}

func TestHandleTopology(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/topology", nil)
	rec := httptest.NewRecorder()

	wd.handleTopology(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body []any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}

	if len(body) != 2 {
		t.Errorf("topology entries = %d, want 2", len(body))
	}

	// Verify overlord data is present in each entry.
	for i, entry := range body {
		m, ok := entry.(map[string]any)
		if !ok {
			t.Errorf("entry %d is not an object", i)
			continue
		}
		if _, ok := m["overlord_id"]; !ok {
			t.Errorf("entry %d missing overlord_id", i)
		}
		if _, ok := m["worker_count"]; !ok {
			t.Errorf("entry %d missing worker_count", i)
		}
		if _, ok := m["online"]; !ok {
			t.Errorf("entry %d missing online", i)
		}
	}
}

func TestHandleRegionDetail_MissingID(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/region/", nil)
	rec := httptest.NewRecorder()

	wd.handleRegionDetail(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	body := strings.TrimSpace(rec.Body.String())
	if !strings.Contains(body, "overlord_id required") {
		t.Errorf("body = %q, want to contain %q", body, "overlord_id required")
	}
}

func TestHandleDashboard(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	wd.handleDashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/html; charset=utf-8")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "OpenDB Autopilot") {
		t.Error("dashboard HTML does not contain 'OpenDB Autopilot'")
	}
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("dashboard HTML does not contain DOCTYPE")
	}
}
