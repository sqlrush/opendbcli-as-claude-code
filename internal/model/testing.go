/*-------------------------------------------------------------------------
 *
 * testing.go
 *	  NewManagerForTest creates a Manager with a pre-built provider for
 *	  testing. If provider is nil, the manager has no active model
 *	  (rule-only mode).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/model/testing.go
 *
 *-------------------------------------------------------------------------
 */
package model

import (
	"github.com/sqlrush/opendb/internal/llm"
)

// NewManagerForTest creates a Manager with a pre-built provider for testing.
// If provider is nil, the manager has no active model (rule-only mode).
func NewManagerForTest(provider llm.Provider, capability string) *Manager {
	m := &Manager{
		profiles: make(map[string]ModelProfile),
	}
	if provider != nil {
		profile := ModelProfile{
			Name:       "test",
			Provider:   "mock",
			Capability: capability,
		}
		m.profiles["test"] = profile
		m.active = &profile
		m.provider = provider
	}
	return m
}
