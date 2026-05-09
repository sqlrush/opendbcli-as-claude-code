/*-------------------------------------------------------------------------
 *
 * worker_conn.go
 *	  Package overlord implements the Memory Agent (Overlord/王虫).
 *	  Regional coordination, cross-node operations, worker memory
 *	  management, and direct database access.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/worker_conn.go
 *
 *-------------------------------------------------------------------------
 */
// Package overlord implements the Memory Agent (Overlord/王虫).
// Regional coordination, cross-node operations, worker memory management, and direct database access.
package overlord

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/odberr"
)

// WorkerConnection holds the gRPC connection and client to a single Worker.
type WorkerConnection struct {
	WorkerID   string
	Addr       string
	DBType     string
	Instance   string
	State      pb.AgentState
	Health     *pb.HealthScore
	LastSeen   time.Time
	conn       *grpc.ClientConn
	client     pb.AgentServiceClient
	eventStream pb.AgentService_EventStreamClient
}

// WorkerManager manages connections to all Workers in a region.
type WorkerManager struct {
	mu              sync.RWMutex
	workers         map[string]*WorkerConnection // workerID → connection
	overlordID      string
	region          string
	logger          *slog.Logger
	onWorkerChanged func() // callback when Worker state changes (ISSUE-006)
}

// NewWorkerManager creates a new WorkerManager for the given region.
func NewWorkerManager(overlordID, region string) *WorkerManager {
	return &WorkerManager{
		workers:    make(map[string]*WorkerConnection),
		overlordID: overlordID,
		region:     region,
		logger:     cluster.DefaultLogger("overlord"),
	}
}

// AddWorker connects to a Worker at the given address.
func (wm *WorkerManager) AddWorker(ctx context.Context, workerID, addr string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.workers[workerID]; exists {
		return fmt.Errorf("worker %s already connected", workerID)
	}

	// Connect to Worker gRPC server.
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to worker %s at %s: %w", workerID, addr, err)
	}

	client := pb.NewAgentServiceClient(conn)

	wc := &WorkerConnection{
		WorkerID: workerID,
		Addr:     addr,
		State:    pb.AgentState_STATE_UNKNOWN,
		LastSeen: time.Now(),
		conn:     conn,
		client:   client,
	}

	wm.workers[workerID] = wc
	wm.logger.Info("Connected to worker", slog.String("worker_id", workerID), slog.String("addr", addr))
	return nil
}

// RemoveWorker disconnects from a Worker.
func (wm *WorkerManager) RemoveWorker(workerID string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wc, exists := wm.workers[workerID]
	if !exists {
		return fmt.Errorf("worker %s not found", workerID)
	}

	if wc.conn != nil {
		wc.conn.Close()
	}
	delete(wm.workers, workerID)
	wm.logger.Info("Disconnected worker", slog.String("worker_id", workerID))
	return nil
}

// GetWorkerStatus pulls status from a specific Worker.
func (wm *WorkerManager) GetWorkerStatus(ctx context.Context, workerID string) (*pb.StatusResponse, error) {
	wm.mu.RLock()
	wc, exists := wm.workers[workerID]
	wm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("worker %s not found", workerID)
	}

	resp, err := wc.client.GetStatus(ctx, &pb.StatusRequest{WorkerId: workerID})
	if err != nil {
		return nil, fmt.Errorf("get status from %s: %w", workerID, err)
	}

	// Update cached state.
	wm.mu.Lock()
	wc.State = resp.State
	wc.Health = resp.Health
	wc.DBType = resp.DbType
	wc.Instance = resp.InstanceName
	wc.LastSeen = time.Now()
	wm.mu.Unlock()

	return resp, nil
}

// GetAllWorkerStatus pulls status from all Workers.
// Workers that fail to respond are marked offline (ISSUE-006 fix).
func (wm *WorkerManager) GetAllWorkerStatus(ctx context.Context) []*pb.StatusResponse {
	wm.mu.RLock()
	ids := make([]string, 0, len(wm.workers))
	for id := range wm.workers {
		ids = append(ids, id)
	}
	wm.mu.RUnlock()

	results := make([]*pb.StatusResponse, 0, len(ids))
	for _, id := range ids {
		resp, err := wm.GetWorkerStatus(ctx, id)
		if err != nil {
			wm.logger.Warn("Failed to get status from worker, marking offline",
				slog.String("worker_id", id), slog.String("error", err.Error()))
			wm.markWorkerOffline(id)
			continue
		}
		results = append(results, resp)
	}
	return results
}

// markWorkerOffline sets a Worker's state to STATE_STOPPED (ISSUE-006 fix).
func (wm *WorkerManager) markWorkerOffline(workerID string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if wc, exists := wm.workers[workerID]; exists {
		if wc.State != pb.AgentState_STATE_OFFLINE {
			wc.State = pb.AgentState_STATE_OFFLINE
			wm.logger.Info("Worker marked offline", slog.String("worker_id", workerID))
		}
	}
}

// CheckHeartbeatTimeouts marks Workers as offline if their last heartbeat exceeds the timeout (ISSUE-006 fix).
func (wm *WorkerManager) CheckHeartbeatTimeouts(timeout time.Duration) []string {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	now := time.Now()
	var staleIDs []string
	for id, wc := range wm.workers {
		if wc.State == pb.AgentState_STATE_RUNNING && now.Sub(wc.LastSeen) > timeout {
			wc.State = pb.AgentState_STATE_OFFLINE
			staleIDs = append(staleIDs, id)
			wm.logger.Warn("Worker heartbeat timeout, marking offline",
				slog.String("worker_id", id),
				slog.Duration("last_seen_ago", now.Sub(wc.LastSeen)))
		}
	}
	return staleIDs
}

// PushCommand sends a command to a specific Worker.
func (wm *WorkerManager) PushCommand(ctx context.Context, workerID string, cmd *pb.CommandRequest) (*pb.CommandResponse, error) {
	wm.mu.RLock()
	wc, exists := wm.workers[workerID]
	wm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("worker %s not found", workerID)
	}

	return wc.client.PushCommand(ctx, cmd)
}

// BroadcastCommand sends a command to multiple Workers.
func (wm *WorkerManager) BroadcastCommand(ctx context.Context, workerIDs []string, cmd *pb.CommandRequest) map[string]*pb.CommandResponse {
	results := make(map[string]*pb.CommandResponse)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, id := range workerIDs {
		wg.Add(1)
		wid := id
		odberr.SafeGo(odberr.ErrClusterRPC, func() {
			defer wg.Done()
			resp, err := wm.PushCommand(ctx, wid, cmd)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[wid] = &pb.CommandResponse{
					CommandId:    cmd.CommandId,
					Success:      false,
					ErrorMessage: err.Error(),
				}
			} else {
				results[wid] = resp
			}
		})
	}

	wg.Wait()
	return results
}

// WorkerCount returns the number of connected Workers.
func (wm *WorkerManager) WorkerCount() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return len(wm.workers)
}

// OnlineCount returns the number of Workers in running state.
func (wm *WorkerManager) OnlineCount() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	count := 0
	for _, wc := range wm.workers {
		if wc.State == pb.AgentState_STATE_RUNNING {
			count++
		}
	}
	return count
}

// WorkerIDs returns all connected Worker IDs.
func (wm *WorkerManager) WorkerIDs() []string {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	ids := make([]string, 0, len(wm.workers))
	for id := range wm.workers {
		ids = append(ids, id)
	}
	return ids
}

// GetWorkerAddr returns the gRPC address for a specific Worker.
func (wm *WorkerManager) GetWorkerAddr(workerID string) string {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	if wc, exists := wm.workers[workerID]; exists {
		return wc.Addr
	}
	return ""
}

// HeartbeatTimeout is the duration after which a Worker is considered offline if no heartbeat received.
const HeartbeatTimeout = 90 * time.Second

// StartStatusPoller periodically pulls status from all Workers and checks heartbeat timeouts.
func (wm *WorkerManager) StartStatusPoller(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check for heartbeat timeouts first (ISSUE-006 fix).
			staleIDs := wm.CheckHeartbeatTimeouts(HeartbeatTimeout)
			if len(staleIDs) > 0 {
				wm.logger.Info("Detected stale workers", slog.Int("count", len(staleIDs)))
				if wm.onWorkerChanged != nil {
					wm.onWorkerChanged()
				}
			}
			wm.GetAllWorkerStatus(ctx)
		}
	}
}

// SetOnWorkerChanged sets a callback triggered when Worker state changes (ISSUE-006 fix).
func (wm *WorkerManager) SetOnWorkerChanged(fn func()) {
	wm.onWorkerChanged = fn
}

// Close disconnects from all Workers.
func (wm *WorkerManager) Close() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	for _, wc := range wm.workers {
		if wc.conn != nil {
			wc.conn.Close()
		}
	}
	wm.workers = make(map[string]*WorkerConnection)
}
