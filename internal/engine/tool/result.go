/*-------------------------------------------------------------------------
 *
 * result.go
 *	  ResultHandler controls tool result sizes with dynamic budgets.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/result.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import "fmt"

// ResultHandler controls tool result sizes with dynamic budgets.
type ResultHandler struct {
	maxResultSize int    // Max chars per single tool result (default 4000)
	persistDir    string // Directory for large result persistence (optional)
}

// NewResultHandler creates a ResultHandler.
func NewResultHandler(maxResultSize int, persistDir string) *ResultHandler {
	if maxResultSize <= 0 {
		maxResultSize = 4000
	}
	return &ResultHandler{
		maxResultSize: maxResultSize,
		persistDir:    persistDir,
	}
}

// Process applies dynamic budget truncation to tool results.
// remainingTokens is the estimated remaining context window capacity.
// Returns a new slice; does not mutate the input.
func (h *ResultHandler) Process(results []ToolResult, remainingTokens int) []ToolResult {
	if len(results) == 0 {
		return results
	}

	// Dynamic budget: 30% of remaining context for all tool results.
	totalBudget := remainingTokens * 30 / 100
	if totalBudget < 2000 {
		totalBudget = 2000
	}

	perToolBudget := totalBudget / len(results)
	if perToolBudget > h.maxResultSize {
		perToolBudget = h.maxResultSize
	}
	if perToolBudget < 500 {
		perToolBudget = 500
	}

	processed := make([]ToolResult, len(results))
	for i, r := range results {
		processed[i] = h.processOne(r, perToolBudget)
	}
	return processed
}

// processOne truncates a single result if it exceeds the budget.
func (h *ResultHandler) processOne(r ToolResult, budget int) ToolResult {
	if r.Error != "" {
		return r // Don't truncate error messages
	}

	if len(r.Content) <= budget {
		return r // Within budget
	}

	// Create a new result (immutable)
	result := ToolResult{
		ToolCallID: r.ToolCallID,
		Name:       r.Name,
		Duration:   r.Duration,
	}

	result.Content = SmartTruncate(r.Content, budget)

	return result
}

// SmartTruncate keeps the head (70%) and tail (20%) of content,
// inserting an omission notice in the middle.
// This preserves the most important data (top results) and potential
// summary lines at the end, unlike a naive head-only truncation.
func SmartTruncate(content string, budget int) string {
	if len(content) <= budget {
		return content
	}

	// Reserve space for the omission notice (~60 chars)
	noticeReserve := 60
	usable := budget - noticeReserve
	if usable < 100 {
		// Budget too small for smart truncation, fall back to head-only
		if budget > 20 {
			return content[:budget-20] + "\n...(truncated)"
		}
		return content[:budget]
	}

	headSize := usable * 70 / 100
	tailSize := usable * 20 / 100

	// Ensure we don't exceed content length
	if headSize+tailSize >= len(content) {
		return content
	}

	head := content[:headSize]
	tail := content[len(content)-tailSize:]
	omitted := len(content) - headSize - tailSize

	return head +
		fmt.Sprintf("\n\n... (%d 字符已省略) ...\n\n", omitted) +
		tail
}
