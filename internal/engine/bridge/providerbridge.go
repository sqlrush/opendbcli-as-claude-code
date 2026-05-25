/*-------------------------------------------------------------------------
 *
 * providerbridge.go
 *	  NewAdapterFromManager creates a provider.ProviderAdapter from a
 *	  model.Manager. Reads the active model profile and creates the
 *	  appropriate adapter.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/bridge/providerbridge.go
 *
 *-------------------------------------------------------------------------
 */
package bridge

import (
	"github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/model"
)

// NewAdapterFromManager creates a provider.ProviderAdapter from a model.Manager.
// Reads the active model profile and creates the appropriate adapter.
func NewAdapterFromManager(mgr *model.Manager) (provider.ProviderAdapter, error) {
	active := mgr.Active()
	if active == nil {
		return nil, nil // No LLM configured
	}

	cfg := provider.AdapterConfig{
		Provider: active.Provider,
		Vendor:   active.Vendor,
		BaseURL:  active.BaseURL,
		Model:    active.Model,
		APIKey:   model.ExpandEnvVars(active.APIKey),
	}

	return provider.NewAdapter(cfg)
}
