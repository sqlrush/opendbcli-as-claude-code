/*-------------------------------------------------------------------------
 *
 * products.go
 *	  Product registry plumbing — defines the `product` struct that
 *	  bundles per-DB driver factory, AI skill registrar, connector,
 *	  and CLI handlers, then collects registered products at init time.
 *
 *	  Each per-DB sibling file (product_oracle.go, product_dm.go, etc.)
 *	  registers itself behind its own build tag so unused drivers stay
 *	  out of the binary.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/products.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui"
)

// product defines the registration hooks for a database product (Oracle/MySQL/PG).
type product struct {
	name           string
	driverFactory  connection.DriverFactory
	registerSkills func(
		registry *skill.Registry,
		driver db.Driver,
		connMgr *connection.Manager,
		history *session.SessionHistory,
		cfg *config.Config,
		modelMgr *model.Manager,
		opendbDir string,
	)
	// registerAISkills returns REPL-compatible interfaces (nil-safe).
	registerAISkills func(
		registry *skill.Registry,
		driver db.Driver,
		executor *skill.Executor,
		cfg *config.Config,
		modelMgr *model.Manager,
	) (ui.SentinelAlertSource, ui.DiagAsyncSource)
}

// products holds all registered database products (populated by init() in build-tagged files).
var products []product

// registerProduct adds a database product to the registry.
func registerProduct(p product) {
	products = append(products, p)
}
