/*-------------------------------------------------------------------------
 *
 * orchestrator_test.go
 *	  Test cases for orchestrator.go (tool package):
 *	  TestPartitionReadOnlyAndWrite, TestExecuteAllReadOnly,
 *	  TestExecuteConcurrentFasterThanSerial.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/orchestrator_test.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// ── Mock executor ──

type mockExecutor struct {
	results        map[string]*SkillResult
	securityLevels map[string]int
	callCount      atomic.Int32
	delay          time.Duration // Simulate slow queries
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{
		results:        make(map[string]*SkillResult),
		securityLevels: make(map[string]int),
	}
}

func (m *mockExecutor) Execute(ctx context.Context, name string, params map[string]any) (*SkillResult, error) {
	m.callCount.Add(1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if r, ok := m.results[name]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

func (m *mockExecutor) SecurityLevel(name string) int {
	if level, ok := m.securityLevels[name]; ok {
		return level
	}
	return 0 // Default: read-only
}

// ── Tests ──

func TestPartitionReadOnlyAndWrite(t *testing.T) {
	mock := newMockExecutor()
	mock.securityLevels["waits"] = 0      // read-only
	mock.securityLevels["topsql"] = 0     // read-only
	mock.securityLevels["kill"] = 1       // write
	mock.securityLevels["alter"] = 2      // write

	orch := NewOrchestrator(mock, 5)

	calls := []provider.ToolCall{
		{ID: "1", Name: "waits"},
		{ID: "2", Name: "kill"},
		{ID: "3", Name: "topsql"},
		{ID: "4", Name: "alter"},
	}

	readOnly, writeOps := orch.partition(calls)

	if len(readOnly) != 2 {
		t.Errorf("expected 2 read-only, got %d", len(readOnly))
	}
	if len(writeOps) != 2 {
		t.Errorf("expected 2 write ops, got %d", len(writeOps))
	}

	if readOnly[0].Name != "waits" || readOnly[1].Name != "topsql" {
		t.Errorf("unexpected read-only order: %v", readOnly)
	}
	if writeOps[0].Name != "kill" || writeOps[1].Name != "alter" {
		t.Errorf("unexpected write order: %v", writeOps)
	}
}

func TestExecuteAllReadOnly(t *testing.T) {
	mock := newMockExecutor()
	mock.results["waits"] = &SkillResult{Text: "db file sequential read 52%"}
	mock.results["topsql"] = &SkillResult{Text: "SQL_ID=abc, elapsed=823ms"}
	mock.results["activesessions"] = &SkillResult{Text: "15 active sessions"}

	orch := NewOrchestrator(mock, 5)

	calls := []provider.ToolCall{
		{ID: "1", Name: "waits"},
		{ID: "2", Name: "topsql"},
		{ID: "3", Name: "activesessions"},
	}

	results := orch.Execute(context.Background(), calls)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Results should be in original order (deterministic)
	if results[0].Name != "waits" {
		t.Errorf("expected first result 'waits', got %q", results[0].Name)
	}
	if results[1].Name != "topsql" {
		t.Errorf("expected second result 'topsql', got %q", results[1].Name)
	}
	if results[2].Name != "activesessions" {
		t.Errorf("expected third result 'activesessions', got %q", results[2].Name)
	}
}

func TestExecuteConcurrentFasterThanSerial(t *testing.T) {
	mock := newMockExecutor()
	mock.delay = 50 * time.Millisecond
	mock.results["a"] = &SkillResult{Text: "a"}
	mock.results["b"] = &SkillResult{Text: "b"}
	mock.results["c"] = &SkillResult{Text: "c"}

	orch := NewOrchestrator(mock, 5)

	calls := []provider.ToolCall{
		{ID: "1", Name: "a"},
		{ID: "2", Name: "b"},
		{ID: "3", Name: "c"},
	}

	start := time.Now()
	results := orch.Execute(context.Background(), calls)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Concurrent should be ~50ms (max of 3 parallel), not ~150ms (3 serial).
	// Allow some margin for CI environments.
	if elapsed > 120*time.Millisecond {
		t.Errorf("concurrent execution took %v, expected < 120ms (3x50ms serial would be 150ms)", elapsed)
	}
}

func TestExecuteMixedReadWrite(t *testing.T) {
	mock := newMockExecutor()
	mock.results["waits"] = &SkillResult{Text: "waits result"}
	mock.results["topsql"] = &SkillResult{Text: "topsql result"}
	mock.results["kill"] = &SkillResult{Text: "session killed"}
	mock.securityLevels["kill"] = 1

	orch := NewOrchestrator(mock, 5)

	calls := []provider.ToolCall{
		{ID: "1", Name: "waits"},
		{ID: "2", Name: "kill"},
		{ID: "3", Name: "topsql"},
	}

	results := orch.Execute(context.Background(), calls)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Read-only results come first (waits, topsql), then write (kill)
	readResults := results[:2]
	writeResult := results[2]

	for _, r := range readResults {
		if r.Name != "waits" && r.Name != "topsql" {
			t.Errorf("expected read-only result, got %q", r.Name)
		}
	}
	if writeResult.Name != "kill" {
		t.Errorf("expected write result 'kill', got %q", writeResult.Name)
	}
}

func TestExecuteToolError(t *testing.T) {
	mock := newMockExecutor()
	// "badtool" not in results → will return error

	orch := NewOrchestrator(mock, 5)

	calls := []provider.ToolCall{
		{ID: "1", Name: "badtool", Arguments: "{}"},
	}

	results := orch.Execute(context.Background(), calls)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == "" {
		t.Error("expected error in result")
	}
	if !strings.Contains(results[0].Error, "not found") {
		t.Errorf("expected 'not found' in error, got %q", results[0].Error)
	}
}

func TestExecuteRenderedPreferred(t *testing.T) {
	mock := newMockExecutor()
	mock.results["waits"] = &SkillResult{
		Text:     "raw text",
		Rendered: "formatted output",
		Summary:  "summary",
	}

	orch := NewOrchestrator(mock, 5)

	results := orch.Execute(context.Background(), []provider.ToolCall{
		{ID: "1", Name: "waits"},
	})

	if results[0].Content != "formatted output" {
		t.Errorf("expected Rendered to be preferred, got %q", results[0].Content)
	}
}

func TestParseArguments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKey  string
		wantVal  string
	}{
		{"empty", "", "", ""},
		{"empty object", "{}", "", ""},
		{"simple args", `{"args":"5000"}`, "args", "5000"},
		{"sql args", `{"args":"SELECT 1 FROM dual"}`, "args", "SELECT 1 FROM dual"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parseArguments(tt.input)
			if tt.wantKey != "" {
				v, ok := m[tt.wantKey]
				if !ok {
					t.Errorf("expected key %q", tt.wantKey)
				}
				if fmt.Sprintf("%v", v) != tt.wantVal {
					t.Errorf("expected %q=%q, got %q", tt.wantKey, tt.wantVal, v)
				}
			}
		})
	}
}

func TestConcurrencyLimit(t *testing.T) {
	var maxConcurrent atomic.Int32
	var current atomic.Int32

	cm := newMockExecutor()

	// We need a custom executor to track concurrency
	tracker := &concurrencyTracker{
		inner:         cm,
		maxConcurrent: &maxConcurrent,
		current:       &current,
	}

	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("tool%d", i)
		cm.results[name] = &SkillResult{Text: name}
	}

	orch := NewOrchestrator(tracker, 3) // Limit to 3

	calls := make([]provider.ToolCall, 10)
	for i := 0; i < 10; i++ {
		calls[i] = provider.ToolCall{ID: fmt.Sprintf("%d", i), Name: fmt.Sprintf("tool%d", i)}
	}

	orch.Execute(context.Background(), calls)

	if maxConcurrent.Load() > 3 {
		t.Errorf("max concurrent was %d, expected <= 3", maxConcurrent.Load())
	}
}

type concurrencyTracker struct {
	inner         SkillExecutor
	maxConcurrent *atomic.Int32
	current       *atomic.Int32
}

func (c *concurrencyTracker) Execute(ctx context.Context, name string, params map[string]any) (*SkillResult, error) {
	cur := c.current.Add(1)
	for {
		old := c.maxConcurrent.Load()
		if cur <= old {
			break
		}
		if c.maxConcurrent.CompareAndSwap(old, cur) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond) // Hold the slot briefly
	c.current.Add(-1)
	return c.inner.Execute(ctx, name, params)
}

func (c *concurrencyTracker) SecurityLevel(name string) int {
	return c.inner.SecurityLevel(name)
}
