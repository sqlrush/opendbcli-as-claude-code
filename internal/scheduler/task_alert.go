/*-------------------------------------------------------------------------
 *
 * task_alert.go
 *	  AlertRecord records a detected ORA error for dedup.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/task_alert.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ORA error severity classification.
var oraClassification = map[string]string{
	"ORA-00060": "WARN",     // deadlock
	"ORA-01555": "WARN",     // snapshot too old
	"ORA-04031": "CRITICAL", // shared pool exhaustion
	"ORA-00600": "CRITICAL", // internal error
	"ORA-07445": "CRITICAL", // internal error
	"ORA-01652": "CRITICAL", // unable to extend temp segment
	"ORA-01653": "CRITICAL", // unable to extend table
	"ORA-01654": "CRITICAL", // unable to extend index
	"ORA-04030": "CRITICAL", // PGA out of memory
	"ORA-01578": "CRITICAL", // data block corruption
	"ORA-27468": "WARN",     // scheduler job wait
}

// AlertRecord records a detected ORA error for dedup.
type AlertRecord struct {
	ORACode   string
	Severity  string
	Message   string
	FirstSeen time.Time
	Count     int
}

// AlertDedup provides 5-minute deduplication for ORA errors.
type AlertDedup struct {
	mu      sync.Mutex
	seen    map[string]time.Time // ORA code -> last alert time
	window  time.Duration
}

// NewAlertDedup creates a dedup with the given window.
func NewAlertDedup(window time.Duration) *AlertDedup {
	return &AlertDedup{
		seen:   make(map[string]time.Time),
		window: window,
	}
}

// ShouldAlert returns true if this ORA code hasn't been alerted within the window.
func (d *AlertDedup) ShouldAlert(oraCode string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Clean stale entries.
	for code, t := range d.seen {
		if now.Sub(t) > d.window {
			delete(d.seen, code)
		}
	}

	last, ok := d.seen[oraCode]
	if ok && now.Sub(last) < d.window {
		return false
	}
	d.seen[oraCode] = now
	return true
}

// ClassifyORAError extracts ORA codes from message text and classifies severity.
func ClassifyORAError(message string) []AlertRecord {
	var results []AlertRecord
	// Scan for ORA- patterns.
	msg := strings.ToUpper(message)
	for code, severity := range oraClassification {
		if strings.Contains(msg, code) {
			results = append(results, AlertRecord{
				ORACode:  code,
				Severity: severity,
				Message:  truncate(message, 120),
			})
		}
	}

	// Generic ORA- errors not in the classification map → INFO.
	if len(results) == 0 && strings.Contains(msg, "ORA-") {
		// Extract the ORA code.
		idx := strings.Index(msg, "ORA-")
		if idx >= 0 {
			end := idx + 9 // "ORA-NNNNN"
			if end > len(msg) {
				end = len(msg)
			}
			code := msg[idx:end]
			results = append(results, AlertRecord{
				ORACode:  code,
				Severity: "INFO",
				Message:  truncate(message, 120),
			})
		}
	}

	return results
}

// FormatAlertRecords returns a human-readable alert string.
func FormatAlertRecords(records []AlertRecord) string {
	if len(records) == 0 {
		return ""
	}
	var parts []string
	for _, r := range records {
		parts = append(parts, fmt.Sprintf("[%s] %s: %s", r.Severity, r.ORACode, r.Message))
	}
	return strings.Join(parts, "\n")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
