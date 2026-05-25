/*-------------------------------------------------------------------------
 *
 * overlord_client.go
 *	  overlord_client.go implements the Worker's gRPC client for
 *	  connecting to Overlord. Handles registration, heartbeat, memory
 *	  sync, and event reporting.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/overlord_client.go
 *
 *-------------------------------------------------------------------------
 */
// overlord_client.go implements the Worker's gRPC client for connecting to Overlord.
// Handles registration, heartbeat, memory sync, and event reporting.
package drone

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// OverlordClient manages the Worker's connection to its Overlord.
type OverlordClient struct {
	overlordAddr string
	workerID     string
	hostname     string
	dbType       string
	instance     string
	listenAddr   string
	conn         *grpc.ClientConn
	client       pb.AgentServiceClient
	connected    bool
	logger       *slog.Logger
}

// NewOverlordClient creates a client for connecting to Overlord.
func NewOverlordClient(overlordAddr, workerID, hostname, dbType, instance, listenAddr string) *OverlordClient {
	return &OverlordClient{
		overlordAddr: overlordAddr,
		workerID:     workerID,
		hostname:     hostname,
		dbType:       dbType,
		instance:     instance,
		listenAddr:   listenAddr,
		logger:       cluster.DefaultLogger("overlord-client"),
	}
}

// Connect establishes gRPC connection to Overlord and registers.
func (oc *OverlordClient) Connect(ctx context.Context, joinToken string) error {
	if oc.overlordAddr == "" {
		return fmt.Errorf("no overlord address configured")
	}

	conn, err := grpc.NewClient(oc.overlordAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to overlord %s: %w", oc.overlordAddr, err)
	}
	oc.conn = conn
	oc.client = pb.NewAgentServiceClient(conn)

	// Register with Overlord.
	resp, err := oc.client.Register(ctx, &pb.RegisterRequest{
		WorkerId:     oc.workerID,
		Hostname:     oc.hostname,
		DbType:       oc.dbType,
		InstanceName: oc.instance,
		ListenAddr:   oc.listenAddr,
		JoinToken:    joinToken,
	})
	if err != nil {
		oc.conn.Close()
		return fmt.Errorf("register with overlord: %w", err)
	}
	if !resp.Accepted {
		oc.conn.Close()
		return fmt.Errorf("registration rejected: %s", resp.Message)
	}

	oc.connected = true
	oc.logger.Info("Registered with Overlord", slog.String("addr", oc.overlordAddr))
	return nil
}

// StartHeartbeat sends periodic heartbeats to Overlord with automatic reconnection.
func (oc *OverlordClient) StartHeartbeat(ctx context.Context, interval time.Duration, healthFn func() (int32, string)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveFailures := 0
	maxFailures := 3 // reconnect after 3 consecutive failures

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !oc.connected {
				// Try reconnecting.
				oc.reconnect(ctx)
				continue
			}

			score, summary := healthFn()
			_, err := oc.client.Heartbeat(ctx, &pb.HeartbeatRequest{
				WorkerId:  oc.workerID,
				Timestamp: time.Now().Unix(),
				State:     pb.AgentState_STATE_RUNNING,
				Health:    &pb.HealthScore{Score: score, Summary: summary},
			})
			if err != nil {
				consecutiveFailures++
				oc.logger.Warn("Heartbeat failed", slog.Int("failures", consecutiveFailures), slog.Int("max", maxFailures), slog.String("error", err.Error()))
				if consecutiveFailures >= maxFailures {
					oc.logger.Warn("Connection lost, will attempt reconnection")
					oc.connected = false
					consecutiveFailures = 0
				}
			} else {
				consecutiveFailures = 0
			}
		}
	}
}

// reconnect attempts to re-establish connection to Overlord.
func (oc *OverlordClient) reconnect(ctx context.Context) {
	oc.logger.Info("Reconnecting to Overlord", slog.String("addr", oc.overlordAddr))

	// Close old connection.
	if oc.conn != nil {
		oc.conn.Close()
		oc.conn = nil
	}

	conn, err := grpc.NewClient(oc.overlordAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		oc.logger.Error("Reconnect failed", slog.String("error", err.Error()))
		return
	}
	oc.conn = conn
	oc.client = pb.NewAgentServiceClient(conn)

	// Re-register.
	resp, err := oc.client.Register(ctx, &pb.RegisterRequest{
		WorkerId:     oc.workerID,
		Hostname:     oc.hostname,
		DbType:       oc.dbType,
		InstanceName: oc.instance,
		ListenAddr:   oc.listenAddr,
	})
	if err != nil {
		oc.logger.Error("Re-registration failed", slog.String("error", err.Error()))
		oc.conn.Close()
		oc.conn = nil
		return
	}
	if !resp.Accepted {
		oc.logger.Warn("Re-registration rejected", slog.String("message", resp.Message))
		return
	}

	oc.connected = true
	oc.logger.Info("Reconnected and re-registered successfully")
}

// SendMemorySync sends a memory file update to Overlord via event stream.
func (oc *OverlordClient) SendMemorySync(ctx context.Context, instance, fileName string, content []byte, action string) error {
	if !oc.connected {
		return fmt.Errorf("not connected to overlord")
	}

	// TODO: use EventStream for memory sync instead of unary RPC.
	// For now, use PushCommand as a workaround.
	_, err := oc.client.PushCommand(ctx, &pb.CommandRequest{
		CommandId:   fmt.Sprintf("memsync-%d", time.Now().UnixMilli()),
		CommandType: "memory_sync",
		Payload:     fmt.Sprintf(`{"instance":"%s","file":"%s","action":"%s","size":%d}`, instance, fileName, action, len(content)),
	})
	return err
}

// IsConnected returns whether the client is connected to Overlord.
func (oc *OverlordClient) IsConnected() bool {
	return oc.connected
}

// RedirectTo disconnects from the current Overlord and connects to a new one (C09).
func (oc *OverlordClient) RedirectTo(ctx context.Context, newAddr, newID, reason string) error {
	oc.logger.Info("Redirecting to new Overlord",
		slog.String("old_addr", oc.overlordAddr),
		slog.String("new_addr", newAddr),
		slog.String("reason", reason))

	// Close existing connection.
	if oc.conn != nil {
		oc.conn.Close()
		oc.conn = nil
	}
	oc.connected = false

	// Update target address.
	oc.overlordAddr = newAddr

	// Connect to new Overlord.
	conn, err := grpc.NewClient(newAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to new overlord %s: %w", newAddr, err)
	}
	oc.conn = conn
	oc.client = pb.NewAgentServiceClient(conn)

	// Register with new Overlord.
	resp, err := oc.client.Register(ctx, &pb.RegisterRequest{
		WorkerId:     oc.workerID,
		Hostname:     oc.hostname,
		DbType:       oc.dbType,
		InstanceName: oc.instance,
		ListenAddr:   oc.listenAddr,
	})
	if err != nil {
		oc.conn.Close()
		oc.conn = nil
		return fmt.Errorf("register with new overlord: %w", err)
	}
	if !resp.Accepted {
		oc.conn.Close()
		oc.conn = nil
		return fmt.Errorf("registration rejected by new overlord: %s", resp.Message)
	}

	oc.connected = true
	oc.logger.Info("Redirect successful — now connected to new Overlord", slog.String("addr", newAddr))
	return nil
}

// Close disconnects from Overlord.
func (oc *OverlordClient) Close() {
	if oc.conn != nil {
		oc.conn.Close()
		oc.connected = false
	}
}
