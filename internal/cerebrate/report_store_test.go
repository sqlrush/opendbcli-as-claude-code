/*-------------------------------------------------------------------------
 *
 * report_store_test.go
 *	  Test cases for report_store.go (cerebrate package):
 *	  TestReportStore_AddAndGet, TestReportStore_RingBufferEviction,
 *	  TestReportStore_ListWithSeverityFilter.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/report_store_test.go
 *
 *-------------------------------------------------------------------------
 */
package cerebrate

import (
	"fmt"
	"testing"
	"time"
)

func TestReportStore_AddAndGet(t *testing.T) {
	store := NewReportStore(10)

	entry := &ReportEntry{
		ID:        "rpt-001",
		Timestamp: time.Now(),
		Source:    "worker:w1",
		Severity:  "critical",
		Summary:   "Bad SQL detected",
		Markdown:  "# Report\n\nContent here.",
	}

	store.Add(entry)

	got := store.Get("rpt-001")
	if got == nil {
		t.Fatal("Get returned nil for existing entry")
	}
	if got.ID != "rpt-001" {
		t.Errorf("ID = %q, want rpt-001", got.ID)
	}
	if got.Summary != "Bad SQL detected" {
		t.Errorf("Summary = %q, want 'Bad SQL detected'", got.Summary)
	}
}

func TestReportStore_RingBufferEviction(t *testing.T) {
	maxSize := 5
	store := NewReportStore(maxSize)

	// Add maxSize+1 entries.
	for i := 0; i < maxSize+1; i++ {
		store.Add(&ReportEntry{
			ID:        fmt.Sprintf("rpt-%03d", i),
			Timestamp: time.Now(),
			Source:    "worker:w1",
			Severity:  "info",
			Summary:   fmt.Sprintf("Report %d", i),
		})
	}

	// Should have exactly maxSize entries.
	if store.Len() != maxSize {
		t.Errorf("Len = %d, want %d", store.Len(), maxSize)
	}

	// Oldest entry (rpt-000) should be evicted.
	if store.Get("rpt-000") != nil {
		t.Error("oldest entry should have been evicted")
	}

	// Newest entry (rpt-005) should exist.
	if store.Get("rpt-005") == nil {
		t.Error("newest entry should exist")
	}

	// Second oldest (rpt-001) should still exist.
	if store.Get("rpt-001") == nil {
		t.Error("second entry should still exist")
	}
}

func TestReportStore_ListWithSeverityFilter(t *testing.T) {
	store := NewReportStore(100)

	store.Add(&ReportEntry{ID: "rpt-1", Severity: "critical", Summary: "Critical issue"})
	store.Add(&ReportEntry{ID: "rpt-2", Severity: "warning", Summary: "Warning issue"})
	store.Add(&ReportEntry{ID: "rpt-3", Severity: "critical", Summary: "Another critical"})
	store.Add(&ReportEntry{ID: "rpt-4", Severity: "info", Summary: "Info message"})

	// Filter by critical.
	criticals := store.List(10, "critical")
	if len(criticals) != 2 {
		t.Errorf("critical count = %d, want 2", len(criticals))
	}
	for _, r := range criticals {
		if r.Severity != "critical" {
			t.Errorf("filtered entry severity = %q, want critical", r.Severity)
		}
	}

	// Filter by warning.
	warnings := store.List(10, "warning")
	if len(warnings) != 1 {
		t.Errorf("warning count = %d, want 1", len(warnings))
	}

	// No filter.
	all := store.List(10, "")
	if len(all) != 4 {
		t.Errorf("all count = %d, want 4", len(all))
	}

	// Limit.
	limited := store.List(2, "")
	if len(limited) != 2 {
		t.Errorf("limited count = %d, want 2", len(limited))
	}
}

func TestReportStore_GetNonexistent(t *testing.T) {
	store := NewReportStore(10)

	got := store.Get("does-not-exist")
	if got != nil {
		t.Errorf("Get nonexistent = %v, want nil", got)
	}
}

func TestReportStore_AddNil(t *testing.T) {
	store := NewReportStore(10)

	store.Add(nil)

	if store.Len() != 0 {
		t.Errorf("Len = %d after adding nil, want 0", store.Len())
	}
}

func TestReportStore_NewestFirst(t *testing.T) {
	store := NewReportStore(10)

	store.Add(&ReportEntry{ID: "old", Timestamp: time.Now()})
	store.Add(&ReportEntry{ID: "new", Timestamp: time.Now()})

	list := store.List(10, "")
	if len(list) != 2 {
		t.Fatalf("list length = %d, want 2", len(list))
	}
	if list[0].ID != "new" {
		t.Errorf("first entry = %q, want 'new' (newest first)", list[0].ID)
	}
	if list[1].ID != "old" {
		t.Errorf("second entry = %q, want 'old'", list[1].ID)
	}
}

func TestReportStore_DefaultMaxSize(t *testing.T) {
	store := NewReportStore(0)
	if store.maxSize != 100 {
		t.Errorf("default maxSize = %d, want 100", store.maxSize)
	}
}
