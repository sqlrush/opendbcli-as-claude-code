/*-------------------------------------------------------------------------
 *
 * main.go
 *	  skilltest: headless skill tester — runs each skill and reports
 *	  pass/fail. Usage: skilltest [connName] (defaults to "oratest")
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/skilltest/main.go
 *
 *-------------------------------------------------------------------------
 */
// skilltest: headless skill tester — runs each skill and reports pass/fail.
// Usage: skilltest [connName]  (defaults to "oratest")
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	oracledriver "github.com/sqlrush/opendb/internal/oracle/driver"
	"github.com/sqlrush/opendb/internal/oracle/agent"
	"github.com/sqlrush/opendb/internal/llm/ollama"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
	admin "github.com/sqlrush/opendb/internal/skill/builtin/admin"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
	monitor "github.com/sqlrush/opendb/internal/oracle/skill/monitor"
	query "github.com/sqlrush/opendb/internal/oracle/skill/query"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	connMgr, err := connection.NewManager(cfg,
		connection.WithDriverFactory(func(cfg db.ConnectionConfig) (db.Driver, error) {
			return oracledriver.NewDriver(cfg)
		}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connmgr: %v\n", err)
		os.Exit(1)
	}

	connName := "oratest"
	if len(os.Args) > 1 {
		connName = os.Args[1]
	}
	ctx := context.Background()
	if err := connMgr.Connect(ctx, connName); err != nil {
		fmt.Fprintf(os.Stderr, "connect %s: %v\n", connName, err)
		os.Exit(1)
	}
	fmt.Printf("Connected to %s\n\n", connName)

	driver := connection.NewLazyDriver(connMgr)
	sessionStore := session.NewStore(cfg.Session.HistoryDir)
	sessionHistory := sessionStore.GetOrCreate("_default")
	_ = sessionHistory

	registry := skill.NewRegistry()

	// Register all skills (same as cmd/opendb/main.go).
	registry.Register(shared.NewHelpSkill(registry))
	registry.Register(admin.NewAlterSkill(driver))
	registry.Register(admin.NewResizeSkill(driver))
	registry.Register(admin.NewJobsSkill(driver))
	registry.Register(admin.NewSpaceSkill(driver))
	registry.Register(admin.NewKillSkill(driver))

	registry.Register(query.NewSQLSkill(driver))
	registry.Register(query.NewTopSQLSkill(driver))
	registry.Register(query.NewSlowSQLSkill(driver))
	registry.Register(query.NewExplainSkill(driver))

	registry.Register(monitor.NewTempSessSkill(driver))
	registry.Register(monitor.NewUndoSessSkill(driver))
	registry.Register(monitor.NewPGASkill(driver))
	registry.Register(monitor.NewSGASkill(driver))
	registry.Register(monitor.NewRedoSkill(driver))
	registry.Register(monitor.NewFRASkill(driver))
	registry.Register(monitor.NewASMSkill(driver))
	registry.Register(monitor.NewSortUsageSkill(driver))
	registry.Register(monitor.NewResourceSkill(driver))
	registry.Register(monitor.NewHealthSkill(driver))
	registry.Register(monitor.NewBlockTreeSkill(driver))
	registry.Register(monitor.NewActiveSessionsSkill(driver))
	registry.Register(monitor.NewWaitsSkill(driver))
	registry.Register(monitor.NewLocksSkill(driver))

	guard := security.NewGuard(security.LevelDangerous, func(action string) (bool, error) {
		return true, nil // auto-confirm in test mode
	})
	executor := skill.NewExecutor(registry, guard)

	type testCase struct {
		name   string
		skill  string
		args   string
		expect string
	}

	tests := []testCase{
		// P0
		{"P0: /tempsess", "tempsess", "", ""},
		{"P0: /undosess", "undosess", "", ""},
		// P1
		{"P1: /alter open_cursors", "alter", "open_cursors", ""},
		{"P1: /resize TEMP", "resize", "TEMP", ""},
		{"P1: /pga", "pga", "", ""},
		{"P1: /sga", "sga", "", ""},
		// P2
		{"P2: /redo", "redo", "", ""},
		{"P2: /fra", "fra", "", ""},
		{"P2: /asm", "asm", "", ""},
		{"P2: /topsql", "topsql", "", ""},
		{"P2: /sortusage", "sortusage", "", ""},
		// P3
		{"P3: /jobs", "jobs", "", ""},
		{"P3: /resource", "resource", "", ""},
		// Extra
		{"Extra: /health", "health", "", ""},
		{"Extra: /space", "space", "", ""},
		{"Extra: /blocktree", "blocktree", "", ""},
		{"Extra: /activesessions", "activesessions", "", ""},
		{"Extra: /waits", "waits", "", ""},
		{"Extra: /locks", "locks", "", ""},
		{"Extra: SQL", "sql", "", "2"}, // uses "sql" param key below
		{"Extra: /help", "help", "", ""},
	}

	pass, fail := 0, 0
	for _, tc := range tests {
		paramMap := map[string]any{"args": tc.args}
		if tc.skill == "sql" {
			paramMap = map[string]any{"sql": "select 1+1 as result from dual"}
		}
		params := skill.ParamsFromMap(paramMap)
		start := time.Now()
		result, err := executor.Execute(ctx, tc.skill, params)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("FAIL  %-30s  %v  (%s)\n", tc.name, err, elapsed.Round(time.Millisecond))
			fail++
			continue
		}

		output := extractOutput(result)

		if tc.expect != "" && !strings.Contains(strings.ToLower(output), strings.ToLower(tc.expect)) {
			fmt.Printf("FAIL  %-30s  missing '%s' in output (%d chars)  (%s)\n",
				tc.name, tc.expect, len(output), elapsed.Round(time.Millisecond))
			fail++
			continue
		}

		if output == "" && result != nil {
			output = fmt.Sprintf("[type=%d, summary=%s]", result.Type, result.Summary)
		}

		preview := strings.ReplaceAll(output, "\n", " ")
		if len(preview) > 80 {
			preview = preview[:77] + "..."
		}
		fmt.Printf("PASS  %-30s  (%s)  %s\n", tc.name, elapsed.Round(time.Millisecond), preview)
		pass++
	}

	fmt.Printf("\n=== %d PASS / %d FAIL / %d total ===\n", pass, fail, pass+fail)

	// ── LLM Diagnosis Test ──
	if cfg.LLM.Provider != "" && cfg.LLM.Provider != "none" {
		fmt.Println("\n── LLM Diagnosis Tests ──")
		provider := ollama.NewOllamaProvider(cfg.LLM.BaseURL, cfg.LLM.Model)
		diagnoser := agent.NewDiagnoser(provider, executor, registry)

		// Test streaming callback
		var streamChunks int64
		diagnoser.SetOnStream(func(delta string) {
			atomic.AddInt64(&streamChunks, 1)
		})
		diagnoser.SetOnRound(func(info agent.RoundInfo) {
			fmt.Printf("  ● Round %d: %s\n", info.Round, info.Summary)
		})

		llmTests := []struct {
			name  string
			mode  agent.DiagnoseMode
			input string
		}{
			{"LLM: playbook mode", agent.ModePlaybook, "当前数据库是否存在性能问题"},
			{"LLM: assist mode (3 rounds)", agent.ModeAssist, "分析当前数据库性能"},
		}

		for _, lt := range llmTests {
			atomic.StoreInt64(&streamChunks, 0)
			start := time.Now()
			result, err := diagnoser.Diagnose(ctx, lt.mode, nil, lt.input)
			elapsed := time.Since(start)

			if err != nil {
				fmt.Printf("FAIL  %-35s  %v  (%s)\n", lt.name, err, elapsed.Round(time.Millisecond))
				fail++
				continue
			}

			chunks := atomic.LoadInt64(&streamChunks)
			preview := strings.ReplaceAll(result.Analysis, "\n", " ")
			if len(preview) > 60 {
				preview = preview[:57] + "..."
			}
			streamInfo := fmt.Sprintf("chunks=%d", chunks)
			if chunks > 0 {
				streamInfo = fmt.Sprintf("streamed=%d chunks", chunks)
			}

			fmt.Printf("PASS  %-35s  (%s, %d rounds, %s)  %s\n",
				lt.name, elapsed.Round(time.Millisecond), result.RoundsUsed, streamInfo, preview)
			pass++
		}
		fmt.Printf("\n=== %d PASS / %d FAIL / %d total (with LLM) ===\n", pass, fail, pass+fail)
	} else {
		fmt.Println("\n(LLM not configured, skipping diagnosis tests)")
	}

	if fail > 0 {
		os.Exit(1)
	}
}

func extractOutput(result *skill.Result) string {
	if result == nil {
		return ""
	}
	if result.Rendered != "" {
		return result.Rendered
	}
	if s, ok := result.Data.(string); ok {
		return s
	}
	if qr, ok := result.Data.(*db.QueryResult); ok {
		if len(qr.Rows) > 0 && len(qr.Rows[0]) > 0 {
			return fmt.Sprintf("[table: %d cols, %d rows] first=%v", len(qr.Columns), len(qr.Rows), qr.Rows[0][0])
		}
		return fmt.Sprintf("[table: %d cols, %d rows]", len(qr.Columns), len(qr.Rows))
	}
	return fmt.Sprintf("%v", result.Data)
}
