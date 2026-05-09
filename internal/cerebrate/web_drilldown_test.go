/*-------------------------------------------------------------------------
 *
 * web_drilldown_test.go
 *	  Test cases for web_drilldown.go (cerebrate package):
 *	  TestHandleTimeline_EmptyStore, TestHandleTimeline_WithEvents,
 *	  TestHandleTimeline_SeverityFilter.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/web_drilldown_test.go
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
	"time"
)

// --- Timeline tests ---

func TestHandleTimeline_EmptyStore(t *testing.T) {
	wd := newTestDashboard()
	wd.SetTimeline(NewTimelineStore(100))

	req := httptest.NewRequest(http.MethodGet, "/api/timeline", nil)
	rec := httptest.NewRecorder()

	wd.handleTimeline(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var events []TimelineEvent
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events count = %d, want 0", len(events))
	}
}

func TestHandleTimeline_WithEvents(t *testing.T) {
	wd := newTestDashboard()
	ts := NewTimelineStore(100)
	ts.Add(TimelineEvent{
		ID:          "evt-1",
		Timestamp:   time.Now().Add(-2 * time.Minute),
		OverlordID:  "overlord-1",
		WorkerID:    "worker-1",
		EventType:   "fault",
		Severity:    "critical",
		Description: "Bad SQL detected",
		ReportID:    "rpt-001",
	})
	ts.Add(TimelineEvent{
		ID:          "evt-2",
		Timestamp:   time.Now().Add(-1 * time.Minute),
		OverlordID:  "overlord-2",
		EventType:   "recovery",
		Severity:    "info",
		Description: "Worker recovered",
	})
	wd.SetTimeline(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/timeline", nil)
	rec := httptest.NewRecorder()

	wd.handleTimeline(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var events []TimelineEvent
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("events count = %d, want 2", len(events))
	}

	// Newest first.
	if events[0].ID != "evt-2" {
		t.Errorf("first event ID = %q, want evt-2 (newest first)", events[0].ID)
	}
	if events[0].EventType != "recovery" {
		t.Errorf("first event type = %q, want recovery", events[0].EventType)
	}
}

func TestHandleTimeline_SeverityFilter(t *testing.T) {
	wd := newTestDashboard()
	ts := NewTimelineStore(100)
	ts.Add(TimelineEvent{ID: "e1", Severity: "critical", EventType: "fault"})
	ts.Add(TimelineEvent{ID: "e2", Severity: "info", EventType: "recovery"})
	ts.Add(TimelineEvent{ID: "e3", Severity: "critical", EventType: "escalation"})
	wd.SetTimeline(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/timeline?severity=critical", nil)
	rec := httptest.NewRecorder()

	wd.handleTimeline(rec, req)

	var events []TimelineEvent
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("critical events = %d, want 2", len(events))
	}
	for _, e := range events {
		if e.Severity != "critical" {
			t.Errorf("event %s severity = %q, want critical", e.ID, e.Severity)
		}
	}
}

func TestHandleTimeline_LimitParam(t *testing.T) {
	wd := newTestDashboard()
	ts := NewTimelineStore(100)
	for i := 0; i < 10; i++ {
		ts.Add(TimelineEvent{ID: "e", Severity: "info", EventType: "fault"})
	}
	wd.SetTimeline(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/timeline?limit=3", nil)
	rec := httptest.NewRecorder()

	wd.handleTimeline(rec, req)

	var events []TimelineEvent
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("events count = %d, want 3", len(events))
	}
}

// --- Report tests ---

func TestHandleReportList(t *testing.T) {
	wd := newTestDashboard()
	rs := NewReportStore(100)
	rs.Add(&ReportEntry{ID: "rpt-1", Severity: "critical", Summary: "Issue 1", Markdown: "# R1"})
	rs.Add(&ReportEntry{ID: "rpt-2", Severity: "warning", Summary: "Issue 2", Markdown: "# R2"})
	wd.SetReports(rs)

	req := httptest.NewRequest(http.MethodGet, "/api/reports", nil)
	rec := httptest.NewRecorder()

	wd.handleReportList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var entries []*ReportEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("report count = %d, want 2", len(entries))
	}
}

func TestHandleReportDetail(t *testing.T) {
	wd := newTestDashboard()
	rs := NewReportStore(100)
	rs.Add(&ReportEntry{
		ID:       "rpt-detail",
		Severity: "critical",
		Summary:  "Bad SQL",
		Markdown: "# Bad SQL Report\n\nDetails here.",
	})
	wd.SetReports(rs)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/rpt-detail", nil)
	rec := httptest.NewRecorder()

	wd.handleReportDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entry ReportEntry
	if err := json.NewDecoder(rec.Body).Decode(&entry); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if entry.ID != "rpt-detail" {
		t.Errorf("ID = %q, want rpt-detail", entry.ID)
	}
	if entry.Markdown != "# Bad SQL Report\n\nDetails here." {
		t.Errorf("Markdown = %q, unexpected content", entry.Markdown)
	}
}

func TestHandleReportDetail_NotFound(t *testing.T) {
	wd := newTestDashboard()
	wd.SetReports(NewReportStore(100))

	req := httptest.NewRequest(http.MethodGet, "/api/reports/nonexistent", nil)
	rec := httptest.NewRecorder()

	wd.handleReportDetail(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	body := strings.TrimSpace(rec.Body.String())
	if !strings.Contains(body, "not found") {
		t.Errorf("body = %q, want to contain 'not found'", body)
	}
}

func TestHandleReportHTML(t *testing.T) {
	wd := newTestDashboard()
	rs := NewReportStore(100)
	rs.Add(&ReportEntry{
		ID:       "rpt-html",
		Severity: "critical",
		Summary:  "SQL Issue",
		Markdown: "## Summary\n\nBad SQL detected.\n\n```sql\nSELECT * FROM orders;\n```",
	})
	wd.SetReports(rs)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/rpt-html/html", nil)
	rec := httptest.NewRecorder()

	wd.handleReportDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/html; charset=utf-8")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("HTML does not contain DOCTYPE")
	}
	if !strings.Contains(body, "<h2>Summary</h2>") {
		t.Error("HTML does not contain rendered h2 header")
	}
	if !strings.Contains(body, "Copy") {
		t.Error("HTML does not contain Copy button for SQL code block")
	}
	if !strings.Contains(body, "SELECT * FROM orders;") {
		t.Error("HTML does not contain SQL content")
	}
}

// --- Worker/Region drill-down tests ---

func TestHandleWorkerDetail(t *testing.T) {
	wd := newTestDashboard()

	// The test fleet has overlord-1 and overlord-2 but no worker-level data
	// in the cached status (GetCachedStatus returns overlord-level maps only).
	// Worker detail relies on "workers" field in cached data. With the test
	// setup, we expect 404 since there are no worker entries in cache.
	req := httptest.NewRequest(http.MethodGet, "/api/worker/worker-1", nil)
	rec := httptest.NewRecorder()

	wd.handleWorkerDetail(rec, req)

	// No worker data in cache, so expect 404.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleWorkerDetail_MissingID(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/worker/", nil)
	rec := httptest.NewRecorder()

	wd.handleWorkerDetail(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleRegionReport(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/report/overlord-1", nil)
	rec := httptest.NewRecorder()

	wd.handleRegionReport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var report RegionReportEntry
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if report.OverlordID != "overlord-1" {
		t.Errorf("OverlordID = %q, want overlord-1", report.OverlordID)
	}
	if report.Region != "china-east" {
		t.Errorf("Region = %q, want china-east", report.Region)
	}
	if report.TotalWorkers != 5 {
		t.Errorf("TotalWorkers = %d, want 5", report.TotalWorkers)
	}
	if report.OnlineWorkers != 5 {
		t.Errorf("OnlineWorkers = %d, want 5", report.OnlineWorkers)
	}
	if report.HealthScore != 95 {
		t.Errorf("HealthScore = %d, want 95", report.HealthScore)
	}
	if report.GeneratedAt == "" {
		t.Error("GeneratedAt is empty")
	}
}

func TestHandleRegionReport_NotFound(t *testing.T) {
	wd := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/report/nonexistent", nil)
	rec := httptest.NewRecorder()

	wd.handleRegionReport(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// --- Markdown-to-HTML tests ---

func TestMarkdownToHTML_Headers(t *testing.T) {
	md := "# Title\n## Section\n### Subsection"
	html := markdownToHTML(md)

	if !strings.Contains(html, "<h1>Title</h1>") {
		t.Errorf("missing h1, got: %s", html)
	}
	if !strings.Contains(html, "<h2>Section</h2>") {
		t.Errorf("missing h2, got: %s", html)
	}
	if !strings.Contains(html, "<h3>Subsection</h3>") {
		t.Errorf("missing h3, got: %s", html)
	}
}

func TestMarkdownToHTML_CodeBlocks(t *testing.T) {
	md := "```sql\nSELECT 1;\n```"
	html := markdownToHTML(md)

	if !strings.Contains(html, "<pre") {
		t.Errorf("missing pre tag, got: %s", html)
	}
	if !strings.Contains(html, "<code") {
		t.Errorf("missing code tag, got: %s", html)
	}
	if !strings.Contains(html, "Copy") {
		t.Errorf("missing copy button for SQL, got: %s", html)
	}
	if !strings.Contains(html, "SELECT 1;") {
		t.Errorf("missing SQL content, got: %s", html)
	}

	// Non-SQL code blocks should not have copy button.
	md2 := "```go\nfmt.Println()\n```"
	html2 := markdownToHTML(md2)

	if strings.Contains(html2, "Copy") {
		t.Errorf("non-SQL code block should not have copy button, got: %s", html2)
	}
	if !strings.Contains(html2, "fmt.Println()") {
		t.Errorf("missing code content, got: %s", html2)
	}
}

func TestMarkdownToHTML_Tables(t *testing.T) {
	md := "| Name | Value |\n|------|-------|\n| cpu | 85% |\n| mem | 60% |"
	html := markdownToHTML(md)

	if !strings.Contains(html, "<table") {
		t.Errorf("missing table tag, got: %s", html)
	}
	if !strings.Contains(html, "<th") {
		t.Errorf("missing th tag, got: %s", html)
	}
	if !strings.Contains(html, "<td") {
		t.Errorf("missing td tag, got: %s", html)
	}
	if !strings.Contains(html, "cpu") {
		t.Errorf("missing table data 'cpu', got: %s", html)
	}
	if !strings.Contains(html, "85%") {
		t.Errorf("missing table data '85%%', got: %s", html)
	}
}

func TestMarkdownToHTML_BulletList(t *testing.T) {
	md := "- item one\n- item two\n* item three"
	html := markdownToHTML(md)

	if !strings.Contains(html, "<ul>") {
		t.Errorf("missing ul tag, got: %s", html)
	}
	if !strings.Contains(html, "<li>item one</li>") {
		t.Errorf("missing li for item one, got: %s", html)
	}
	if !strings.Contains(html, "<li>item two</li>") {
		t.Errorf("missing li for item two, got: %s", html)
	}
	if !strings.Contains(html, "<li>item three</li>") {
		t.Errorf("missing li for item three, got: %s", html)
	}
}

func TestMarkdownToHTML_HTMLEscape(t *testing.T) {
	md := "## Test <script>alert('xss')</script>"
	html := markdownToHTML(md)

	if strings.Contains(html, "<script>") {
		t.Errorf("HTML should be escaped, got: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("should contain escaped script tag, got: %s", html)
	}
}

// --- Timeline store unit tests ---

func TestTimelineStore_AddAndList(t *testing.T) {
	ts := NewTimelineStore(5)

	ts.Add(TimelineEvent{ID: "a", Severity: "info"})
	ts.Add(TimelineEvent{ID: "b", Severity: "critical"})

	if ts.Len() != 2 {
		t.Errorf("Len = %d, want 2", ts.Len())
	}

	all := ts.List(10, "", "")
	if len(all) != 2 {
		t.Fatalf("List len = %d, want 2", len(all))
	}
	// Newest first.
	if all[0].ID != "b" {
		t.Errorf("first = %q, want b (newest)", all[0].ID)
	}
}

func TestTimelineStore_RingBuffer(t *testing.T) {
	ts := NewTimelineStore(3)

	ts.Add(TimelineEvent{ID: "1"})
	ts.Add(TimelineEvent{ID: "2"})
	ts.Add(TimelineEvent{ID: "3"})
	ts.Add(TimelineEvent{ID: "4"}) // Should evict "1".

	if ts.Len() != 3 {
		t.Errorf("Len = %d, want 3", ts.Len())
	}

	all := ts.List(10, "", "")
	ids := make([]string, len(all))
	for i, e := range all {
		ids[i] = e.ID
	}
	// Should be 4, 3, 2 (newest first, "1" evicted).
	if ids[0] != "4" || ids[1] != "3" || ids[2] != "2" {
		t.Errorf("IDs = %v, want [4, 3, 2]", ids)
	}
}

func TestTimelineStore_DefaultMaxSize(t *testing.T) {
	ts := NewTimelineStore(0)
	if ts.maxSize != 500 {
		t.Errorf("default maxSize = %d, want 500", ts.maxSize)
	}
}

func TestTimelineStore_OverlordFilter(t *testing.T) {
	ts := NewTimelineStore(100)
	ts.Add(TimelineEvent{ID: "a", OverlordID: "o1", Severity: "info"})
	ts.Add(TimelineEvent{ID: "b", OverlordID: "o2", Severity: "info"})
	ts.Add(TimelineEvent{ID: "c", OverlordID: "o1", Severity: "critical"})

	filtered := ts.List(10, "", "o1")
	if len(filtered) != 2 {
		t.Errorf("o1 events = %d, want 2", len(filtered))
	}
	for _, e := range filtered {
		if e.OverlordID != "o1" {
			t.Errorf("event %s overlord = %q, want o1", e.ID, e.OverlordID)
		}
	}
}

// --- Helper test: nil timeline ---

func TestHandleTimeline_NilStore(t *testing.T) {
	wd := newTestDashboard()
	// timeline is nil by default.

	req := httptest.NewRequest(http.MethodGet, "/api/timeline", nil)
	rec := httptest.NewRecorder()

	wd.handleTimeline(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var events []TimelineEvent
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %d, want 0 for nil store", len(events))
	}
}
