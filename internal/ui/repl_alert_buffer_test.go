/*-------------------------------------------------------------------------
 *
 * repl_alert_buffer_test.go
 *	  Test cases for repl_alert_buffer.go (ui package):
 *	  TestAlertBuffer_Empty, TestAlertBuffer_Push,
 *	  TestAlertBuffer_MaxSize.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_alert_buffer_test.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"testing"
	"time"
)

func TestAlertBuffer_Empty(t *testing.T) {
	buf := newAlertBuffer()
	entries := buf.Entries()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
	if buf.Len() != 0 {
		t.Fatalf("expected Len() == 0, got %d", buf.Len())
	}
}

func TestAlertBuffer_Push(t *testing.T) {
	buf := newAlertBuffer()
	now := time.Now()

	buf.Push(AlertEntry{Source: "Sentinel", Summary: "A", Timestamp: now, Index: 1})
	buf.Push(AlertEntry{Source: "Sentinel", Summary: "B", Timestamp: now.Add(time.Second), Index: 2})
	buf.Push(AlertEntry{Source: "Scheduler", Summary: "C", Timestamp: now.Add(2 * time.Second), Index: -1})

	entries := buf.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// newest first
	if entries[0].Summary != "C" {
		t.Errorf("expected entries[0].Summary == C, got %q", entries[0].Summary)
	}
	if entries[1].Summary != "B" {
		t.Errorf("expected entries[1].Summary == B, got %q", entries[1].Summary)
	}
	if entries[2].Summary != "A" {
		t.Errorf("expected entries[2].Summary == A, got %q", entries[2].Summary)
	}
}

func TestAlertBuffer_MaxSize(t *testing.T) {
	buf := newAlertBuffer()
	for i := 0; i < 60; i++ {
		buf.Push(AlertEntry{
			Source:    "Sentinel",
			Summary:   "entry",
			Timestamp: time.Now(),
			Index:     i,
		})
	}
	if buf.Len() != maxAlertEntries {
		t.Fatalf("expected Len() == %d, got %d", maxAlertEntries, buf.Len())
	}
	// Oldest entries (index 0-9) should be evicted; first remaining is index 10.
	entries := buf.Entries()
	// newest first: last pushed was index 59
	oldest := entries[len(entries)-1]
	if oldest.Index != 10 {
		t.Errorf("expected oldest entry Index == 10, got %d", oldest.Index)
	}
}

func TestAlertBuffer_NewestFirst(t *testing.T) {
	buf := newAlertBuffer()
	buf.Push(AlertEntry{Source: "Sentinel", Summary: "A", Timestamp: time.Now(), Index: -1})
	buf.Push(AlertEntry{Source: "Sentinel", Summary: "B", Timestamp: time.Now(), Index: -1})

	entries := buf.Entries()
	if entries[0].Summary != "B" {
		t.Errorf("expected newest entry first (B), got %q", entries[0].Summary)
	}
	if entries[1].Summary != "A" {
		t.Errorf("expected oldest entry second (A), got %q", entries[1].Summary)
	}
}
