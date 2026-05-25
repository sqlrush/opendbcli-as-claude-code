/*-------------------------------------------------------------------------
 *
 * types.go
 *	  SessionID uniquely identifies a conversation session. Format:
 *	  {instance}:{uuid}
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/session/types.go
 *
 *-------------------------------------------------------------------------
 */
package session

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// SessionID uniquely identifies a conversation session.
// Format: {instance}:{uuid}
type SessionID string

// NewSessionID generates a new session ID for the given instance.
func NewSessionID(instance string) SessionID {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	return SessionID(fmt.Sprintf("%s:%s", instance, id))
}

// Instance extracts the instance name from the session ID.
func (id SessionID) Instance() string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == ':' {
			return string(id[:i])
		}
	}
	return string(id)
}

// String returns the string representation.
func (id SessionID) String() string {
	return string(id)
}

// SessionStatus represents the lifecycle state of a session.
type SessionStatus string

const (
	SessionActive    SessionStatus = "active"
	SessionCompleted SessionStatus = "completed"
	SessionExpired   SessionStatus = "expired"
)

// Session holds the persisted state of a conversation session.
type Session struct {
	ID         SessionID          `json:"id"`
	Instance   string             `json:"instance"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Messages   []provider.Message `json:"messages"`
	TurnCount  int                `json:"turn_count"`
	TokensUsed int                `json:"tokens_used"`
	Status     SessionStatus      `json:"status"`
}

// NewSession creates a new active session for the given instance.
func NewSession(instance string) *Session {
	now := time.Now()
	return &Session{
		ID:        NewSessionID(instance),
		Instance:  instance,
		CreatedAt: now,
		UpdatedAt: now,
		Status:    SessionActive,
	}
}
