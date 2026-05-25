/*-------------------------------------------------------------------------
 *
 * reconnect.go
 *	  Reconnect helpers — handles the auto-reconnect path when an
 *	  idle connection drops mid-session (network blip, server restart),
 *	  preserving the active session-level state.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/connection/reconnect.go
 *
 *-------------------------------------------------------------------------
 */
package connection

import (
	"context"
	"fmt"
)

// reconnect attempts to re-establish the current connection using the last
// known configuration. For read-only operations this can happen silently;
// for write operations the caller should confirm with the user first.
func (m *Manager) reconnect(ctx context.Context) error {
	m.mu.RLock()
	cfg := m.currentConfig
	name := m.currentName
	m.mu.RUnlock()

	if cfg == nil {
		return fmt.Errorf("no previous connection to reconnect")
	}

	driver, err := m.createDriver(*cfg)
	if err != nil {
		return fmt.Errorf("reconnecting to %q: %w", name, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Close the stale driver if it still exists.
	if m.driver != nil {
		_ = m.driver.Close()
	}

	m.driver = driver
	return nil
}

// ReconnectIfReadOnly attempts a silent reconnect when the pending
// operation is read-only. For write operations it returns an error
// indicating that user confirmation is required.
func (m *Manager) ReconnectIfReadOnly(ctx context.Context, readOnly bool) error {
	if readOnly {
		return m.reconnect(ctx)
	}
	return fmt.Errorf("connection lost: reconnect requires user confirmation for write operations")
}
