//go:build opengauss || full

/*-------------------------------------------------------------------------
 *
 * product_opengauss.go
 *	  openGauss product registration — built only with -tags opengauss
 *	  or -tags full. Reuses the pgx stdlib driver (OG is wire-protocol
 *	  compatible) and registers the OG-specific skill set.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/product_opengauss.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/opengauss"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui"
)

func init() {
	registerProduct(product{
		name:           "opengauss",
		driverFactory:  opengauss.DriverFactory,
		registerSkills: opengauss.RegisterSkills,
		registerAISkills: func(
			registry *skill.Registry,
			driver db.Driver,
			executor *skill.Executor,
			cfg *config.Config,
			modelMgr *model.Manager,
		) (ui.SentinelAlertSource, ui.DiagAsyncSource) {
			sentinel, diag := opengauss.RegisterAISkills(registry, driver, executor, cfg, modelMgr)
			return sentinel, diag
		},
	})
}
