/*-------------------------------------------------------------------------
 *
 * llm_priority_queue_test.go
 *	  Test cases for llm_priority_queue.go (cerebrate package):
 *	  TestLLMPriorityQueue_SeverityOrdering,
 *	  TestLLMPriorityQueue_TimeOrdering, TestLLMPriorityQueue_Stats.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/llm_priority_queue_test.go
 *
 *-------------------------------------------------------------------------
 */
package cerebrate

import (
	"testing"
	"time"
)

func TestLLMPriorityQueue_SeverityOrdering(t *testing.T) {
	q := NewLLMPriorityQueue()
	now := time.Now()

	// Enqueue in reverse priority order
	q.Enqueue(&LLMCallPriority{ID: "info-1", Severity: "info", RequestTime: now})
	q.Enqueue(&LLMCallPriority{ID: "critical-1", Severity: "critical", RequestTime: now})
	q.Enqueue(&LLMCallPriority{ID: "warning-1", Severity: "warning", RequestTime: now})

	if q.Len() != 3 {
		t.Fatalf("expected len 3, got %d", q.Len())
	}

	// Dequeue should return critical first
	call := q.Dequeue()
	if call.ID != "critical-1" {
		t.Errorf("expected critical-1 first, got %s", call.ID)
	}

	call = q.Dequeue()
	if call.ID != "warning-1" {
		t.Errorf("expected warning-1 second, got %s", call.ID)
	}

	call = q.Dequeue()
	if call.ID != "info-1" {
		t.Errorf("expected info-1 third, got %s", call.ID)
	}

	if q.Dequeue() != nil {
		t.Error("expected nil from empty queue")
	}
}

func TestLLMPriorityQueue_TimeOrdering(t *testing.T) {
	q := NewLLMPriorityQueue()
	now := time.Now()

	// Same severity, different times — earlier should come first
	q.Enqueue(&LLMCallPriority{ID: "late", Severity: "warning", RequestTime: now.Add(10 * time.Second)})
	q.Enqueue(&LLMCallPriority{ID: "early", Severity: "warning", RequestTime: now})

	call := q.Dequeue()
	if call.ID != "early" {
		t.Errorf("expected early first, got %s", call.ID)
	}
}

func TestLLMPriorityQueue_Stats(t *testing.T) {
	q := NewLLMPriorityQueue()
	now := time.Now()

	q.Enqueue(&LLMCallPriority{ID: "c1", Severity: "critical", RequestTime: now})
	q.Enqueue(&LLMCallPriority{ID: "c2", Severity: "critical", RequestTime: now})
	q.Enqueue(&LLMCallPriority{ID: "w1", Severity: "warning", RequestTime: now})
	q.Enqueue(&LLMCallPriority{ID: "i1", Severity: "info", RequestTime: now})

	stats := q.Stats()
	if stats.Total != 4 {
		t.Errorf("expected total 4, got %d", stats.Total)
	}
	if stats.Critical != 2 {
		t.Errorf("expected 2 critical, got %d", stats.Critical)
	}
	if stats.Warning != 1 {
		t.Errorf("expected 1 warning, got %d", stats.Warning)
	}
	if stats.Info != 1 {
		t.Errorf("expected 1 info, got %d", stats.Info)
	}
}

func TestLLMPriorityQueue_Peek(t *testing.T) {
	q := NewLLMPriorityQueue()
	if q.Peek() != nil {
		t.Error("expected nil peek on empty queue")
	}

	q.Enqueue(&LLMCallPriority{ID: "x", Severity: "critical", RequestTime: time.Now()})
	peek := q.Peek()
	if peek == nil || peek.ID != "x" {
		t.Error("expected peek to return x")
	}
	if q.Len() != 1 {
		t.Error("peek should not remove item")
	}
}

func TestLLMPriorityQueue_EmptyDequeue(t *testing.T) {
	q := NewLLMPriorityQueue()
	if q.Dequeue() != nil {
		t.Error("expected nil from empty queue")
	}
}
