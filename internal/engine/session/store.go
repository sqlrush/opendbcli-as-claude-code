/*-------------------------------------------------------------------------
 *
 * store.go
 *	  SessionStore abstracts session persistence. Implementations must
 *	  be safe for concurrent use.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/session/store.go
 *
 *-------------------------------------------------------------------------
 */
package session

import "context"

// SessionStore abstracts session persistence.
// Implementations must be safe for concurrent use.
type SessionStore interface {
	// Save persists a session (create or update).
	Save(ctx context.Context, session *Session) error

	// Load retrieves a session by ID. Returns nil, nil if not found.
	Load(ctx context.Context, id SessionID) (*Session, error)

	// ListByInstance returns active sessions for an instance, ordered by
	// UpdatedAt descending. At most limit entries are returned.
	ListByInstance(ctx context.Context, instance string, limit int) ([]*Session, error)
}
