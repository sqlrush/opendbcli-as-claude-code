/*-------------------------------------------------------------------------
 *
 * repl_alert_buffer.go
 *	  AlertEntry represents one exception captured from Sentinel or
 *	  Scheduler.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_alert_buffer.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"sync"
	"time"
)

const maxAlertEntries = 50

// AlertEntry represents one exception captured from Sentinel or Scheduler.
type AlertEntry struct {
	Source    string    // "Sentinel" or "Scheduler"
	Summary  string    // e.g. "Lock Wait冲高 13→31", "USERS 92%"
	Timestamp time.Time
	Index     int // sentinel report index (for /diag N), or -1
}

// alertBuffer is a ring buffer of recent exceptions for /diag and /rule dropdowns.
type alertBuffer struct {
	mu      sync.Mutex
	entries []AlertEntry
}

func newAlertBuffer() *alertBuffer {
	return &alertBuffer{}
}

// Push adds an entry. Oldest entries are evicted when buffer exceeds maxAlertEntries.
func (b *alertBuffer) Push(entry AlertEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, entry)
	if len(b.entries) > maxAlertEntries {
		b.entries = b.entries[len(b.entries)-maxAlertEntries:]
	}
}

// Entries returns a copy of all entries, newest first.
func (b *alertBuffer) Entries() []AlertEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]AlertEntry, len(b.entries))
	for i, e := range b.entries {
		result[len(b.entries)-1-i] = e // reverse: newest first
	}
	return result
}

// Len returns the number of entries.
func (b *alertBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}
