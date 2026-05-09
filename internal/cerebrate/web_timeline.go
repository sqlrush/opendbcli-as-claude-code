/*-------------------------------------------------------------------------
 *
 * web_timeline.go
 *	  web_timeline.go provides the fault timeline data store and API
 *	  handler. Used by the Cerebrate dashboard to display recent events
 *	  chronologically.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/web_timeline.go
 *
 *-------------------------------------------------------------------------
 */
// web_timeline.go provides the fault timeline data store and API handler.
// Used by the Cerebrate dashboard to display recent events chronologically.
package cerebrate

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// TimelineEvent represents a single event on the fault timeline.
type TimelineEvent struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	OverlordID  string    `json:"overlord_id,omitempty"`
	WorkerID    string    `json:"worker_id,omitempty"`
	EventType   string    `json:"event_type"`   // "fault", "recovery", "escalation", "heartbeat_lost"
	Severity    string    `json:"severity"`      // "critical", "warning", "info"
	Description string    `json:"description"`
	ReportID    string    `json:"report_id,omitempty"` // link to full report
}

// TimelineStore is a thread-safe ring buffer for timeline events.
// Events are stored newest first. Immutable update semantics.
type TimelineStore struct {
	mu      sync.RWMutex
	events  []TimelineEvent // ring buffer, newest first
	maxSize int
}

// NewTimelineStore creates a new timeline store with the given capacity.
func NewTimelineStore(maxSize int) *TimelineStore {
	if maxSize <= 0 {
		maxSize = 500
	}
	return &TimelineStore{
		events:  make([]TimelineEvent, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add inserts a new event at the front of the ring buffer.
// Creates a new slice rather than mutating the existing one (immutable pattern).
func (ts *TimelineStore) Add(event TimelineEvent) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	newEvents := make([]TimelineEvent, 0, ts.maxSize)
	newEvents = append(newEvents, event)

	limit := ts.maxSize - 1
	if len(ts.events) < limit {
		limit = len(ts.events)
	}
	newEvents = append(newEvents, ts.events[:limit]...)

	ts.events = newEvents
}

// List returns up to limit events, optionally filtered by severity and overlordID.
// Empty filter strings match all events. Returns a new slice (immutable).
func (ts *TimelineStore) List(limit int, severity, overlordID string) []TimelineEvent {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	if limit <= 0 {
		limit = len(ts.events)
	}

	result := make([]TimelineEvent, 0, limit)
	for _, e := range ts.events {
		if severity != "" && e.Severity != severity {
			continue
		}
		if overlordID != "" && e.OverlordID != overlordID {
			continue
		}
		result = append(result, e)
		if len(result) >= limit {
			break
		}
	}
	return result
}

// Len returns the current number of stored events.
func (ts *TimelineStore) Len() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.events)
}

// handleTimeline serves GET /api/timeline?limit=50&severity=critical&overlord=xxx
func (wd *WebDashboard) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if wd.timeline == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]TimelineEvent{})
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	severity := r.URL.Query().Get("severity")
	overlordID := r.URL.Query().Get("overlord")

	events := wd.timeline.List(limit, severity, overlordID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}
