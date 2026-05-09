/*-------------------------------------------------------------------------
 *
 * provider.go
 *	  provider — Models the Provider and Stream used inside llm.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/llm/provider.go
 *
 *-------------------------------------------------------------------------
 */
package llm

import "context"

type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*Response, error)
	ChatStream(ctx context.Context, req ChatRequest) (Stream, error)
	Name() string
}

type Stream interface {
	Next() (StreamEvent, error)
	Close() error
}
