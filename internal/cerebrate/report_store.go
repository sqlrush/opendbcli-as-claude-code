/*-------------------------------------------------------------------------
 *
 * report_store.go
 *	  report_store.go implements a thread-safe in-memory report cache
 *	  for the Cerebrate web dashboard. Uses a ring buffer with immutable
 *	  update semantics.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/report_store.go
 *
 *-------------------------------------------------------------------------
 */
// report_store.go implements a thread-safe in-memory report cache for the
// Cerebrate web dashboard. Uses a ring buffer with immutable update semantics.
package cerebrate

import (
	"sync"
	"time"
)

// ReportStore is a thread-safe ring buffer cache for fault reports.
// Used by the Cerebrate web dashboard to serve recent reports via API.
type ReportStore struct {
	mu      sync.RWMutex
	reports []*ReportEntry // newest first
	maxSize int
}

// ReportEntry is one cached report in the store.
type ReportEntry struct {
	ID        string    // unique report ID
	Timestamp time.Time // report generation time
	Source    string    // "worker:{id}" or "overlord:{id}"
	Severity  string    // critical/warning/info
	Summary   string    // one-line summary
	Markdown  string    // full report content
}

// NewReportStore creates a new report store with the given maximum capacity.
func NewReportStore(maxSize int) *ReportStore {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &ReportStore{
		reports: make([]*ReportEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add inserts a new report entry. If the store is at capacity, the oldest
// entry is evicted. Uses immutable pattern: creates a new slice rather than
// mutating the existing one.
func (s *ReportStore) Add(entry *ReportEntry) {
	if entry == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Prepend new entry (newest first), then trim to maxSize.
	newReports := make([]*ReportEntry, 0, s.maxSize)
	newReports = append(newReports, entry)

	limit := s.maxSize - 1
	if len(s.reports) < limit {
		limit = len(s.reports)
	}
	newReports = append(newReports, s.reports[:limit]...)

	s.reports = newReports
}

// List returns up to `limit` report entries, optionally filtered by severity.
// If severity is empty, all entries are returned. Returns a new slice (immutable).
func (s *ReportStore) List(limit int, severity string) []*ReportEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = len(s.reports)
	}

	result := make([]*ReportEntry, 0, limit)
	for _, r := range s.reports {
		if severity != "" && r.Severity != severity {
			continue
		}
		result = append(result, r)
		if len(result) >= limit {
			break
		}
	}
	return result
}

// Get retrieves a single report by ID. Returns nil if not found.
func (s *ReportStore) Get(id string) *ReportEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, r := range s.reports {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// Len returns the current number of stored reports.
func (s *ReportStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.reports)
}
