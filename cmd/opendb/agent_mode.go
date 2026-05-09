/*-------------------------------------------------------------------------
 *
 * agent_mode.go
 *	  Cluster agent bootstrap — wires worker / memory / manager daemon
 *	  startup from cmd/opendb/, where product registration (driver
 *	  factories, AI skills) is available. Imported by main.go's
 *	  cluster subcommand handler.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/agent_mode.go
 *
 *-------------------------------------------------------------------------
 */
package main

// agent_mode.go wires all three roles (worker/memory/manager) for daemon startup.
// This file lives in cmd/opendb/ because product registration (driver factories, AI skills)
// is only available here.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/brand"
	"github.com/sqlrush/opendb/internal/cerebrate"
	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/drone"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/odberr"
	"github.com/sqlrush/opendb/internal/overlord"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
	"github.com/sqlrush/opendb/internal/ui"
)

// runAgentMode handles "opendb agent start --role <worker|memory|manager>".
func runAgentMode(args drone.AgentArgs) error {
	// PID check and write.
	if err := drone.CheckAndWritePID(args.Role); err != nil {
		return err
	}
	defer drone.RemovePIDFile(args.Role)

	fmt.Printf("OpenDB Agent starting (role=%s, pid=%d)\n", args.Role, os.Getpid())

	// Signal handling.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
		sig := <-sigCh
		fmt.Printf("\nReceived %v, shutting down gracefully...\n", sig)
		cancel()
	})

	// Load cluster config to get saved addresses.
	opendbDir := defaultOpenDBDir()
	clusterCfg, _ := cluster.LoadClusterConfig(opendbDir)
	mergeClusterConfig(&args, clusterCfg) // #8: cluster config → agent args

	// Dispatch to role-specific startup.
	switch args.Role {
	case "worker":
		return runWorkerMode(ctx, args, opendbDir)
	case "memory":
		return runOverlordMode(ctx, args, opendbDir)
	case "manager":
		return runCerebrateMode(ctx, args, opendbDir)
	default:
		return fmt.Errorf("unknown role: %s (use worker, memory, or manager)", args.Role)
	}
}

// mergeClusterConfig fills agent args from saved cluster config (#8).
func mergeClusterConfig(args *drone.AgentArgs, clusterCfg cluster.ClusterConfig) {
	if !clusterCfg.Initialized {
		return
	}
	if args.Role == "" {
		args.Role = clusterCfg.Role
	}
	if args.Listen == "0.0.0.0:9300" && clusterCfg.Listen != "" {
		args.Listen = clusterCfg.Listen
	}
	if args.Overlord == "" && clusterCfg.Overlord != "" {
		args.Overlord = clusterCfg.Overlord
	}
	if args.Web == "" && clusterCfg.WebListen != "" {
		args.Web = clusterCfg.WebListen
	}
}

// ============================================================================
// Worker Mode (Drone)
// ============================================================================

func runWorkerMode(ctx context.Context, args drone.AgentArgs, opendbDir string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	modelMgr, err := model.NewManager(cfg.ModelsDir, cfg.ActiveModel, model.FallbackLLM{
		Provider: cfg.LLM.Provider, BaseURL: cfg.LLM.BaseURL,
		Model: cfg.LLM.Model, Capability: cfg.LLM.Capability,
		InlineModels: configToInlineModels(cfg),
	})
	if err != nil {
		return fmt.Errorf("load models: %w", err)
	}

	// Fix ISSUE-004: Warn if no LLM configured — Worker will fall back to Rule Engine.
	if cfg.LLM.Provider == "" && cfg.ActiveModel == "" && len(cfg.Models) == 0 {
		fmt.Println("WARNING: No LLM configured. Worker will use Rule Engine fallback for diagnostics.")
		fmt.Printf("  Run '%s /llm' to configure, or copy config.yaml from a configured node.\n", brand.Current().BinaryName)
	}

	connOpts := buildProductConnOpts()
	connMgr, err := connection.NewManager(cfg, connOpts...)
	if err != nil {
		return fmt.Errorf("create connection manager: %w", err)
	}

	names := connMgr.ConnectionNames()
	dbConnected := false
	if len(names) > 0 {
		if err := connMgr.Connect(ctx, names[0]); err != nil {
			fmt.Printf("Database connection failed: %v (running in heartbeat mode)\n", err)
		} else {
			dbConnected = true
			fmt.Printf("Connected to database: %s (%s)\n", names[0], connMgr.CurrentDBType())
		}
	} else {
		fmt.Println("No database connections configured (running in heartbeat mode)")
	}

	driver := connection.NewLazyDriver(connMgr)
	sessionStore := session.NewStore(cfg.Session.HistoryDir)
	sessionHistory := sessionStore.GetOrCreate("_default")

	registry := skill.NewRegistry()
	registerSharedSkills(registry, connMgr, sessionHistory, cfg, modelMgr)

	guard := security.NewGuard(security.LevelDangerous, func(_ string) (bool, error) {
		return true, nil // L4 auto-confirm
	})
	executor := skill.NewExecutor(registry, guard)

	var sentinelSource ui.SentinelAlertSource

	if dbConnected {
		for _, p := range products {
			p.registerSkills(registry, driver, connMgr, sessionHistory, cfg, modelMgr, opendbDir)
		}
		if dbType := connMgr.CurrentDBType(); dbType != "" {
			registry.SetActiveDB(dbType)
		}
		activeDB := connMgr.CurrentDBType()
		for _, p := range products {
			// Only register AI skills for the active database type
			// to avoid wrong product's SentinelSkill overriding the correct one.
			if activeDB != "" && p.name != activeDB {
				continue
			}
			ss, _ := p.registerAISkills(registry, driver, executor, cfg, modelMgr)
			if ss != nil {
				sentinelSource = ss
			}
		}
		registerScheduler(registry, executor, cfg)
		registry.Register(drone.NewInjectSkill(driver))
	}

	// Register skills that work without DB connection.
	registry.Register(drone.NewEscalateToOverlordSkill())
	connName := connMgr.CurrentName()
	if connName == "" {
		connName = "standalone"
	}
	ruleWriter := drone.NewRuleWriter(opendbDir, connName)
	registry.Register(drone.NewRuleWriteSkill(ruleWriter))
	registry.Register(shared.NewPolicySkill(
		func() string { return filepath.Join(opendbDir, "policies") },
		func() string { return connMgr.CurrentName() },
	))

	auditLogger, err := drone.NewAuditLogger(opendbDir)
	if err != nil {
		return fmt.Errorf("create audit logger: %w", err)
	}
	defer auditLogger.Close()

	workerClusterCfg, _ := cluster.LoadClusterConfig(opendbDir)
	workerID := workerClusterCfg.NodeID
	if workerID == "" {
		hostname, _ := os.Hostname()
		workerID = fmt.Sprintf("drone-%s-%d", hostname, os.Getpid())
	}
	loop := drone.NewAutonomyLoop(drone.AutonomyConfig{
		Config: cfg, ConnMgr: connMgr, Driver: driver,
		Registry: registry, Executor: executor, ModelMgr: modelMgr,
		AuditLogger: auditLogger, Role: args.Role, Listen: args.Listen,
		WorkerID: workerID, DBType: connMgr.CurrentDBType(),
		Instance: connMgr.CurrentName(), OverlordAddr: args.Overlord,
		RuleWriter: ruleWriter,
		LLMRecordDir: args.LLMRecord, LLMReplayFile: args.LLMReplay,
	})

	if sentinelSource != nil {
		loop.SetSentinelStartFn(func(sctx context.Context) (<-chan alert.Event, error) {
			if err := sentinelSource.AutoStart(sctx); err != nil {
				return nil, err
			}
			return sentinelSource.AlertCh(), nil
		})
		fmt.Printf("Sentinel wired for %s database\n", connMgr.CurrentDBType())
	}

	loop.SetDiagnoseFn(func(dctx context.Context, evt alert.Event) (*skill.Result, error) {
		return executor.Execute(dctx, "llm", skill.ParamsFromMap(map[string]any{
			"args": "1", "mode": "auto",
		}))
	})

	fmt.Println("Entering Worker Autonomy Loop...")
	return loop.Run(ctx)
}

// ============================================================================
// Overlord Mode (Memory Agent)
// ============================================================================

func runOverlordMode(ctx context.Context, args drone.AgentArgs, opendbDir string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	_ = cfg

	clusterCfg, _ := cluster.LoadClusterConfig(opendbDir)
	region := clusterCfg.Region
	if region == "" {
		region = "default"
	}
	overlordID := clusterCfg.NodeID
	if overlordID == "" {
		overlordID = fmt.Sprintf("overlord-%d", os.Getpid())
	}

	// Create Worker manager and Overlord gRPC server.
	workerMgr := overlord.NewWorkerManager(overlordID, region)
	defer workerMgr.Close()

	overlordSrv := overlord.NewOverlordServer(overlordID, region, workerMgr)
	defer overlordSrv.Close()

	// Create registration handler (accepts Worker gRPC registrations).
	regHandler := overlord.NewRegistrationHandler(workerMgr, nil)

	// Create correlation engine (detects cross-worker patterns).
	corrEngine := overlord.NewCorrelationEngine(workerMgr)

	// #1: Wire Worker alert events → correlation engine → auto-escalate.
	overlordSrv.SetOnWorkerAlert(func(evt *pb.AlertEvent) {
		regHandler.HandleAlertEvent(evt)
		results := corrEngine.AddAlert(overlord.AlertRecord{
			WorkerID: evt.WorkerId,
			Alert:    evt,
			Received: time.Now(),
		})
		// #2: If correlation detected, auto-escalate to Cerebrate.
		if len(results) > 0 {
			report := overlord.FormatCorrelation(results)
			fmt.Printf("[overlord] Correlation detected:\n%s\n", report)
			overlordSrv.PushEvent(&pb.OverlordEvent{
				EventId:   fmt.Sprintf("corr-%d", time.Now().UnixMilli()),
				Timestamp: time.Now().Unix(),
				Payload: &pb.OverlordEvent_Escalation{
					Escalation: &pb.AlertEvent{
						Description:   report,
						Severity:      results[0].Severity,
						RepairSummary: fmt.Sprintf("%d correlated patterns across %d workers", len(results), len(results[0].AffectedIDs)),
					},
				},
			})
		}
	})

	// #1: Wire memory sync events → registration handler.
	overlordSrv.SetOnMemorySync(func(evt *pb.MemorySyncEvent) {
		regHandler.HandleMemorySync(evt)
	})

	// Create DB access manager for direct database connections.
	dbAccessMgr := overlord.NewDBAccessManager()
	// Wire product driver factories so Overlord can directly connect to Worker databases.
	if len(products) > 0 {
		dbAccessMgr.SetDriverFactory(products[0].driverFactory)
	}

	// Create skill registry and register Overlord skills.
	registry := skill.NewRegistry()
	overlord.RegisterSkills(registry, workerMgr, overlordSrv, region, dbAccessMgr)
	overlord.RegisterDBAccessSkill(registry, dbAccessMgr)

	// #7: Load hot config.
	hotCfg, _ := cluster.LoadHotConfig(filepath.Join(opendbDir, "cluster", "hotconfig.yaml"))
	if hotCfg == nil {
		hotCfg = cluster.NewHotConfig()
	}
	_ = hotCfg

	// #3: Connect to Cerebrate if configured (auto-reconnect on failure).
	// Must happen before gRPC server starts so AgentService can trigger immediate heartbeats.
	cerebrateAddr := clusterCfg.Cerebrate
	var cerebrateClient *overlord.CerebrateClient
	if cerebrateAddr != "" {
		cerebrateClient = overlord.NewCerebrateClient(cerebrateAddr, overlordID, region, args.Listen)
		if err := cerebrateClient.Connect(ctx, 200); err != nil {
			fmt.Printf("[overlord] Cerebrate initial connection failed: %v (will retry via heartbeat)\n", err)
		}
		go cerebrateClient.StartHeartbeat(ctx, 30*time.Second, workerMgr)
		defer cerebrateClient.Close()
	}

	// Create AgentService and wire: Worker registration triggers immediate Cerebrate heartbeat.
	agentSvc := overlord.NewOverlordAgentService(workerMgr, overlordID)
	agentSvc.SetDBAccessManager(dbAccessMgr) // #1: Worker DB config auto-registered

	// ISSUE-013: Wire token validator so Workers must present the correct join token.
	if clusterCfg.JoinToken != "" {
		expectedToken := clusterCfg.JoinToken
		agentSvc.SetTokenValidator(func(token string) bool {
			return token == expectedToken
		})
	}
	if cerebrateClient != nil {
		workerChangedFn := func() {
			cerebrateClient.SendHeartbeatNow(workerMgr)
		}
		agentSvc.SetOnWorkerChanged(workerChangedFn)
		// ISSUE-006: Wire WorkerManager state-change callback so heartbeat timeout
		// also triggers immediate Cerebrate update.
		workerMgr.SetOnWorkerChanged(workerChangedFn)
	}

	// Start Overlord gRPC server (OverlordService + AgentService).
	odberr.SafeGo(odberr.ErrClusterRPC, func() {
		if err := overlord.StartOverlordGRPCServer(ctx, overlordSrv, agentSvc, args.Listen); err != nil {
			fmt.Printf("[overlord] gRPC server error: %v\n", err)
		}
	})

	// Start Worker status poller (every 60s).
	go workerMgr.StartStatusPoller(ctx, 30*time.Second)

	// C10: Start StandbyMonitor to detect Cerebrate failure and trigger HA election.
	if cerebrateAddr != "" {
		standby := cerebrate.NewStandbyMonitor(cerebrateAddr, overlordID, args.Listen)
		odberr.SafeGo(odberr.ErrClusterRPC, func() {
			promoted := standby.Start(ctx)
			if promoted {
				fmt.Printf("[overlord] *** PROMOTED TO CEREBRATE — starting Cerebrate services ***\n")
				// TODO: Start Cerebrate gRPC + Web services in dual-role mode.
				// For now, log the promotion decision. Full dual-role is a future milestone.
			} else {
				fmt.Printf("[overlord] Election completed — another Overlord was promoted\n")
			}
		})
	}

	// #5: Start mirror sync to peers (every 5 minutes).
	mirrorMgr := overlord.NewMirrorManager(overlordID, workerMgr)
	go mirrorMgr.StartPeriodicSync(ctx, 5*time.Minute)
	defer mirrorMgr.Close()

	fmt.Printf("Overlord running (id=%s, region=%s, listen=%s)\n", overlordID, region, args.Listen)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("[overlord] Shutting down...")
			return nil
		case t := <-ticker.C:
			fmt.Printf("[%s] overlord heartbeat (workers=%d, online=%d, mirrors=%d)\n",
				t.Format("15:04:05"), workerMgr.WorkerCount(), workerMgr.OnlineCount(), mirrorMgr.PeerCount())
		}
	}
}

// ============================================================================
// Cerebrate Mode (Manager Agent)
// ============================================================================

func runCerebrateMode(ctx context.Context, args drone.AgentArgs, opendbDir string) error {
	clusterCfg, _ := cluster.LoadClusterConfig(opendbDir)
	cerebrateID := clusterCfg.NodeID
	if cerebrateID == "" {
		cerebrateID = fmt.Sprintf("cerebrate-%d", os.Getpid())
	}

	// Create Fleet manager (connects to Overlords).
	fleetMgr := cerebrate.NewFleetManager(cerebrateID)
	defer fleetMgr.Close()

	// Register Cerebrate skills.
	registry := skill.NewRegistry()
	cerebrate.RegisterSkills(registry, fleetMgr)

	// Create config audit manager and LLM priority queue.
	configAuditMgr := cerebrate.NewConfigAuditManager(fleetMgr)
	llmQueue := cerebrate.NewLLMPriorityQueue()
	cerebrate.RegisterExtendedSkills(registry, configAuditMgr, llmQueue)
	_ = registry

	// #4: Start gRPC server to accept Overlord registrations.
	// Cerebrate reuses OverlordService as its registration endpoint —
	// Overlords call RegisterOverlord on this server.
	cerebrateGRPC := cerebrate.NewCerebrateGRPCServer(cerebrateID, fleetMgr)

	// ISSUE-013: Wire token validator so Overlords must present the correct join token.
	if savedToken, err := cluster.LoadToken(opendbDir); err == nil && savedToken.Value != "" {
		cerebrateGRPC.SetTokenValidator(func(token string) bool {
			return token == savedToken.Value && savedToken.IsValid()
		})
	}

	odberr.SafeGo(odberr.ErrClusterRPC, func() {
		if err := cerebrateGRPC.Start(ctx, args.Listen); err != nil {
			fmt.Printf("[cerebrate] gRPC server error: %v\n", err)
		}
	})

	// Start Overlord status poller (every 30 seconds).
	go fleetMgr.StartStatusPoller(ctx, 30*time.Second)

	// Start periodic config audit (every hour).
	go configAuditMgr.StartPeriodicCollection(ctx, 1*time.Hour)

	// #5: Start HA manager (sync to 2 backup Overlords).
	haMgr := cerebrate.NewHAManager(cerebrateID, fleetMgr)
	go haMgr.StartPeriodicSync(ctx, 2*time.Minute)
	defer haMgr.Close()

	// C10: Wire HA manager to gRPC server for auto-backup designation.
	cerebrateGRPC.SetHAManager(haMgr)

	// #7: Load hot config.
	hotCfg, _ := cluster.LoadHotConfig(filepath.Join(opendbDir, "cluster", "hotconfig.yaml"))
	if hotCfg == nil {
		hotCfg = cluster.NewHotConfig()
	}
	_ = hotCfg

	// Create shared TimelineStore and ReportStore.
	timelineStore := cerebrate.NewTimelineStore(1000)
	reportStore := cerebrate.NewReportStore(1000)

	// Wire timeline/reports into gRPC server (for event tracking on registration/heartbeat).
	cerebrateGRPC.SetTimeline(timelineStore)
	cerebrateGRPC.SetReports(reportStore)

	// Start Web dashboard + Pull install server if configured.
	if args.Web != "" {
		dashboard := cerebrate.NewWebDashboard(fleetMgr, args.Web)
		dashboard.SetTimeline(timelineStore)
		dashboard.SetReports(reportStore)
		// #5: Register Pull install routes on the same HTTP mux.
		token, _ := cluster.LoadToken(opendbDir)
		pullServer := cerebrate.NewPullInstallServer("", args.Web, token.Value)
		dashboard.RegisterPullInstall(pullServer)
		odberr.SafeGo(odberr.ErrClusterRPC, func() {
			if err := dashboard.Start(ctx); err != nil {
				fmt.Printf("[cerebrate] Web dashboard error: %v\n", err)
			}
		})
	}

	fmt.Printf("Cerebrate running (id=%s, listen=%s, ha_backups=%d)\n", cerebrateID, args.Listen, haMgr.BackupCount())
	if args.Web != "" {
		fmt.Printf("Web dashboard: http://%s\n", args.Web)
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("[cerebrate] Shutting down...")
			return nil
		case t := <-ticker.C:
			fmt.Printf("[%s] cerebrate heartbeat (overlords=%d, workers=%d, ha=%d)\n",
				t.Format("15:04:05"), fleetMgr.OverlordCount(), fleetMgr.TotalWorkerCount(), haMgr.BackupCount())
		}
	}
}

// ============================================================================
// Helpers
// ============================================================================

func buildProductConnOpts() []connection.Option {
	connOpts := []connection.Option{}
	for _, p := range products {
		connOpts = append(connOpts, connection.WithDriverFactoryFor(p.name, p.driverFactory))
	}
	if len(products) > 0 {
		connOpts = append(connOpts, connection.WithDriverFactory(products[0].driverFactory))
	}
	return connOpts
}
