/*-------------------------------------------------------------------------
 *
 * agent.go
 *	  RunAgent dispatches to the appropriate agent subcommand.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/agent.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
	"github.com/sqlrush/opendb/internal/odberr"
)

// RunAgent dispatches to the appropriate agent subcommand.
func RunAgent(args AgentArgs) error {
	switch args.Subcmd {
	case SubcmdStart:
		return runStart(args)
	case SubcmdStop:
		return runStop(args)
	case SubcmdStatus:
		return runStatus(args)
	default:
		return fmt.Errorf("unknown subcommand: %s", args.Subcmd)
	}
}

func runStart(args AgentArgs) error {
	logger := cluster.DefaultLogger("agent")

	// Check if already running.
	pid, err := readPID(args.Role)
	if err != nil {
		return fmt.Errorf("check existing agent: %w", err)
	}
	if pid > 0 && isProcessRunning(pid) {
		return fmt.Errorf("agent already running (PID %d). Use 'opendb agent stop' first", pid)
	}

	// Write PID file.
	if err := writePID(args.Role); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}
	defer removePID(args.Role)

	logger.Info("OpenDB Agent starting", slog.String("role", args.Role), slog.Int("pid", os.Getpid()))

	// Setup signal handling for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				logger.Info("Received SIGHUP, reloading configuration")
				// #7: Config reload — re-read config.yaml and hot-apply changes.
				// Currently logs the event; full reload wired in agent_mode.go.
			case syscall.SIGINT, syscall.SIGTERM:
				logger.Info("Received signal, shutting down gracefully", slog.String("signal", sig.String()))
				cancel()
				return
			}
		}
	})

	// Initialize daemon services and run autonomy loop.
	ac, err := initDaemonServices(args)
	if err != nil {
		// If service init fails, fall back to basic heartbeat mode.
		logger.Warn("Service init failed, falling back to heartbeat mode", slog.String("error", err.Error()))
		return runHeartbeatLoop(ctx, args)
	}
	defer ac.AuditLogger.Close()

	loop := NewAutonomyLoop(*ac)

	// Product-specific Sentinel and Diagnose functions will be wired in
	// by cmd/opendb/ product registration. For now, run the base loop.
	return loop.Run(ctx)
}

// runHeartbeatLoop is a minimal fallback loop when full services can't initialize.
func runHeartbeatLoop(ctx context.Context, args AgentArgs) error {
	logger := cluster.DefaultLogger("agent")
	logger.Info("Agent running in heartbeat mode", slog.String("role", args.Role))

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Agent stopped")
			return nil
		case <-ticker.C:
			logger.Debug("Agent heartbeat", slog.String("role", args.Role))
		}
	}
}

func runStop(args AgentArgs) error {
	logger := cluster.DefaultLogger("agent")
	pid, err := readPID(args.Role)
	if err != nil {
		return fmt.Errorf("read PID: %w", err)
	}
	if pid == 0 {
		logger.Info("No agent running")
		return nil
	}
	if !isProcessRunning(pid) {
		logger.Info("Agent is not running, cleaning up PID file", slog.Int("pid", pid))
		removePID(args.Role)
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", pid, err)
	}
	logger.Info("Sent SIGTERM to agent, waiting for shutdown", slog.Int("pid", pid))

	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		if !isProcessRunning(pid) {
			logger.Info("Agent stopped")
			removePID(args.Role)
			return nil
		}
	}

	return fmt.Errorf("agent (PID %d) did not stop within 10 seconds", pid)
}

func runStatus(args AgentArgs) error {
	logger := cluster.DefaultLogger("agent")
	pid, err := readPID(args.Role)
	if err != nil {
		return fmt.Errorf("read PID: %w", err)
	}
	if pid == 0 {
		logger.Info("Agent status", slog.String("role", args.Role), slog.String("status", "not running"))
		return nil
	}
	if !isProcessRunning(pid) {
		// ISSUE-007: Clean up stale PID file automatically.
		logger.Info("Agent status: stale PID file detected, cleaning up",
			slog.String("role", args.Role), slog.Int("stale_pid", pid))
		removePID(args.Role)
		return nil
	}
	logger.Info("Agent status", slog.String("role", args.Role), slog.String("status", "running"), slog.Int("pid", pid))
	return nil
}
