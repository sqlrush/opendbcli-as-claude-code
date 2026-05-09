//go:build oracle || full || (!mysql && !postgres && !opengauss)

/*-------------------------------------------------------------------------
 *
 * product_oracle.go
 *	  Oracle product registration — default product when no DB tag is
 *	  set, also included by -tags oracle or -tags full. Wires the
 *	  go-ora driver and Oracle skill set into the product registry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/product_oracle.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/oracle"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui"
)

func init() {
	registerProduct(product{
		name:           "oracle",
		driverFactory:  oracle.DriverFactory,
		registerSkills: oracle.RegisterSkills,
		registerAISkills: func(
			registry *skill.Registry,
			driver db.Driver,
			executor *skill.Executor,
			cfg *config.Config,
			modelMgr *model.Manager,
		) (ui.SentinelAlertSource, ui.DiagAsyncSource) {
			sentinel, diag := oracle.RegisterAISkills(registry, driver, executor, cfg, modelMgr)
			return sentinel, diag
		},
	})
}
