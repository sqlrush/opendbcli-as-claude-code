/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Re-export provider types for convenience. Callers can use
 *	  engine.Message or provider.Message interchangeably.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/types.go
 *
 *-------------------------------------------------------------------------
 */
package engine

import "github.com/sqlrush/opendb/internal/engine/provider"

// Re-export provider types for convenience.
// Callers can use engine.Message or provider.Message interchangeably.
type (
	Message          = provider.Message
	ToolCall         = provider.ToolCall
	ToolSchema       = provider.ToolSchema
	ThinkingBlock    = provider.ThinkingBlock
	CacheControl     = provider.CacheControl
	SystemPromptBlock = provider.SystemPromptBlock
	Request          = provider.Request
	Response         = provider.Response
	Usage            = provider.Usage
	CacheStats       = provider.CacheStats
	Stream           = provider.Stream
	StreamEvent      = provider.StreamEvent
	StreamEventType  = provider.StreamEventType
	ProviderAdapter  = provider.ProviderAdapter
)

// Re-export constants.
const (
	StreamTextDelta     = provider.StreamTextDelta
	StreamThinkingDelta = provider.StreamThinkingDelta
	StreamToolCallDelta = provider.StreamToolCallDelta
	StreamToolResult    = provider.StreamToolResult
	StreamDone          = provider.StreamDone
	StreamError         = provider.StreamError
)
