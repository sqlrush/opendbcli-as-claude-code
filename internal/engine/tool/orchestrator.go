/*-------------------------------------------------------------------------
 *
 * orchestrator.go
 *	  Orchestrator executes tool calls with read-only concurrency and
 *	  write serialization.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/orchestrator.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/odberr"
)

// Orchestrator executes tool calls with read-only concurrency and write serialization.
type Orchestrator struct {
	executor       SkillExecutor
	maxConcurrency int

	// dedupCache caches read-only tool call results within a single
	// Engine run so the LLM doesn't waste a round re-executing the same
	// (skill, args) pair. Keyed by "name|argsJSON" → prior Content.
	// Write tools (SecurityLevel > 0) are never cached.
	dedupMu    sync.Mutex
	dedupCache map[string]string
}

// NewOrchestrator creates an Orchestrator.
// maxConcurrency controls the maximum number of concurrent read-only tool executions.
func NewOrchestrator(executor SkillExecutor, maxConcurrency int) *Orchestrator {
	if maxConcurrency <= 0 {
		maxConcurrency = 5
	}
	return &Orchestrator{
		executor:       executor,
		maxConcurrency: maxConcurrency,
		dedupCache:     make(map[string]string),
	}
}

// dedupKey builds the cache key for a tool call. Normalized args prevent
// trivial whitespace differences from defeating the cache.
func dedupKey(name, args string) string {
	return name + "|" + args
}

// ToolResult holds the output of a single tool execution.
type ToolResult struct {
	ToolCallID string
	Name       string
	Content    string
	Error      string
	Duration   time.Duration
}

// Execute runs a set of tool calls.
// Read-only tools (SecurityLevel <= 0) run concurrently.
// Write tools (SecurityLevel > 0) run serially after all reads complete.
func (o *Orchestrator) Execute(ctx context.Context, toolCalls []provider.ToolCall) []ToolResult {
	readOnly, writeOps := o.partition(toolCalls)

	results := make([]ToolResult, 0, len(toolCalls))

	if len(readOnly) > 0 {
		results = append(results, o.executeConcurrent(ctx, readOnly)...)
	}

	for _, tc := range writeOps {
		results = append(results, o.executeSingle(ctx, tc))
	}

	return results
}

// partition separates tool calls into read-only (concurrent) and write (serial) groups.
func (o *Orchestrator) partition(toolCalls []provider.ToolCall) (readOnly, writeOps []provider.ToolCall) {
	for _, tc := range toolCalls {
		level := o.executor.SecurityLevel(tc.Name)
		if level <= 0 {
			readOnly = append(readOnly, tc)
		} else {
			writeOps = append(writeOps, tc)
		}
	}
	return
}

// executeConcurrent runs read-only tools in parallel with a concurrency limit.
func (o *Orchestrator) executeConcurrent(ctx context.Context, toolCalls []provider.ToolCall) []ToolResult {
	sem := make(chan struct{}, o.maxConcurrency)
	resultCh := make(chan ToolResult, len(toolCalls))

	var wg sync.WaitGroup
	for _, tc := range toolCalls {
		wg.Add(1)
		tc := tc
		odberr.SafeGo(odberr.ErrDiagToolCall, func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			resultCh <- o.executeSingle(ctx, tc)
		})
	}

	wg.Wait()
	close(resultCh)

	results := make([]ToolResult, 0, len(toolCalls))
	for r := range resultCh {
		results = append(results, r)
	}

	// Sort by original order for deterministic output.
	orderIndex := make(map[string]int, len(toolCalls))
	for i, tc := range toolCalls {
		orderIndex[tc.ID] = i
	}
	sort.Slice(results, func(i, j int) bool {
		return orderIndex[results[i].ToolCallID] < orderIndex[results[j].ToolCallID]
	})

	return results
}

// executeSingle runs one tool call and captures the result. Read-only
// tools hit the dedup cache first; a hit short-circuits to the prior
// content with a hint so the LLM knows it already has this data.
func (o *Orchestrator) executeSingle(ctx context.Context, tc provider.ToolCall) ToolResult {
	start := time.Now()

	// Cache check for read-only tools only. We look up by (name, args);
	// write tools always go through since they have side effects.
	sec := o.executor.SecurityLevel(tc.Name)
	key := dedupKey(tc.Name, tc.Arguments)
	if sec <= 0 {
		o.dedupMu.Lock()
		cached, hit := o.dedupCache[key]
		o.dedupMu.Unlock()
		if hit {
			return ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    "⚠ 此工具本次已调用过（相同参数），返回缓存结果；避免重复调用:\n" + cached,
				Duration:   time.Since(start),
			}
		}
	}

	params := parseArguments(tc.Arguments)
	sr, err := o.executor.Execute(ctx, tc.Name, params)

	duration := time.Since(start)

	if err != nil {
		return ToolResult{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Error:      err.Error(),
			Duration:   duration,
		}
	}

	content := formatSkillResult(tc.Name, sr)

	// Store read-only results for subsequent dedup.
	if sec <= 0 {
		o.dedupMu.Lock()
		o.dedupCache[key] = content
		o.dedupMu.Unlock()
	}

	return ToolResult{
		ToolCallID: tc.ID,
		Name:       tc.Name,
		Content:    content,
		Duration:   duration,
	}
}

// parseArguments converts JSON arguments string to a map.
func parseArguments(args string) map[string]any {
	if args == "" || args == "{}" {
		return map[string]any{}
	}

	// Simple key extraction: most tool calls have {"args": "value"} format.
	// For robustness, use json.Unmarshal.
	var m map[string]any
	if err := jsonUnmarshal([]byte(args), &m); err != nil {
		// Fallback: treat entire string as the "args" value
		return map[string]any{"args": args}
	}
	return m
}

// formatSkillResult converts a SkillResult to text for the LLM.
func formatSkillResult(name string, sr *SkillResult) string {
	if sr == nil {
		return "(no result)"
	}

	if sr.Error != "" {
		return "Error: " + sr.Error
	}

	if sr.Rendered != "" {
		return sr.Rendered
	}

	if sr.Text != "" {
		return sr.Text
	}

	return sr.Summary
}
