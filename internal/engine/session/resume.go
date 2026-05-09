/*-------------------------------------------------------------------------
 *
 * resume.go
 *	  ResumeOrNew returns the most recent active session ID for the
 *	  instance if one exists within ResumeMaxAge, otherwise mints a
 *	  fresh ID. This implements the "connection-level session" semantics
 *	  (one session shared across multiple /llm invocations on the same
 *	  instance) — without it, batch-mode `opendb -c og "/llm ..."`
 *	  would start a new session every invocation and drop the previous
 *	  context.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/session/resume.go
 *
 *-------------------------------------------------------------------------
 */
package session

import (
	"context"
	"time"
)

// ResumeMaxAge caps how old an active session can be before we treat it
// as stale and start fresh. 24h keeps the "same conversation across a day
// of work" model natural while preventing infinite accumulation.
const ResumeMaxAge = 24 * time.Hour

// ResumeOrNew returns the most recent active session ID for the instance if
// one exists within ResumeMaxAge, otherwise mints a fresh ID. This
// implements the "connection-level session" semantics (one session shared
// across multiple /llm invocations on the same instance) — without it,
// batch-mode `opendb -c og "/llm ..."` would start a new session every
// invocation and drop the previous context.
//
// The store is queried best-effort; any error falls through to NewSessionID
// so a broken store never blocks a diagnosis run.
func ResumeOrNew(ctx context.Context, store SessionStore, instance string) SessionID {
	if store == nil || instance == "" {
		return NewSessionID(instance)
	}
	sessions, err := store.ListByInstance(ctx, instance, 5)
	if err != nil {
		return NewSessionID(instance)
	}
	now := time.Now()
	for _, s := range sessions {
		if s == nil || s.Status != SessionActive {
			continue
		}
		if s.UpdatedAt.IsZero() || now.Sub(s.UpdatedAt) > ResumeMaxAge {
			continue
		}
		return s.ID
	}
	return NewSessionID(instance)
}
