/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Re-export provider types so context package uses them directly.
 *	  This replaces the local Message/ToolCall/etc. definitions that
 *	  were added to break the circular import (now resolved by moving
 *	  types to provider).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/types.go
 *
 *-------------------------------------------------------------------------
 */
package context

import "github.com/sqlrush/opendb/internal/engine/provider"

// Re-export provider types so context package uses them directly.
// This replaces the local Message/ToolCall/etc. definitions that were
// added to break the circular import (now resolved by moving types to provider).
type (
	Message          = provider.Message
	ToolCall         = provider.ToolCall
	ThinkingBlock    = provider.ThinkingBlock
	SystemPromptBlock = provider.SystemPromptBlock
	CacheControl     = provider.CacheControl
	Usage            = provider.Usage
)
