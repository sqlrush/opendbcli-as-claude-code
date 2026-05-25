//go:build dm || full

/*-------------------------------------------------------------------------
 *
 * product_dm.go
 *	  DM product registration — compiled in only with -tags dm or
 *	  -tags full. Wires the Dameng db.Driver and dm.RegisterAISkills
 *	  into the shared product registry, so the resulting binary speaks
 *	  DM without dragging the driver into builds that don't need it.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/product_dm.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/dm"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui"
)

func init() {
	registerProduct(product{
		name:           dm.DBType,
		driverFactory:  dm.DriverFactory,
		registerSkills: dm.RegisterSkills,
		registerAISkills: func(
			registry *skill.Registry,
			driver db.Driver,
			executor *skill.Executor,
			cfg *config.Config,
			modelMgr *model.Manager,
		) (ui.SentinelAlertSource, ui.DiagAsyncSource) {
			return dm.RegisterAISkills(registry, driver, executor, cfg, modelMgr)
		},
	})
}
