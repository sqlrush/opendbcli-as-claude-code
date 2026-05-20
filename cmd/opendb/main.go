/*-------------------------------------------------------------------------
 *
 * main.go
 *	  opendb binary entry — parses CLI flags (--version / --help /
 *	  -c batch mode / configure / setup / cluster / agent subcommands),
 *	  loads brand-aware config, then dispatches to either the
 *	  interactive REPL (ui.Repl) or single-shot batch execution.
 *
 *	  cluster / agent subcommands fork into the cerebrate / drone /
 *	  overlord startup paths under internal/cluster and internal/drone.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/main.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/sqlrush/opendb/internal/brand"
	"github.com/sqlrush/opendb/internal/cerebrate"
	"github.com/sqlrush/opendb/internal/cluster"
	"github.com/sqlrush/opendb/internal/drone"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/dispatch"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/odberr"
	"github.com/sqlrush/opendb/internal/overlord"
	"github.com/sqlrush/opendb/internal/scheduler"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/setup"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
	"github.com/sqlrush/opendb/internal/ui"
	"github.com/sqlrush/opendb/internal/version"
)

func main() {
	defer odberr.RecoverFatal(odberr.ErrCoreMainPanic)

	// pprof: enable via DBAA_PPROF=:6060 env var (DEBUG ONLY)
	if addr := os.Getenv("DBAA_PPROF"); addr != "" {
		go func() {
			fmt.Fprintf(os.Stderr, "[pprof] listening on %s\n", addr)
			_ = pprofServe(addr)
		}()
	}

	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version.String())
		os.Exit(0)
	}

	// Setup wizard: opendb setup (also supports --setup for backward compat)
	if len(os.Args) > 1 && (os.Args[1] == "setup" || os.Args[1] == "--setup") {
		if err := setup.RunSetup(defaultOpenDBDir(), buildDriverFactory()); err != nil {
			fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Configure: opendb configure
	if len(os.Args) > 1 && os.Args[1] == "configure" {
		if err := setup.RunConfigure(defaultOpenDBDir(), buildDriverFactory()); err != nil {
			fmt.Fprintf(os.Stderr, "Configure error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Cluster management: opendb cluster <init|join|status> [flags]
	if len(os.Args) > 1 && os.Args[1] == "cluster" {
		clusterArgs, err := cluster.ParseClusterArgs(os.Args[2:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := cluster.RunCluster(clusterArgs); err != nil {
			fmt.Fprintf(os.Stderr, "Cluster error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Agent mode: opendb agent <start|stop|status> [flags]
	if len(os.Args) > 1 && os.Args[1] == "agent" {
		agentArgs, err := drone.ParseAgentArgs(os.Args[2:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// start uses full product wiring; stop/status use lightweight PID-only logic.
		var agentErr error
		if agentArgs.Subcmd == drone.SubcmdStart {
			agentErr = runAgentMode(agentArgs)
		} else {
			agentErr = drone.RunAgent(agentArgs)
		}
		if agentErr != nil {
			fmt.Fprintf(os.Stderr, "Agent error: %v\n", agentErr)
			os.Exit(1)
		}
		return
	}

	// Batch mode: opendb -c <connection> <command>
	// Runs a single command non-interactively and exits.
	if len(os.Args) >= 4 && os.Args[1] == "-c" {
		// Suppress lipgloss/termenv terminal probing (OSC 11 background color
		// query + CSI 6n cursor query). Probes leak into stdout in batch mode
		// as `]11;?\[6n` prefix on every output (task 15 真机日志可见).
		// Setting NO_COLOR makes termenv skip probing entirely; LLM-facing
		// batch output doesn't need ANSI colors anyway.
		if os.Getenv("NO_COLOR") == "" {
			os.Setenv("NO_COLOR", "1")
		}
		runBatch(os.Args[2], os.Args[3:])
		return
	}

	// Terminal guardian: protect against kill -9 leaving terminal in raw mode.
	// The parent process saves terminal state, re-execs as child, and restores
	// terminal after the child exits (regardless of how it exits).
	if os.Getenv("_OPENDB_CHILD") == "" {
		runAsGuardian()
		return
	}

	// Check if first-time setup is needed.
	if err := runSetupWizardIfNeeded(); err != nil {
		fmt.Fprintf(os.Stderr, "Setup wizard error: %v\n", err)
		os.Exit(1)
	}

	// Load configuration.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Create connection manager with driver factories from registered products.
	opts := []connection.Option{}
	for _, p := range products {
		opts = append(opts, connection.WithDriverFactoryFor(p.name, p.driverFactory))
	}
	// Legacy fallback: first product's factory as default (backward compatible).
	if len(products) > 0 {
		opts = append(opts, connection.WithDriverFactory(products[0].driverFactory))
	}
	connMgr, err := connection.NewManager(cfg, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating connection manager: %v\n", err)
		os.Exit(1)
	}

	// Create lazy driver that delegates to current connection.
	driver := connection.NewLazyDriver(connMgr)

	// Create session store.
	sessionStore := session.NewStore(cfg.Session.HistoryDir)
	sessionHistory := sessionStore.GetOrCreate("_default")

	// Create model manager (multi-model support).
	modelMgr, err := model.NewManager(cfg.ModelsDir, cfg.ActiveModel, model.FallbackLLM{
		Provider:     cfg.LLM.Provider,
		BaseURL:      cfg.LLM.BaseURL,
		Model:        cfg.LLM.Model,
		Capability:   cfg.LLM.Capability,
		InlineModels: configToInlineModels(cfg),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading models: %v\n", err)
		os.Exit(1)
	}

	opendbDir := defaultOpenDBDir()

	// Create skill registry and register shared + product skills.
	registry := skill.NewRegistry()
	sharedMemStore := registerSharedSkills(registry, connMgr, sessionHistory, cfg, modelMgr)
	for _, p := range products {
		p.registerSkills(registry, driver, connMgr, sessionHistory, cfg, modelMgr, opendbDir)
	}

	// Create executor and dispatcher.
	guard := security.NewGuard(security.LevelDangerous, func(action string) (bool, error) {
		return true, nil
	})
	executor := skill.NewExecutor(registry, guard)

	// Register AI skills from each product — store per-DB type for dynamic resolution.
	sentinelMap := make(map[string]ui.SentinelAlertSource)
	diagMap := make(map[string]ui.DiagAsyncSource)
	var fallbackSentinel ui.SentinelAlertSource
	var fallbackDiag ui.DiagAsyncSource
	for _, p := range products {
		ss, ds := p.registerAISkills(registry, driver, executor, cfg, modelMgr)
		if ss != nil {
			sentinelMap[p.name] = ss
			fallbackSentinel = ss
		}
		if ds != nil {
			diagMap[p.name] = ds
			fallbackDiag = ds
		}
	}

	// Create scheduler and register skill.
	schedulerSkill := registerScheduler(registry, executor, cfg)

	dispatcher := dispatch.NewDispatcher(registry, executor)

	// Start REPL (interactive chat-style, like Claude Code).
	// Pass fallback sentinel/diag for backward compat; REPL resolves dynamically.
	repl := ui.NewREPL(dispatcher, connMgr, registry, fallbackSentinel, fallbackDiag, schedulerSkill, cfg)
	repl.SetAISkillMaps(sentinelMap, diagMap)
	repl.SetModelReloader(modelMgr)
	repl.SetActiveInstanceSync(sharedMemStore.SetActiveInstance)
	if err := repl.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// registerSharedSkills registers skills that are database-agnostic (shell layer).
//
// Returns the shared memory store so callers can sync its active instance
// with the connection at /login time. Before this return, memory_write /
// memory_recall / memory_update were TODO'd out — meaning the LLM had no
// tool to persist anything, and profile/memory systems silently did nothing.
func registerSharedSkills(
	registry *skill.Registry,
	connMgr *connection.Manager,
	history *session.SessionHistory,
	cfg *config.Config,
	modelMgr *model.Manager,
) *memory.Store {
	registry.Register(shared.NewHelpSkill(registry))
	registry.Register(shared.NewClearSkill())
	registry.Register(shared.NewHistorySkill(history))
	registry.Register(shared.NewConfigSkill(cfg))
	registry.Register(shared.NewLoginSkill(connMgr))
	registry.Register(shared.NewLogoutSkill(connMgr))
	registry.Register(shared.NewConnSkill(connMgr))
	registry.Register(shared.NewModelSkill(modelMgr))
	registry.Register(odberr.NewErrorSkill())

	// Policy skill — uses closures to get dynamic baseDir and instance name
	baseDir := defaultOpenDBDir()
	registry.Register(shared.NewPolicySkill(
		func() string { return filepath.Join(baseDir, "policies") },
		func() string {
			if connMgr != nil {
				return connMgr.CurrentName()
			}
			return ""
		},
	))

	// Memory skills. One shared Store points at ~/.opendb/memory; the
	// active instance is kept in sync with the connection — see the
	// login-completion hook in REPL / runBatch.
	memStore := memory.NewStore(filepath.Join(baseDir, "memory"))
	registry.Register(shared.NewMemoryWriteSkill(memStore))
	registry.Register(shared.NewMemoryRecallSkill(memStore))
	registry.Register(shared.NewMemoryUpdateSkill(memStore))
	registry.Register(shared.NewProfileSkill(memStore))
	// /session new — manual override for the 24h session resume semantics
	// added in v1.1.08; use when switching topics within the same instance
	// to avoid prior-topic context contamination.
	registry.Register(shared.NewSessionSkill(filepath.Join(baseDir, "sessions"), connMgr))
	return memStore
}

// registerScheduler creates the scheduler engine and registers the /scheduler skill.
func registerScheduler(
	registry *skill.Registry,
	executor *skill.Executor,
	cfg *config.Config,
) *shared.SchedulerSkill {
	dataDir := filepath.Join(defaultOpenDBDir(), "scheduler")
	sched := scheduler.New(executor, dataDir)

	// Convert config task defs to scheduler task configs.
	var taskConfigs []scheduler.TaskConfig
	for _, t := range cfg.Scheduler.Tasks {
		taskConfigs = append(taskConfigs, scheduler.TaskConfig{
			Name:     t.Name,
			Schedule: t.Schedule,
			Skill:    t.Skill,
			Params:   t.Params,
			OnWarn:   t.OnWarn,
			Enabled:  t.Enabled,
		})
	}

	schedulerSkill := shared.NewSchedulerSkill(sched, taskConfigs)
	registry.Register(schedulerSkill)

	// TODO(Phase 5): Register memory skills when memoryStore is wired in.
	// memoryStore := memory.NewStore(filepath.Join(baseDir, "memory"))
	// registry.Register(shared.NewMemoryWriteSkill(memoryStore))
	// registry.Register(shared.NewMemoryRecallSkill(memoryStore))
	// registry.Register(shared.NewMemoryUpdateSkill(memoryStore))

	return schedulerSkill
}

// defaultOpenDBDir returns the OpenDB base directory.
// defaultOpenDBDir delegates to config.DefaultOpenDBDir() — the single
// brand-aware resolver. Kept as a thin wrapper for callers in this file
// so the import surface stays small.
func defaultOpenDBDir() string {
	return config.DefaultOpenDBDir()
}

// runSetupWizardIfNeeded checks whether the config file and connections
// directory exist. If neither is found, it runs the first-time setup wizard.
func runSetupWizardIfNeeded() error {
	baseDir := defaultOpenDBDir()
	configPath := filepath.Join(baseDir, "config.yaml")

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		return nil
	}

	connectionsDir := filepath.Join(baseDir, "connections")
	if _, err := os.Stat(connectionsDir); !os.IsNotExist(err) {
		return nil
	}

	return setup.RunSetup(baseDir, buildDriverFactory())
}

// configToInlineModels converts config.Models to model.InlineModel slice.
func configToInlineModels(cfg *config.Config) []model.InlineModel {
	if len(cfg.Models) == 0 {
		return nil
	}
	result := make([]model.InlineModel, len(cfg.Models))
	for i, m := range cfg.Models {
		result[i] = model.InlineModel{
			Name:        m.Name,
			Provider:    m.Provider,
			Vendor:      m.Vendor,
			BaseURL:     m.BaseURL,
			Model:       m.Model,
			Capability:  m.Capability,
			APIKey:      m.APIKey,
			StripThink:  m.StripThink,
			Description: m.Description,
			ToolMode:    m.ToolMode,
		}
	}
	return result
}

// buildDriverFactory creates a DriverFactory that uses registered products.
func buildDriverFactory() setup.DriverFactory {
	return func(cfg *setup.SetupConfig) (db.Driver, error) {
		connCfg := db.ConnectionConfig{
			DBType:   cfg.DBType,
			Host:     cfg.Connection.Host,
			Port:     cfg.Connection.Port,
			Service:  cfg.Connection.Service,
			Database: cfg.Connection.Database,
			User:     cfg.Connection.User,
			Password: cfg.Password,
		}
		for _, p := range products {
			if p.name == cfg.DBType {
				return p.driverFactory(connCfg)
			}
		}
		return nil, fmt.Errorf("unsupported database type: %s", cfg.DBType)
	}
}

// batchCtxInitializer mirrors the REPL's contextStoreInitializer interface
// so batch mode can also wire up session / memory / policy stores on the
// active DiagnoseSkill. Without this, batch `/llm` runs lost its context
// stores — see docs/PROJECT-OPENGAUSS-OPTIMIZATION.md §6 上下文管理.
type batchCtxInitializer interface {
	SetContextStores(baseDir, instance string)
}

// batchProgressSetter mirrors the SetOnProgress signature shared by all
// per-DB DiagnoseSkill types. Used by batch mode to surface "第N轮: 调用 ..."
// progress to stderr — without this, /llm in batch mode is a 5+ minute black
// box (REPL has its own renderer in repl_async.go).
type batchProgressSetter interface {
	SetOnProgress(fn func(phase, message string, elapsed time.Duration, result *skill.Result, err error))
}

// runBatch executes commands non-interactively and prints results to stdout.
// Usage: opendb -c <connection_name> <command> [args...]
func runBatch(connName string, cmdArgs []string) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	batchOpts := []connection.Option{}
	for _, p := range products {
		batchOpts = append(batchOpts, connection.WithDriverFactoryFor(p.name, p.driverFactory))
	}
	if len(products) > 0 {
		batchOpts = append(batchOpts, connection.WithDriverFactory(products[0].driverFactory))
	}
	connMgr, err := connection.NewManager(cfg, batchOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating connection manager: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := connMgr.Connect(ctx, connName); err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to %q: %v\n", connName, err)
		os.Exit(1)
	}

	driver := connection.NewLazyDriver(connMgr)
	sessionStore := session.NewStore(cfg.Session.HistoryDir)
	sessionHistory := sessionStore.GetOrCreate("_default")

	batchModelMgr, err := model.NewManager(cfg.ModelsDir, cfg.ActiveModel, model.FallbackLLM{
		Provider:     cfg.LLM.Provider,
		BaseURL:      cfg.LLM.BaseURL,
		Model:        cfg.LLM.Model,
		Capability:   cfg.LLM.Capability,
		InlineModels: configToInlineModels(cfg),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading models: %v\n", err)
		os.Exit(1)
	}

	opendbDir := defaultOpenDBDir()

	registry := skill.NewRegistry()
	batchMemStore := registerSharedSkills(registry, connMgr, sessionHistory, cfg, batchModelMgr)
	// Batch mode: sync shared memory store's active instance now that we've
	// connected — so memory_write / memory_recall hit the right directory.
	batchMemStore.SetActiveInstance(connName)
	for _, p := range products {
		p.registerSkills(registry, driver, connMgr, sessionHistory, cfg, batchModelMgr, opendbDir)
	}

	// Route skill commands to the connected database type.
	if dbType := connMgr.CurrentDBType(); dbType != "" {
		registry.SetActiveDB(dbType)
	}

	guard := security.NewGuard(security.LevelDangerous, func(_ string) (bool, error) {
		return true, nil
	})
	executor := skill.NewExecutor(registry, guard)
	// Batch mode used to drop the DiagnoseSkill return values, which meant
	// SetContextStores was never called and /llm ran without session /
	// memory / policy stores wired up — the very bug v0.9.42 flagged. Keep
	// the DiagnoseSkill around and initialize its context stores now that
	// we know which instance is connected.
	batchDiagSkills := make(map[string]batchCtxInitializer)
	for _, p := range products {
		_, ds := p.registerAISkills(registry, driver, executor, cfg, batchModelMgr)
		if ds != nil {
			if init, ok := ds.(batchCtxInitializer); ok {
				batchDiagSkills[p.name] = init
			}
		}
	}
	if init, ok := batchDiagSkills[connMgr.CurrentDBType()]; ok {
		init.SetContextStores(opendbDir, connName)
		// Wire progress → stderr so batch /llm shows per-round activity
		// instead of a 5+ minute black screen. Stdout stays reserved for
		// result.Rendered so `opendb -c og /llm xxx > out.md` keeps a
		// clean markdown file.
		if ps, ok := init.(batchProgressSetter); ok {
			ps.SetOnProgress(batchDiagProgress)
		}
	}
	registerScheduler(registry, executor, cfg)

	// Load cluster skills if this node is part of a cluster.
	clusterCfg, clusterErr := cluster.LoadClusterConfig(opendbDir)
	if clusterErr == nil && clusterCfg.Initialized {
		switch clusterCfg.Role {
		case "memory":
			wm := overlord.NewWorkerManager(clusterCfg.NodeID, clusterCfg.Region)
			defer wm.Close()
			overlord.RegisterSkills(registry, wm, nil, clusterCfg.Region, nil)
		case "manager":
			fm := cerebrate.NewFleetManager(clusterCfg.NodeID)
			defer fm.Close()
			cerebrate.RegisterSkills(registry, fm)
		}
	}

	dispatcher := dispatch.NewDispatcher(registry, executor)

	// Join command args into a single input string.
	input := strings.Join(cmdArgs, " ")
	result, err := dispatcher.Dispatch(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if result == nil {
		return
	}

	// Print rendered output.
	text := result.Rendered
	if text == "" {
		if s, ok := result.Data.(string); ok {
			text = s
		}
	}
	if text != "" {
		fmt.Println(text)
	}

	// For table results, format and print. Use a generous TermWidth in
	// batch mode so diagnostic columns (buffers_alloc, response_time_p95,
	// etc.) don't get hidden behind a "...(N列)" placeholder — pipe-to-
	// file consumers need the full row.
	if result.Type == skill.ResultTable {
		if qr, ok := result.Data.(*db.QueryResult); ok {
			table := format.FormatTableOpts(qr, format.TableOptions{
				MaxRows:   100,
				TermWidth: 200,
			})
			fmt.Println(table)
		}
	}

	// Live-refresh skills (/dbtop) produce a DbtopRefreshSource that only
	// the interactive REPL can consume. In batch mode we'd otherwise print
	// nothing — give the user a clear pointer instead of silence.
	if result.Type == skill.ResultRefresh && text == "" {
		fmt.Printf("此命令需要交互式 REPL。请直接运行 `%s` 后输入 /dbtop 使用。\n", brand.Current().BinaryName)
	}

	// Only print summary when there's no rendered text (e.g. table-only results).
	// When Rendered is present, it already conveys the message — Summary would be redundant.
	if result.Summary != "" && text == "" {
		fmt.Fprintf(os.Stderr, "%s\n", result.Summary)
	}
}

// batchDiagProgress prints DiagnoseSkill progress events to stderr in batch
// mode. Format mirrors the REPL renderer (diag_renderer.go) so users see
// "第N轮: 调用 ..." instead of a 5+ minute silent diagnosis. Streaming
// reasoning chunks (ai_streaming) are dropped — printing thousands of
// thinking-mode tokens to stderr would drown out the round summaries.
func batchDiagProgress(phase, message string, elapsed time.Duration, _ *skill.Result, err error) {
	switch phase {
	case "start":
		if message != "" {
			fmt.Fprintf(os.Stderr, "✳ %s\n", message)
		}
	case "rule":
		if message != "" {
			fmt.Fprintf(os.Stderr, "├ 规则引擎: %s\n", message)
		}
	case "ai_start":
		if message != "" {
			fmt.Fprintf(os.Stderr, "✳ %s\n", message)
		}
	case "ai_round":
		if message != "" {
			fmt.Fprintf(os.Stderr, "├ %s (%.1fs)\n", message, elapsed.Seconds())
		}
	case "error":
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err.Error())
		}
	}
}

// runAsGuardian runs opendb as a guardian parent process.
// It saves terminal state, re-execs itself as a child (with _OPENDB_CHILD=1),
// and restores terminal after the child exits — even after kill -9.
func runAsGuardian() {
	fd := int(os.Stdin.Fd())
	savedState, err := term.GetState(fd)
	if err != nil {
		// Not a terminal (piped), just exec directly.
		execDirect()
		return
	}

	// Re-exec self as child process.
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "_OPENDB_CHILD=1")

	// Forward signals to child (except SIGKILL which can't be caught).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting opendb: %v\n", err)
		os.Exit(1)
	}

	// Forward signals to child in background.
	odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
		for sig := range sigCh {
			cmd.Process.Signal(sig)
		}
	})

	// Wait for child to exit (any reason: normal, signal, kill -9).
	cmd.Wait()
	signal.Stop(sigCh)

	// Restore terminal state — this is the key: even after kill -9 on child,
	// the parent survives and restores the terminal.
	term.Restore(fd, savedState)
}

// execDirect replaces the current process with the child (non-TTY fallback).
func execDirect() {
	os.Setenv("_OPENDB_CHILD", "1")
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	syscall.Exec(exe, os.Args, os.Environ())
}
