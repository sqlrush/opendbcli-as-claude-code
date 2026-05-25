//go:build gaussdb || full

/*-------------------------------------------------------------------------
 *
 * product_gaussdb.go
 *	  GaussDB product registration — built only with -tags gaussdb or
 *	  -tags full. Uses Huawei's gaussdb-go driver (SCRAM-SHA256 over
 *	  the commercial GaussDB protocol, distinct from openGauss) and
 *	  shares the OG skill set for now.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/product_gaussdb.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/gaussdb"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui"
)

func init() {
	registerProduct(product{
		name:           gaussdb.DBType,
		driverFactory:  gaussdb.DriverFactory,
		registerSkills: gaussdb.RegisterSkills,
		registerAISkills: func(
			registry *skill.Registry,
			driver db.Driver,
			executor *skill.Executor,
			cfg *config.Config,
			modelMgr *model.Manager,
		) (ui.SentinelAlertSource, ui.DiagAsyncSource) {
			sentinel, diag := gaussdb.RegisterAISkills(registry, driver, executor, cfg, modelMgr)
			return sentinel, diag
		},
	})
}
