/*-------------------------------------------------------------------------
 *
 * main.go
 *	  Entry point for the main binary.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/viewtest/main.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	oracledriver "github.com/sqlrush/opendb/internal/oracle/driver"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/skill"
	admin "github.com/sqlrush/opendb/internal/skill/builtin/admin"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
	monitor "github.com/sqlrush/opendb/internal/oracle/skill/monitor"
)

func main() {
	cfg, _ := config.Load()
	connMgr, _ := connection.NewManager(cfg,
		connection.WithDriverFactory(func(cfg db.ConnectionConfig) (db.Driver, error) {
			return oracledriver.NewDriver(cfg)
		}),
	)
	connMgr.Connect(context.Background(), "oratest")
	driver := connection.NewLazyDriver(connMgr)

	registry := skill.NewRegistry()
	registry.Register(shared.NewHelpSkill(registry))
	registry.Register(admin.NewSpaceSkill(driver))
	registry.Register(monitor.NewHealthSkill(driver))
	registry.Register(monitor.NewWaitsSkill(driver))
	registry.Register(monitor.NewActiveSessionsSkill(driver))

	guard := security.NewGuard(security.LevelDangerous, func(action string) (bool, error) {
		return true, nil
	})
	executor := skill.NewExecutor(registry, guard)
	ctx := context.Background()

	skills := []string{"health", "space", "waits", "activesessions"}
	for _, name := range skills {
		fmt.Printf("\n===== /%s =====\n", name)
		result, err := executor.Execute(ctx, name, skill.ParamsFromMap(nil))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			continue
		}
		if result.Rendered != "" {
			fmt.Print(result.Rendered)
		} else if s, ok := result.Data.(string); ok {
			fmt.Print(s)
		} else {
			fmt.Printf("[type=%d]\n", result.Type)
		}
	}
}
