/*-------------------------------------------------------------------------
 *
 * llm_priority_queue.go
 *	  llm_priority_queue.go implements severity-based LLM call
 *	  prioritization. Critical alerts are processed before warnings,
 *	  warnings before info.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/llm_priority_queue.go
 *
 *-------------------------------------------------------------------------
 */
// llm_priority_queue.go implements severity-based LLM call prioritization.
// Critical alerts are processed before warnings, warnings before info.
package cerebrate

import (
	"container/heap"
	"sync"
	"time"
)

// LLMCallPriority represents a queued LLM call with priority metadata.
type LLMCallPriority struct {
	ID             string
	Severity       string // "critical", "warning", "info"
	severityScore  int    // 3=critical, 2=warning, 1=info
	RequestTime    time.Time
	SourceOverlord string
	WorkerID       string
	Description    string
	index          int // heap index
}

// LLMPriorityQueue is a thread-safe priority queue for LLM calls.
type LLMPriorityQueue struct {
	mu   sync.Mutex
	heap llmCallHeap
}

// NewLLMPriorityQueue creates a new priority queue.
func NewLLMPriorityQueue() *LLMPriorityQueue {
	pq := &LLMPriorityQueue{}
	heap.Init(&pq.heap)
	return pq
}

// Enqueue adds a new LLM call request to the priority queue.
func (q *LLMPriorityQueue) Enqueue(call *LLMCallPriority) {
	call.severityScore = severityToScore(call.Severity)
	q.mu.Lock()
	defer q.mu.Unlock()
	heap.Push(&q.heap, call)
}

// Dequeue removes and returns the highest-priority LLM call.
// Returns nil if the queue is empty.
func (q *LLMPriorityQueue) Dequeue() *LLMCallPriority {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.heap.Len() == 0 {
		return nil
	}
	return heap.Pop(&q.heap).(*LLMCallPriority)
}

// Peek returns the highest-priority item without removing it.
func (q *LLMPriorityQueue) Peek() *LLMCallPriority {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.heap.Len() == 0 {
		return nil
	}
	return q.heap[0]
}

// Len returns the number of items in the queue.
func (q *LLMPriorityQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.heap.Len()
}

// Stats returns a summary of pending calls by severity.
type QueueStats struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Info     int `json:"info"`
}

func (q *LLMPriorityQueue) Stats() QueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	stats := QueueStats{Total: q.heap.Len()}
	for _, item := range q.heap {
		switch item.Severity {
		case "critical":
			stats.Critical++
		case "warning":
			stats.Warning++
		default:
			stats.Info++
		}
	}
	return stats
}

// severityToScore converts severity string to numeric priority (higher = more urgent).
func severityToScore(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

// llmCallHeap implements heap.Interface for priority ordering.
// Higher severity score first; ties broken by earlier request time.
type llmCallHeap []*LLMCallPriority

func (h llmCallHeap) Len() int { return len(h) }
func (h llmCallHeap) Less(i, j int) bool {
	if h[i].severityScore != h[j].severityScore {
		return h[i].severityScore > h[j].severityScore // higher severity first
	}
	return h[i].RequestTime.Before(h[j].RequestTime) // earlier request first
}
func (h llmCallHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *llmCallHeap) Push(x any) {
	n := len(*h)
	item := x.(*LLMCallPriority)
	item.index = n
	*h = append(*h, item)
}

func (h *llmCallHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	item.index = -1
	*h = old[:n-1]
	return item
}
