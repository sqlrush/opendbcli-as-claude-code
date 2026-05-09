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
 *	  cmd/e2etest/main.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	oracledriver "github.com/sqlrush/opendb/internal/oracle/driver"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/builtin/admin"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
	"github.com/sqlrush/opendb/internal/oracle/skill/monitor"
	"github.com/sqlrush/opendb/internal/oracle/skill/query"
	"github.com/sqlrush/opendb/internal/oracle/skill/schema"
)

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintf(os.Stderr, "Usage: e2etest <host> <port> <service> <user> <password>\n")
		os.Exit(1)
	}

	host := os.Args[1]
	port := os.Args[2]
	service := os.Args[3]
	user := os.Args[4]
	password := os.Args[5]

	portNum := 1521
	fmt.Sscanf(port, "%d", &portNum)

	fmt.Println("=== OpenDB E2E Integration Test ===")
	fmt.Printf("Target: %s@%s:%d/%s\n\n", user, host, portNum, service)

	// 1. Test Oracle connection
	fmt.Print("[Test 1] Oracle Connection... ")
	connCfg := db.ConnectionConfig{
		DBType:   "oracle",
		Host:     host,
		Port:     portNum,
		Service:  service,
		User:     user,
		Password: password,
	}
	driver, err := oracledriver.NewDriver(connCfg)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	defer driver.Close()
	info := driver.ServerInfo()
	fmt.Printf("OK (%s, %s)\n", info.InstanceName, info.Version)

	// 2. Test connection manager with /login
	fmt.Print("[Test 2] Connection Manager... ")
	cfg := config.Default()
	cfg.ConnectionsDir = "/tmp/opendb-e2e-test-conns"
	os.MkdirAll(cfg.ConnectionsDir, 0755)
	writeTestConnYAML(cfg.ConnectionsDir, host, portNum, service, user)
	connMgr, err := connection.NewManager(&cfg,
		connection.WithDriverFactory(func(c db.ConnectionConfig) (db.Driver, error) {
			return oracledriver.NewDriver(c)
		}),
	)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	err = connMgr.ConnectWithPassword(context.Background(), "e2e-test", password)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("OK")

	// 3. Setup registry with all skills
	lazyDriver := connection.NewLazyDriver(connMgr)
	registry := skill.NewRegistry()
	sessionHistory := session.NewSessionHistory("/tmp/opendb-e2e-history", "e2e-test")

	// Register all skills
	registry.Register(shared.NewHelpSkill(registry))
	registry.Register(shared.NewClearSkill())
	registry.Register(shared.NewHistorySkill(sessionHistory))
	registry.Register(shared.NewConfigSkill(&cfg))
	registry.Register(shared.NewLoginSkill(connMgr))
	registry.Register(shared.NewLogoutSkill(connMgr))
	registry.Register(admin.NewKillSkill(lazyDriver))
	registry.Register(admin.NewSpaceSkill(lazyDriver))
	registry.Register(admin.NewParamsSkill(lazyDriver))
	registry.Register(admin.NewAlertSkill(lazyDriver))
	registry.Register(admin.NewBackupSkill(lazyDriver))
	registry.Register(admin.NewStandbySkill(lazyDriver))
	registry.Register(query.NewSlowSQLSkill(lazyDriver))
	registry.Register(query.NewExplainSkill(lazyDriver))
	registry.Register(query.NewSQLSkill(lazyDriver))
	registry.Register(monitor.NewSessionsSkill(lazyDriver))
	registry.Register(monitor.NewActiveSessionsSkill(lazyDriver))
	registry.Register(monitor.NewWaitsSkill(lazyDriver))
	registry.Register(monitor.NewLocksSkill(lazyDriver))
	registry.Register(monitor.NewLatchesSkill(lazyDriver))
	registry.Register(monitor.NewMutexesSkill(lazyDriver))
	registry.Register(monitor.NewHealthSkill(lazyDriver))
	registry.Register(monitor.NewDBTopSkill(lazyDriver))
	registry.Register(schema.NewTableInfoSkill(lazyDriver))
	registry.Register(schema.NewIndexAdviseSkill(lazyDriver))

	guard := security.NewGuard(security.LevelDangerous, nil)
	executor := skill.NewExecutor(registry, guard)

	ctx := context.Background()

	// 4. Test each skill
	tests := []struct {
		name   string
		skill  string
		params map[string]any
	}{
		{"help", "help", nil},
		{"clear", "clear", nil},
		{"history", "history", nil},
		{"config", "config", nil},
		{"sessions", "sessions", nil},
		{"activesessions", "activesessions", nil},
		{"waits", "waits", nil},
		{"locks", "locks", nil},
		{"latches", "latches", nil},
		{"mutexes", "mutexes", nil},
		{"slowsql (default)", "slowsql", nil},
		{"slowsql (threshold)", "slowsql", map[string]any{"args": "100"}},
		{"space", "space", nil},
		{"params (all)", "params", nil},
		{"params (sga)", "params", map[string]any{"args": "sga"}},
		{"alert", "alert", nil},
		{"backup", "backup", nil},
		{"standby", "standby", nil},
		{"health", "health", nil},
		{"dbtop", "dbtop", nil},
		{"tableinfo", "tableinfo", map[string]any{"args": "TEST_ORDERS"}},
		{"Direct SQL", "sql", map[string]any{"sql": "SELECT * FROM test_orders ORDER BY order_id"}},
	}

	passed := 0
	failed := 0
	for i, tt := range tests {
		fmt.Printf("[Test %d] /%s... ", i+3, tt.name)
		var params skill.Params
		if tt.params != nil {
			params = skill.ParamsFromMap(tt.params)
		} else {
			params = skill.ParamsFromMap(map[string]any{})
		}

		result, err := executor.Execute(ctx, tt.skill, params)
		if err != nil {
			fmt.Printf("FAIL: %v\n", err)
			failed++
			continue
		}

		// Display result summary
		switch result.Type {
		case skill.ResultTable:
			if qr, ok := result.Data.(*db.QueryResult); ok {
				table := format.FormatTableWithLimit(qr, 5)
				lines := strings.Split(table, "\n")
				if len(lines) > 3 {
					fmt.Printf("OK (%d rows)\n", len(qr.Rows))
					// Print first few lines of table
					for _, line := range lines[:min(8, len(lines))] {
						fmt.Printf("    %s\n", line)
					}
				} else {
					fmt.Printf("OK (empty result)\n")
				}
			} else {
				fmt.Printf("OK (table data)\n")
			}
		case skill.ResultText:
			text, _ := result.Data.(string)
			lines := strings.Split(text, "\n")
			preview := lines[0]
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			fmt.Printf("OK: %s\n", preview)
		case skill.ResultRefresh:
			text, _ := result.Data.(string)
			lines := strings.Split(text, "\n")
			fmt.Printf("OK (refresh mode, %d lines)\n", len(lines))
			for _, line := range lines[:min(5, len(lines))] {
				fmt.Printf("    %s\n", line)
			}
		default:
			fmt.Printf("OK (type=%d)\n", result.Type)
		}
		passed++
	}

	// 5. Test explain
	fmt.Printf("[Test %d] /explain... ", len(tests)+3)
	explainParams := skill.ParamsFromMap(map[string]any{
		"args": "SELECT * FROM test_orders WHERE customer_name = 'Alice'",
	})
	result, err := executor.Execute(ctx, "explain", explainParams)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		failed++
	} else {
		text, _ := result.Data.(string)
		lines := strings.Split(text, "\n")
		fmt.Printf("OK (%d lines)\n", len(lines))
		for _, line := range lines[:min(5, len(lines))] {
			fmt.Printf("    %s\n", line)
		}
		passed++
	}

	// 6. Test /logout
	fmt.Printf("[Test %d] /logout... ", len(tests)+4)
	logoutResult, err := executor.Execute(ctx, "logout", skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		failed++
	} else {
		text, _ := logoutResult.Data.(string)
		fmt.Printf("OK: %s\n", text)
		passed++
	}

	// Summary
	fmt.Printf("\n=== Results: %d passed, %d failed ===\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}

	// Cleanup
	os.RemoveAll(cfg.ConnectionsDir)
	os.RemoveAll("/tmp/opendb-e2e-history")
}

func writeTestConnYAML(dir, host string, port int, service, user string) {
	yaml := fmt.Sprintf(`group: e2e
tags: [test]
connections:
  - name: e2e-test
    host: %s
    port: %d
    service: %s
    user: %s
    credential:
      provider: prompt
`, host, port, service, user)
	os.WriteFile(dir+"/e2e.yaml", []byte(yaml), 0644)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Ensure time import is used
var _ = time.Now
