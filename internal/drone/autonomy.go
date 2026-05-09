/*-------------------------------------------------------------------------
 *
 * autonomy.go
 *	  autonomy.go implements the Autonomy Loop for Worker Agent (Drone).
 *	  The loop runs: sense (Sentinel) → diagnose (LLM/Rule Engine) →
 *	  act (repair) → report.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/autonomy.go
 *
 *-------------------------------------------------------------------------
 */
// autonomy.go implements the Autonomy Loop for Worker Agent (Drone).
// The loop runs: sense (Sentinel) → diagnose (LLM/Rule Engine) → act (repair) → report.
package drone

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/odberr"
	"github.com/sqlrush/opendb/internal/skill"
)

// AutonomyLoop is the core daemon loop that continuously monitors and heals.
type AutonomyLoop struct {
	cfg      *config.Config
	connMgr  *connection.Manager
	driver   db.Driver
	registry *skill.Registry
	executor *skill.Executor
	modelMgr *model.Manager
	audit    *AuditLogger
	role     string
	listen   string // gRPC listen address
	logger   *slog.Logger

	// grpcServer is the Worker gRPC server (供 Overlord 连接).
	grpcServer *DroneServer

	// overlordClient connects to Overlord for registration, heartbeat, and event reporting.
	overlordClient *OverlordClient

	// ruleWriter manages auto-generated rules from successful repairs (#6).
	ruleWriter *RuleWriter

	// sentinelAlertCh receives anomaly events from Sentinel.
	sentinelAlertCh <-chan alert.Event

	// sentinelStartFn starts sentinel and returns its alert channel.
	// Set by product-specific init (Oracle/MySQL/PG/OpenGauss).
	sentinelStartFn func(ctx context.Context) (<-chan alert.Event, error)

	// diagnoseFn triggers a diagnosis for an alert event.
	// Set by product-specific init.
	diagnoseFn func(ctx context.Context, evt alert.Event) (*skill.Result, error)
}

// AutonomyConfig holds configuration for the autonomy loop.
type AutonomyConfig struct {
	Config       *config.Config
	ConnMgr      *connection.Manager
	Driver       db.Driver
	Registry     *skill.Registry
	Executor     *skill.Executor
	ModelMgr     *model.Manager
	AuditLogger  *AuditLogger
	Role         string
	Listen       string // gRPC listen address (e.g., "0.0.0.0:9300")
	WorkerID     string // Unique worker identifier
	DBType       string // Database type for gRPC registration
	Instance     string // Instance name for gRPC registration
	OverlordAddr string // Overlord address for registration
	JoinToken    string // Join token for Overlord registration
	RuleWriter   *RuleWriter // Rule auto-generation from successful repairs
	LLMRecordDir string // --llm-record directory (empty = disabled)
	LLMReplayFile string // --llm-replay file (empty = disabled)
}

// NewAutonomyLoop creates a new autonomy loop with the given configuration.
func NewAutonomyLoop(ac AutonomyConfig) *AutonomyLoop {
	hostname, _ := os.Hostname()
	var overlordClient *OverlordClient
	if ac.OverlordAddr != "" {
		overlordClient = NewOverlordClient(ac.OverlordAddr, ac.WorkerID, hostname, ac.DBType, ac.Instance, ac.Listen)
	}

	return &AutonomyLoop{
		cfg:            ac.Config,
		connMgr:        ac.ConnMgr,
		driver:         ac.Driver,
		registry:       ac.Registry,
		executor:       ac.Executor,
		modelMgr:       ac.ModelMgr,
		audit:          ac.AuditLogger,
		role:           ac.Role,
		listen:         ac.Listen,
		logger:         cluster.DefaultLogger("autonomy"),
		grpcServer:     NewDroneServer(ac.WorkerID, ac.DBType, ac.Instance),
		overlordClient: overlordClient,
		ruleWriter:     ac.RuleWriter,
	}
}

// SetSentinelStartFn sets the function that starts Sentinel and returns its alert channel.
func (al *AutonomyLoop) SetSentinelStartFn(fn func(ctx context.Context) (<-chan alert.Event, error)) {
	al.sentinelStartFn = fn
}

// SetDiagnoseFn sets the function that runs diagnosis for an alert event.
func (al *AutonomyLoop) SetDiagnoseFn(fn func(ctx context.Context, evt alert.Event) (*skill.Result, error)) {
	al.diagnoseFn = fn
}

// Run starts the autonomy loop. It blocks until ctx is cancelled.
func (al *AutonomyLoop) Run(ctx context.Context) error {
	// Wire redirect callback (C09): Cerebrate can instruct this Worker to switch Overlord.
	if al.overlordClient != nil {
		al.grpcServer.SetRedirectFunc(al.overlordClient.RedirectTo)
	}

	// Start gRPC Server in background (供 Overlord 连接).
	if al.listen != "" {
		odberr.SafeGo(odberr.ErrClusterRPC, func() {
			if err := StartGRPCServer(ctx, al.grpcServer, al.listen); err != nil {
				al.logger.Error("gRPC server error", slog.String("error", err.Error()))
			}
		})
	}

	// Run startup recovery (scan memory + DB logs, LLM judges if recovery needed).
	RunRecovery(ctx, RecoveryConfig{
		BaseDir:  defaultPIDDir(),
		Instance: al.grpcServer.instance,
		Executor: al.executor,
	})

	// Start memory/policy incremental sync (every 1 minute).
	memorySyncer := NewMemorySyncer(defaultPIDDir(), al.grpcServer.instance, 1*time.Minute, al.grpcServer)
	go memorySyncer.Start(ctx)

	// Connect to Overlord if configured.
	if al.overlordClient != nil {
		if err := al.overlordClient.Connect(ctx, ""); err != nil {
			al.logger.Warn("Overlord 连接失败（独立运行模式）", slog.String("error", err.Error()))
		} else {
			// Start heartbeat in background (every 30s).
			go al.overlordClient.StartHeartbeat(ctx, 30*time.Second, func() (int32, string) {
				if al.grpcServer != nil {
					al.grpcServer.mu.RLock()
					defer al.grpcServer.mu.RUnlock()
					return al.grpcServer.health.Score, al.grpcServer.health.Summary
				}
				return 100, "healthy"
			})
		}
	}

	// Phase 1: Start Sentinel.
	if al.sentinelStartFn != nil {
		alertCh, err := al.sentinelStartFn(ctx)
		if err != nil {
			al.logger.Warn("Sentinel 启动失败, 降级为心跳模式", slog.String("error", err.Error()))
		} else {
			al.sentinelAlertCh = alertCh
			al.logger.Info("Sentinel 已启动，进入自治巡航")
		}
	} else {
		al.logger.Info("未配置 Sentinel，进入心跳模式")
	}

	// Main loop: listen for alerts + periodic heartbeat.
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			al.logger.Info("自治循环停止")
			return nil

		case evt, ok := <-al.sentinelAlertCh:
			if !ok {
				// Channel closed, sentinel stopped.
				al.sentinelAlertCh = nil
				al.logger.Warn("Sentinel 通道关闭，降级为心跳模式")
				continue
			}
			al.handleAlert(ctx, evt)

		case <-heartbeat.C:
			al.logger.Debug("autonomy heartbeat", slog.String("role", al.role))
		}
	}
}

// crossNodeCauses defines CauseNames that require Overlord cross-node diagnosis.
var crossNodeCauses = map[string]bool{
	"replication_lag":  true, // involves primary + replica
	"connection_storm": true, // may be application-layer global issue
	"xid_wraparound":  true, // needs global freeze coordination
	"wal_bottleneck":   true, // affects replica replication
}

// forceEscalateCauses require both local handling AND forced escalation.
var forceEscalateCauses = map[string]bool{
	"database_hang": true, // extreme severity, Overlord must evaluate failover
}

// isLikelyCrossNode checks if the alert likely requires cross-node diagnosis.
func isLikelyCrossNode(causeName string) bool {
	return crossNodeCauses[causeName]
}

// handleAlert processes a single Sentinel alert event with diagnosis routing:
//   - Cross-node causes → skip local LLM, escalate to Overlord
//   - Single-node causes → Worker local LLM + backup to Overlord
//   - Unknown causes → dual path (local LLM + Overlord independent review)
//   - LLM unavailable → escalate upward (not Rule Engine); Rule Engine only as last resort
func (al *AutonomyLoop) handleAlert(ctx context.Context, evt alert.Event) {
	start := time.Now()
	al.logger.Warn("检测到异常", slog.String("description", evt.Description), slog.String("cause", evt.CauseName))

	// Inject matched rules into context.
	if al.ruleWriter != nil {
		matched := al.ruleWriter.LoadMatchedContent(evt.CauseName)
		if matched != "" {
			al.logger.Info("匹配到历史经验规则", slog.Int("count", len(al.ruleWriter.MatchRules(evt.CauseName))))
		}
	}

	// Diagnosis routing based on CauseName classification.
	var diagResult *skill.Result
	var diagSource string // "local" | "escalated" | "rule_engine"

	if isLikelyCrossNode(evt.CauseName) {
		// Cross-node: skip local LLM, escalate to Overlord.
		al.logger.Info("跨节点问题，上交 Overlord 处理", slog.String("cause", evt.CauseName))
		al.escalateToOverlord(ctx, evt, "cross_node")
		diagResult = &skill.Result{
			Type:    skill.ResultText,
			Summary: fmt.Sprintf("已上报 Overlord 进行跨节点分析 (cause=%s)", evt.CauseName),
		}
		diagSource = "escalated"
	} else {
		// Single-node or unknown: attempt local LLM diagnosis.
		// Apply a 5-minute timeout to prevent runaway diagnosis loops.
		diagCtx, diagCancel := context.WithTimeout(ctx, 5*time.Minute)
		localResult, localErr := al.tryLocalDiagnosis(diagCtx, evt)
		diagCancel()
		if localErr != nil {
			// Local LLM failed → escalate upward (NOT Rule Engine).
			al.logger.Warn("本地 LLM 不可用，上报 Overlord 接管诊断", slog.String("error", localErr.Error()))
			al.escalateToOverlord(ctx, evt, "llm_unavailable")
			diagResult = &skill.Result{
				Type:    skill.ResultText,
				Summary: "本地 LLM 不可用，已上报 Overlord 接管",
			}
			diagSource = "escalated"
		} else {
			diagResult = localResult
			diagSource = "local"
		}

		// Unknown causes: also escalate for Overlord independent review (dual path).
		if evt.CauseName == "unknown" || evt.CauseName == "" {
			al.logger.Info("未知异常类型，同步上报 Overlord 进行独立诊断复核")
			al.escalateToOverlord(ctx, evt, "unknown_dual_path")
		}

		// Force-escalate causes: always report even if local succeeded.
		if forceEscalateCauses[evt.CauseName] {
			al.logger.Info("高严重度异常，强制上报 Overlord 评估 failover")
			al.escalateToOverlord(ctx, evt, "force_escalate")
		}
	}

	// For successful local diagnoses of known single-node issues, send backup to Overlord.
	if diagSource == "local" && !isLikelyCrossNode(evt.CauseName) &&
		!forceEscalateCauses[evt.CauseName] && evt.CauseName != "unknown" && evt.CauseName != "" {
		al.pushAlertBackup(ctx, evt, diagResult)
	}

	elapsed := time.Since(start)
	summary := "无诊断结论"
	if diagResult != nil && diagResult.Summary != "" {
		summary = diagResult.Summary
	}
	al.logger.Info("诊断完成",
		slog.String("elapsed", elapsed.Round(time.Millisecond).String()),
		slog.String("summary", summary),
		slog.String("source", diagSource))

	if al.audit != nil {
		al.audit.Log(al.role, evt.CauseName, "auto_diagnose", evt.Description, diagSource)
	}

	al.generateFaultReport(evt, diagResult, summary)
}

// tryLocalDiagnosis attempts local LLM diagnosis. Falls back to Rule Engine only
// when no LLM is configured at all — LLM call failure escalates upward instead.
func (al *AutonomyLoop) tryLocalDiagnosis(ctx context.Context, evt alert.Event) (*skill.Result, error) {
	if al.diagnoseFn != nil {
		return al.diagnoseFn(ctx, evt)
	}
	// No LLM configured at all → Rule Engine as absolute last resort.
	al.logger.Info("未配置 LLM，Rule Engine 兜底诊断", slog.String("cause", evt.CauseName))
	return al.fallbackRuleEngine(ctx, evt), nil
}

// escalateToOverlord sends an escalation event to Overlord for cross-node or LLM-takeover diagnosis.
func (al *AutonomyLoop) escalateToOverlord(ctx context.Context, evt alert.Event, reason string) {
	if al.grpcServer == nil {
		return
	}
	al.grpcServer.PushEvent(&pb.AgentEvent{
		EventId:   fmt.Sprintf("escalate-%d", time.Now().UnixMilli()),
		Timestamp: time.Now().Unix(),
		Payload: &pb.AgentEvent_Alert{
			Alert: &pb.AlertEvent{
				WorkerId:      al.grpcServer.workerID,
				Description:   evt.Description,
				CauseName:     evt.CauseName,
				DurationSec:   evt.DurationSec,
				Severity:      classifySeverity(evt),
				SelfHealed:    false,
				RepairSummary: fmt.Sprintf("escalated: %s", reason),
			},
		},
	})
}

// pushAlertBackup sends a backup alert to Overlord after successful local diagnosis.
func (al *AutonomyLoop) pushAlertBackup(ctx context.Context, evt alert.Event, diagResult *skill.Result) {
	if al.grpcServer == nil {
		return
	}
	summary := ""
	if diagResult != nil {
		summary = diagResult.Summary
	}
	al.grpcServer.PushEvent(&pb.AgentEvent{
		EventId:   fmt.Sprintf("alert-%d", time.Now().UnixMilli()),
		Timestamp: time.Now().Unix(),
		Payload: &pb.AgentEvent_Alert{
			Alert: &pb.AlertEvent{
				WorkerId:      al.grpcServer.workerID,
				Description:   evt.Description,
				CauseName:     evt.CauseName,
				DurationSec:   evt.DurationSec,
				Severity:      classifySeverity(evt),
				SelfHealed:    diagResult != nil,
				RepairSummary: summary,
			},
		},
	})
}

// fallbackRuleEngine runs rule-based diagnosis when LLM is unavailable.
func (al *AutonomyLoop) fallbackRuleEngine(ctx context.Context, evt alert.Event) *skill.Result {
	al.logger.Info("Rule Engine 兜底诊断", slog.String("cause", evt.CauseName))

	// Execute the "rule" skill if available.
	if al.executor != nil {
		result, err := al.executor.Execute(ctx, "rule", skill.ParamsFromMap(map[string]any{
			"args": "auto",
		}))
		if err == nil && result != nil {
			return result
		}
	}

	return &skill.Result{
		Type:    skill.ResultText,
		Summary: fmt.Sprintf("Rule Engine 处理: %s", evt.CauseName),
	}
}

// generateFaultReport creates and saves a fault analysis report after diagnosis.
func (al *AutonomyLoop) generateFaultReport(evt alert.Event, diagResult *skill.Result, summary string) {
	if al.grpcServer == nil {
		return
	}

	analysis := summary
	if diagResult != nil && diagResult.Rendered != "" {
		analysis = diagResult.Rendered
	}

	severity := classifySeverity(evt)
	instance := al.grpcServer.instance
	dbType := al.grpcServer.dbType
	host := al.grpcServer.hostname

	diagInput := DiagnosisInput{
		Mode:     "auto",
		Analysis: analysis,
	}

	report := GenerateFaultReport(diagInput, nil, instance, dbType, host)
	baseDir := defaultPIDDir()
	path, err := SaveReport(baseDir, report)
	if err != nil {
		al.logger.Warn("保存故障报告失败", slog.String("error", err.Error()))
		return
	}
	al.logger.Info("故障报告已生成", slog.String("path", path), slog.String("severity", severity))
}

// classifySeverity maps alert event to severity level.
func classifySeverity(evt alert.Event) string {
	if evt.DurationSec > 60 {
		return "critical"
	}
	return "warning"
}

// initDaemonServices initializes all services needed for daemon mode.
// This creates the connection manager, driver, skill registry, executor, and model manager
// from configuration — without interactive input.
func initDaemonServices(args AgentArgs) (*AutonomyConfig, error) {
	// Load configuration.
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Create model manager.
	modelMgr, err := model.NewManager(cfg.ModelsDir, cfg.ActiveModel, model.FallbackLLM{
		Provider:     cfg.LLM.Provider,
		BaseURL:      cfg.LLM.BaseURL,
		Model:        cfg.LLM.Model,
		Capability:   cfg.LLM.Capability,
		InlineModels: configToInlineModels(cfg),
	})
	if err != nil {
		return nil, fmt.Errorf("load models: %w", err)
	}

	// Create connection manager with driver factories from registered products.
	connOpts := buildConnectionOptions()
	connMgr, err := connection.NewManager(cfg, connOpts...)
	if err != nil {
		return nil, fmt.Errorf("create connection manager: %w", err)
	}

	// Auto-connect to the first configured connection (or specified via --db-conn).
	if err := autoConnect(connMgr, args); err != nil {
		return nil, fmt.Errorf("auto connect: %w", err)
	}

	driver := connection.NewLazyDriver(connMgr)

	// Create skill registry and executor.
	registry := skill.NewRegistry()
	guard := security.NewGuard(security.LevelDangerous, func(_ string) (bool, error) {
		// In daemon mode, auto-confirm all operations (L4 autonomy).
		return true, nil
	})
	executor := skill.NewExecutor(registry, guard)

	// Create audit logger.
	baseDir := defaultPIDDir() // reuse ~/.opendb as base
	auditLogger, err := NewAuditLogger(baseDir)
	if err != nil {
		return nil, fmt.Errorf("create audit logger: %w", err)
	}

	return &AutonomyConfig{
		Config:      cfg,
		ConnMgr:     connMgr,
		Driver:      driver,
		Registry:    registry,
		Executor:    executor,
		ModelMgr:    modelMgr,
		AuditLogger: auditLogger,
		Role:        args.Role,
	}, nil
}

// autoConnect connects to the first available connection or the one specified in args.
func autoConnect(connMgr *connection.Manager, args AgentArgs) error {
	// If connection string provided via CLI, parse and use ConnectDirect.
	// For now, daemon mode uses configured connections (--db-conn support in future).
	_ = args // reserved for future --db-conn parsing

	// Otherwise connect to the first configured connection.
	names := connMgr.ConnectionNames()
	if len(names) == 0 {
		return fmt.Errorf("no database connections configured. Run 'opendb configure' first")
	}
	return connMgr.Connect(context.Background(), names[0])
}

// configToInlineModels converts config model list to model.InlineModel slice.
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
		}
	}
	return result
}

// buildConnectionOptions creates connection options from registered products.
// This is a daemon-mode equivalent of the logic in main.go.
func buildConnectionOptions() []connection.Option {
	// In daemon mode, we rely on the product registration from init().
	// The products variable is in cmd/opendb/products.go — we can't access it here.
	// Instead, we return empty opts; the connection manager will use config-based detection.
	return nil
}
