//go:build mysql || full

/*-------------------------------------------------------------------------
 *
 * product_mysql.go
 *	  MySQL product registration — built only with -tags mysql or
 *	  -tags full. Wires the go-sql-driver/mysql adapter and MySQL
 *	  skill set into the product registry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/product_mysql.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/mysql"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui"
)

func init() {
	registerProduct(product{
		name:           "mysql",
		driverFactory:  mysql.DriverFactory,
		registerSkills: mysql.RegisterSkills,
		registerAISkills: func(
			registry *skill.Registry,
			driver db.Driver,
			executor *skill.Executor,
			cfg *config.Config,
			modelMgr *model.Manager,
		) (ui.SentinelAlertSource, ui.DiagAsyncSource) {
			sentinel, diag := mysql.RegisterAISkills(registry, driver, executor, cfg, modelMgr)
			return sentinel, diag
		},
	})
}
