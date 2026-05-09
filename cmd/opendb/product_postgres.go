//go:build postgres || full

/*-------------------------------------------------------------------------
 *
 * product_postgres.go
 *	  PostgreSQL product registration — built only with -tags postgres
 *	  or -tags full. Wires the pgx-based driver and the postgres skill
 *	  set into the product registry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/product_postgres.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/postgres"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui"
)

func init() {
	registerProduct(product{
		name:           "postgres",
		driverFactory:  postgres.DriverFactory,
		registerSkills: postgres.RegisterSkills,
		registerAISkills: func(
			registry *skill.Registry,
			driver db.Driver,
			executor *skill.Executor,
			cfg *config.Config,
			modelMgr *model.Manager,
		) (ui.SentinelAlertSource, ui.DiagAsyncSource) {
			sentinel, diag := postgres.RegisterAISkills(registry, driver, executor, cfg, modelMgr)
			return sentinel, diag
		},
	})
}
