/*-------------------------------------------------------------------------
 *
 * cerebrate_client.go
 *	  cerebrate_client.go implements Overlord's gRPC client for
 *	  connecting to Cerebrate.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/cerebrate_client.go
 *
 *-------------------------------------------------------------------------
 */
// cerebrate_client.go implements Overlord's gRPC client for connecting to Cerebrate.
package overlord

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

// CerebrateClient manages Overlord's connection to Cerebrate.
type CerebrateClient struct {
	cerebrateAddr string
	overlordID    string
	region        string
	listenAddr    string
	conn          *grpc.ClientConn
	client        pb.OverlordServiceClient
	connected     bool
	logger        *slog.Logger
}

// NewCerebrateClient creates a client for connecting to Cerebrate.
func NewCerebrateClient(cerebrateAddr, overlordID, region, listenAddr string) *CerebrateClient {
	return &CerebrateClient{
		cerebrateAddr: cerebrateAddr,
		overlordID:    overlordID,
		region:        region,
		listenAddr:    listenAddr,
		logger:        cluster.DefaultLogger("cerebrate-client"),
	}
}

// Connect establishes gRPC connection and registers with Cerebrate.
func (cc *CerebrateClient) Connect(ctx context.Context, workerCapacity int32) error {
	if cc.cerebrateAddr == "" {
		return fmt.Errorf("no cerebrate address configured")
	}

	conn, err := grpc.NewClient(cc.cerebrateAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to cerebrate: %w", err)
	}
	cc.conn = conn
	cc.client = pb.NewOverlordServiceClient(conn)

	resp, err := cc.client.RegisterOverlord(ctx, &pb.RegisterOverlordRequest{
		OverlordId:     cc.overlordID,
		Hostname:       Hostname(),
		Region:         cc.region,
		ListenAddr:     cc.listenAddr,
		WorkerCapacity: workerCapacity,
	})
	if err != nil {
		cc.conn.Close()
		return fmt.Errorf("register with cerebrate: %w", err)
	}
	if !resp.Accepted {
		cc.conn.Close()
		return fmt.Errorf("registration rejected: %s", resp.Message)
	}

	cc.connected = true
	cc.logger.Info("Registered with Cerebrate", slog.String("addr", cc.cerebrateAddr))
	return nil
}

// StartHeartbeat sends periodic heartbeats to Cerebrate.
func (cc *CerebrateClient) StartHeartbeat(ctx context.Context, interval time.Duration, workerMgr *WorkerManager) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveFailures := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !cc.connected {
				cc.reconnect(ctx)
				continue
			}

			_, err := cc.client.OverlordHeartbeat(ctx, &pb.OverlordHeartbeatRequest{
				OverlordId:   cc.overlordID,
				Timestamp:    time.Now().Unix(),
				State:        pb.AgentState_STATE_RUNNING,
				WorkerCount:  int32(workerMgr.WorkerCount()),
				WorkerOnline: int32(workerMgr.OnlineCount()),
				RegionHealth: &pb.HealthScore{
					Score:   100,
					Summary: fmt.Sprintf("%d/%d workers online", workerMgr.OnlineCount(), workerMgr.WorkerCount()),
				},
			})
			if err != nil {
				consecutiveFailures++
				if consecutiveFailures >= 3 {
					cc.logger.Warn("Lost connection to Cerebrate")
					cc.connected = false
					consecutiveFailures = 0
				}
			} else {
				consecutiveFailures = 0
			}
		}
	}
}

func (cc *CerebrateClient) reconnect(ctx context.Context) {
	if cc.conn != nil {
		cc.conn.Close()
		cc.conn = nil
	}

	conn, err := grpc.NewClient(cc.cerebrateAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cc.logger.Error("Reconnect dial failed", slog.String("error", err.Error()))
		return
	}
	cc.conn = conn
	cc.client = pb.NewOverlordServiceClient(conn)

	resp, err := cc.client.RegisterOverlord(ctx, &pb.RegisterOverlordRequest{
		OverlordId:     cc.overlordID,
		Hostname:       Hostname(),
		Region:         cc.region,
		ListenAddr:     cc.listenAddr,
		WorkerCapacity: 200,
	})
	if err != nil {
		cc.logger.Error("Reconnect register failed", slog.String("error", err.Error()))
		cc.conn.Close()
		cc.conn = nil
		return
	}
	if !resp.Accepted {
		cc.logger.Warn("Reconnect rejected", slog.String("message", resp.Message))
		cc.conn.Close()
		cc.conn = nil
		return
	}

	cc.connected = true
	cc.logger.Info("Reconnected to Cerebrate")
}

// SendHeartbeatNow sends an immediate heartbeat to Cerebrate (called on Worker registration).
func (cc *CerebrateClient) SendHeartbeatNow(workerMgr *WorkerManager) {
	if !cc.connected || cc.client == nil {
		return
	}
	cc.client.OverlordHeartbeat(context.Background(), &pb.OverlordHeartbeatRequest{
		OverlordId:   cc.overlordID,
		Timestamp:    time.Now().Unix(),
		State:        pb.AgentState_STATE_RUNNING,
		WorkerCount:  int32(workerMgr.WorkerCount()),
		WorkerOnline: int32(workerMgr.OnlineCount()),
		RegionHealth: &pb.HealthScore{
			Score:   100,
			Summary: fmt.Sprintf("%d/%d workers online", workerMgr.OnlineCount(), workerMgr.WorkerCount()),
		},
	})
}

// IsConnected returns connection status.
func (cc *CerebrateClient) IsConnected() bool { return cc.connected }

// Close disconnects from Cerebrate.
func (cc *CerebrateClient) Close() {
	if cc.conn != nil {
		cc.conn.Close()
		cc.connected = false
	}
}
