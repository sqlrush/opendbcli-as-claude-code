/*-------------------------------------------------------------------------
 *
 * overlord_conn.go
 *	  Package cerebrate implements the Manager Agent (Cerebrate/脑虫).
 *	  Global fleet orchestration, cross-region trend analysis, policy
 *	  distribution, and Web UI dashboard.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/overlord_conn.go
 *
 *-------------------------------------------------------------------------
 */
// Package cerebrate implements the Manager Agent (Cerebrate/脑虫).
// Global fleet orchestration, cross-region trend analysis, policy distribution, and Web UI dashboard.
package cerebrate

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

// OverlordConnection holds the gRPC connection to a single Overlord.
type OverlordConnection struct {
	OverlordID  string
	Region      string
	Addr        string
	State       pb.AgentState
	Health      *pb.HealthScore
	WorkerCount int32
	OnlineCount int32
	Workers     []*pb.WorkerSummary // worker details (db_type, state, health)
	LastSeen    time.Time
	conn        *grpc.ClientConn
	client      pb.OverlordServiceClient
}

// FleetManager manages connections to all Overlords.
type FleetManager struct {
	mu          sync.RWMutex
	overlords   map[string]*OverlordConnection // overlordID → connection
	cerebrateID string
	logger      *slog.Logger
}

// NewFleetManager creates a new FleetManager.
func NewFleetManager(cerebrateID string) *FleetManager {
	return &FleetManager{
		overlords:   make(map[string]*OverlordConnection),
		cerebrateID: cerebrateID,
		logger:      cluster.DefaultLogger("cerebrate"),
	}
}

// AddOverlord connects to an Overlord at the given address.
func (fm *FleetManager) AddOverlord(ctx context.Context, overlordID, region, addr string) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	if _, exists := fm.overlords[overlordID]; exists {
		return fmt.Errorf("overlord %s already connected", overlordID)
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to overlord %s at %s: %w", overlordID, addr, err)
	}

	client := pb.NewOverlordServiceClient(conn)

	oc := &OverlordConnection{
		OverlordID: overlordID,
		Region:     region,
		Addr:       addr,
		State:      pb.AgentState_STATE_UNKNOWN,
		LastSeen:   time.Now(),
		conn:       conn,
		client:     client,
	}

	fm.overlords[overlordID] = oc
	fm.logger.Info("Connected to overlord", slog.String("overlord_id", overlordID), slog.String("region", region), slog.String("addr", addr))
	return nil
}

// RemoveOverlord disconnects from an Overlord.
func (fm *FleetManager) RemoveOverlord(overlordID string) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	oc, exists := fm.overlords[overlordID]
	if !exists {
		return fmt.Errorf("overlord %s not found", overlordID)
	}

	if oc.conn != nil {
		oc.conn.Close()
	}
	delete(fm.overlords, overlordID)
	return nil
}

// GetRegionStatus pulls status from a specific Overlord.
func (fm *FleetManager) GetRegionStatus(ctx context.Context, overlordID string) (*pb.RegionStatusResponse, error) {
	fm.mu.RLock()
	oc, exists := fm.overlords[overlordID]
	fm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("overlord %s not found", overlordID)
	}

	resp, err := oc.client.GetRegionStatus(ctx, &pb.RegionStatusRequest{OverlordId: overlordID})
	if err != nil {
		return nil, fmt.Errorf("get region status from %s: %w", overlordID, err)
	}

	// Update cached state.
	fm.mu.Lock()
	oc.WorkerCount = resp.WorkerCount
	oc.OnlineCount = resp.WorkerOnline
	oc.Health = resp.RegionHealth
	oc.Workers = resp.Workers
	oc.State = pb.AgentState_STATE_RUNNING
	oc.LastSeen = time.Now()
	fm.mu.Unlock()

	return resp, nil
}

// GetAllRegionStatus pulls status from all Overlords.
func (fm *FleetManager) GetAllRegionStatus(ctx context.Context) []*pb.RegionStatusResponse {
	fm.mu.RLock()
	ids := make([]string, 0, len(fm.overlords))
	for id := range fm.overlords {
		ids = append(ids, id)
	}
	fm.mu.RUnlock()

	results := make([]*pb.RegionStatusResponse, 0, len(ids))
	for _, id := range ids {
		resp, err := fm.GetRegionStatus(ctx, id)
		if err != nil {
			fm.logger.Warn("Failed to get status", slog.String("overlord_id", id), slog.String("error", err.Error()))
			continue
		}
		results = append(results, resp)
	}
	return results
}

// PushPolicyToAll distributes a policy to all Overlords.
func (fm *FleetManager) PushPolicyToAll(ctx context.Context, req *pb.PolicyRequest) map[string]*pb.PolicyResponse {
	fm.mu.RLock()
	ids := make([]string, 0, len(fm.overlords))
	for id := range fm.overlords {
		ids = append(ids, id)
	}
	fm.mu.RUnlock()

	results := make(map[string]*pb.PolicyResponse)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(1)
		oid := id
		odberr.SafeGo(odberr.ErrClusterRPC, func() {
			defer wg.Done()

			fm.mu.RLock()
			oc := fm.overlords[oid]
			fm.mu.RUnlock()

			resp, err := oc.client.PushPolicy(ctx, req)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[oid] = &pb.PolicyResponse{Success: false, Message: err.Error()}
			} else {
				results[oid] = resp
			}
		})
	}

	wg.Wait()
	return results
}

// OverlordCount returns the number of connected Overlords.
func (fm *FleetManager) OverlordCount() int {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	return len(fm.overlords)
}

// TotalWorkerCount returns the total number of Workers across all Overlords.
func (fm *FleetManager) TotalWorkerCount() int32 {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	var total int32
	for _, oc := range fm.overlords {
		total += oc.WorkerCount
	}
	return total
}

// TotalOnlineCount returns the total number of online Workers across all Overlords.
func (fm *FleetManager) TotalOnlineCount() int32 {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	var total int32
	for _, oc := range fm.overlords {
		total += oc.OnlineCount
	}
	return total
}

// GetCachedStatus returns cached status for all Overlords (no network calls).
// Uses data received from heartbeats — fast, no timeouts.
func (fm *FleetManager) GetCachedStatus() []map[string]any {
	fm.mu.RLock()
	defer fm.mu.RUnlock()

	result := make([]map[string]any, 0, len(fm.overlords))
	for _, oc := range fm.overlords {
		health := map[string]any{"score": int32(0), "summary": "unknown"}
		if oc.Health != nil {
			health = map[string]any{"score": oc.Health.Score, "summary": oc.Health.Summary}
		}
		// Build db_groups and workers list from cached WorkerSummary.
		dbGroups := make(map[string]int)
		workers := make([]map[string]any, 0, len(oc.Workers))
		for _, w := range oc.Workers {
			dbGroups[w.DbType]++
			healthScore := int32(0)
			if w.Health != nil {
				healthScore = w.Health.Score
			}
			workers = append(workers, map[string]any{
				"id":      w.WorkerId,
				"db_type": w.DbType,
				"state":   w.State.String(),
				"health":  healthScore,
			})
		}

		result = append(result, map[string]any{
			"overlord_id":  oc.OverlordID,
			"region":       oc.Region,
			"addr":         oc.Addr,
			"state":        oc.State.String(),
			"worker_count": oc.WorkerCount,
			"online":       oc.OnlineCount,
			"health":       health,
			"last_seen":    oc.LastSeen.Format(time.RFC3339),
			"db_groups":    dbGroups,
			"workers":      workers,
		})
	}
	return result
}

// OverlordIDs returns all connected Overlord IDs.
func (fm *FleetManager) OverlordIDs() []string {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	ids := make([]string, 0, len(fm.overlords))
	for id := range fm.overlords {
		ids = append(ids, id)
	}
	return ids
}

// OverlordHeartbeatTimeout is the duration after which an Overlord is considered offline.
const OverlordHeartbeatTimeout = 90 * time.Second

// CheckOverlordHeartbeatTimeouts marks Overlords as offline if their last heartbeat exceeds the timeout.
// Returns IDs of newly-stale Overlords.
func (fm *FleetManager) CheckOverlordHeartbeatTimeouts(timeout time.Duration) []string {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	now := time.Now()
	var staleIDs []string
	for id, oc := range fm.overlords {
		if oc.State == pb.AgentState_STATE_RUNNING && now.Sub(oc.LastSeen) > timeout {
			oc.State = pb.AgentState_STATE_OFFLINE
			staleIDs = append(staleIDs, id)
			fm.logger.Warn("Overlord heartbeat timeout, marking offline",
				slog.String("overlord_id", id),
				slog.String("region", oc.Region),
				slog.Duration("last_seen_ago", now.Sub(oc.LastSeen)))
		}
	}
	return staleIDs
}

// findHealthyOverlord returns the ID of a healthy Overlord (excluding the given ID).
func (fm *FleetManager) findHealthyOverlord(excludeID string) string {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	for id, oc := range fm.overlords {
		if id != excludeID && oc.State == pb.AgentState_STATE_RUNNING {
			return id
		}
	}
	return ""
}

// StartStatusPoller periodically pulls region status from all Overlords
// and checks for heartbeat timeouts to trigger Worker redirect.
func (fm *FleetManager) StartStatusPoller(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check for Overlord heartbeat timeouts first.
			staleIDs := fm.CheckOverlordHeartbeatTimeouts(OverlordHeartbeatTimeout)
			for _, deadID := range staleIDs {
				targetID := fm.findHealthyOverlord(deadID)
				if targetID == "" {
					fm.logger.Error("No healthy Overlord available for redirect",
						slog.String("dead_overlord", deadID))
					continue
				}
				fm.logger.Info("Initiating Worker redirect from dead Overlord",
					slog.String("dead", deadID),
					slog.String("target", targetID))
				redirected, failed := fm.RedirectWorkersFromDeadOverlord(ctx, deadID, targetID)
				fm.logger.Info("Redirect result",
					slog.String("dead", deadID),
					slog.Int("redirected", redirected),
					slog.Int("failed", failed))
			}
			fm.GetAllRegionStatus(ctx)
		}
	}
}

// RedirectWorkersFromDeadOverlord redirects Workers from a dead Overlord to a healthy one (C09).
// It connects to each Worker's gRPC server and calls RedirectWorker.
func (fm *FleetManager) RedirectWorkersFromDeadOverlord(ctx context.Context, deadOverlordID, targetOverlordID string) (redirected, failed int) {
	fm.mu.RLock()
	deadOC, deadExists := fm.overlords[deadOverlordID]
	targetOC, targetExists := fm.overlords[targetOverlordID]
	fm.mu.RUnlock()

	if !deadExists {
		fm.logger.Warn("Dead overlord not found", slog.String("id", deadOverlordID))
		return 0, 0
	}
	if !targetExists {
		fm.logger.Warn("Target overlord not found", slog.String("id", targetOverlordID))
		return 0, 0
	}

	workers := deadOC.Workers
	if len(workers) == 0 {
		fm.logger.Info("No workers to redirect from dead overlord", slog.String("id", deadOverlordID))
		return 0, 0
	}

	fm.logger.Info("Redirecting workers from dead overlord",
		slog.String("dead", deadOverlordID),
		slog.String("target", targetOverlordID),
		slog.Int("workers", len(workers)))

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, w := range workers {
		if w.Addr == "" {
			mu.Lock()
			failed++
			mu.Unlock()
			fm.logger.Warn("Worker has no addr, cannot redirect", slog.String("worker_id", w.WorkerId))
			continue
		}
		wg.Add(1)
		workerAddr := w.Addr
		workerID := w.WorkerId
		odberr.SafeGo(odberr.ErrClusterRPC, func() {
			defer wg.Done()

			// Connect to Worker's gRPC server.
			conn, err := grpc.NewClient(workerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				fm.logger.Error("Cannot connect to worker for redirect",
					slog.String("worker_id", workerID), slog.String("error", err.Error()))
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			defer conn.Close()

			client := pb.NewAgentServiceClient(conn)
			resp, err := client.RedirectWorker(ctx, &pb.RedirectWorkerRequest{
				NewOverlordAddr: targetOC.Addr,
				NewOverlordId:   targetOC.OverlordID,
				Reason:          fmt.Sprintf("overlord %s is down", deadOverlordID),
			})
			if err != nil || !resp.Success {
				errMsg := ""
				if err != nil {
					errMsg = err.Error()
				} else {
					errMsg = resp.Message
				}
				fm.logger.Error("Worker redirect failed",
					slog.String("worker_id", workerID), slog.String("error", errMsg))
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			fm.logger.Info("Worker redirected successfully",
				slog.String("worker_id", workerID),
				slog.String("target", targetOC.OverlordID))
			mu.Lock()
			redirected++
			mu.Unlock()
		})
	}

	wg.Wait()
	fm.logger.Info("Worker redirect complete",
		slog.String("dead_overlord", deadOverlordID),
		slog.Int("redirected", redirected),
		slog.Int("failed", failed))
	return
}

// Close disconnects from all Overlords.
func (fm *FleetManager) Close() {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	for _, oc := range fm.overlords {
		if oc.conn != nil {
			oc.conn.Close()
		}
	}
	fm.overlords = make(map[string]*OverlordConnection)
}
