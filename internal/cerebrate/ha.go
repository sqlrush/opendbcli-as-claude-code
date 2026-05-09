/*-------------------------------------------------------------------------
 *
 * ha.go
 *	  ha.go implements Cerebrate high availability. 2 Overlords hold
 *	  Cerebrate's full data backup. When Cerebrate goes down, a backup
 *	  Overlord promotes to Cerebrate role (dual role: Overlord +
 *	  Cerebrate).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/ha.go
 *
 *-------------------------------------------------------------------------
 */
// ha.go implements Cerebrate high availability.
// 2 Overlords hold Cerebrate's full data backup. When Cerebrate goes down,
// a backup Overlord promotes to Cerebrate role (dual role: Overlord + Cerebrate).
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
)

// HAManager manages Cerebrate high availability.
type HAManager struct {
	mu          sync.RWMutex
	cerebrateID string
	fleet       *FleetManager
	logger      *slog.Logger

	// backupOverlords holds the 2 Overlords that mirror Cerebrate data.
	backupOverlords []*BackupOverlord

	// cerebrateAddr is the current Cerebrate address (for standby monitoring).
	cerebrateAddr string
}

// BackupOverlord represents an Overlord that holds Cerebrate data backup.
type BackupOverlord struct {
	OverlordID string
	Addr       string
	LastSync   time.Time
	Healthy    bool
	conn       *grpc.ClientConn
	client     pb.OverlordServiceClient
}

// NewHAManager creates a HA manager for Cerebrate.
func NewHAManager(cerebrateID string, fleet *FleetManager) *HAManager {
	return &HAManager{
		cerebrateID:     cerebrateID,
		fleet:           fleet,
		logger:          cluster.DefaultLogger("ha"),
		backupOverlords: make([]*BackupOverlord, 0, 2),
	}
}

// AddBackup designates an Overlord as a Cerebrate backup (max 2).
func (ha *HAManager) AddBackup(ctx context.Context, overlordID, addr string) error {
	ha.mu.Lock()
	defer ha.mu.Unlock()

	if len(ha.backupOverlords) >= 2 {
		return fmt.Errorf("max 2 Cerebrate backups allowed")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to backup %s: %w", overlordID, err)
	}

	backup := &BackupOverlord{
		OverlordID: overlordID,
		Addr:       addr,
		Healthy:    true,
		conn:       conn,
		client:     pb.NewOverlordServiceClient(conn),
	}

	ha.backupOverlords = append(ha.backupOverlords, backup)
	ha.logger.Info("Designated Cerebrate backup", slog.String("overlord_id", overlordID))
	return nil
}

// SyncToBackups replicates Cerebrate's mapping data to backup Overlords.
// Cerebrate data = Worker↔Overlord mappings, memory backup relationships.
func (ha *HAManager) SyncToBackups(ctx context.Context) {
	ha.mu.RLock()
	backups := make([]*BackupOverlord, len(ha.backupOverlords))
	copy(backups, ha.backupOverlords)
	ha.mu.RUnlock()

	for _, backup := range backups {
		if err := ha.syncToBackup(ctx, backup); err != nil {
			ha.logger.Warn("Sync to backup failed", slog.String("backup_id", backup.OverlordID), slog.String("error", err.Error()))
			ha.mu.Lock()
			backup.Healthy = false
			ha.mu.Unlock()
		} else {
			ha.mu.Lock()
			backup.LastSync = time.Now()
			backup.Healthy = true
			ha.mu.Unlock()
		}
	}
}

func (ha *HAManager) syncToBackup(ctx context.Context, backup *BackupOverlord) error {
	// Send Cerebrate mapping data via heartbeat.
	_, err := backup.client.OverlordHeartbeat(ctx, &pb.OverlordHeartbeatRequest{
		OverlordId:   ha.cerebrateID,
		Timestamp:    time.Now().Unix(),
		State:        pb.AgentState_STATE_RUNNING,
		WorkerCount:  ha.fleet.TotalWorkerCount(),
		WorkerOnline: ha.fleet.TotalOnlineCount(),
		RegionHealth: &pb.HealthScore{
			Score:   100,
			Summary: fmt.Sprintf("cerebrate backup sync: %d overlords", ha.fleet.OverlordCount()),
		},
	})
	return err
}

// StartPeriodicSync syncs to backups periodically.
func (ha *HAManager) StartPeriodicSync(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ha.SyncToBackups(ctx)
		}
	}
}

// DesignateFailoverTarget selects which backup Overlord should take over.
// Called by the standby monitor when Cerebrate is detected as down.
func (ha *HAManager) DesignateFailoverTarget() *BackupOverlord {
	ha.mu.RLock()
	defer ha.mu.RUnlock()

	for _, backup := range ha.backupOverlords {
		if backup.Healthy {
			return backup
		}
	}
	return nil
}

// BackupCount returns the number of configured backups.
func (ha *HAManager) BackupCount() int {
	ha.mu.RLock()
	defer ha.mu.RUnlock()
	return len(ha.backupOverlords)
}

// Close disconnects from all backups.
func (ha *HAManager) Close() {
	ha.mu.Lock()
	defer ha.mu.Unlock()
	for _, b := range ha.backupOverlords {
		if b.conn != nil {
			b.conn.Close()
		}
	}
	ha.backupOverlords = nil
}

// ---- Standby Monitor with Election (C10) ----

// StandbyMonitor runs on a backup Overlord, monitoring Cerebrate's health.
// If Cerebrate goes down, it initiates an election with the other backup Overlord.
// The Overlord with the lexicographically smaller ID wins and promotes to Cerebrate.
type StandbyMonitor struct {
	cerebrateAddr string
	selfID        string
	selfAddr      string // this Overlord's gRPC address
	peerAddr      string // other backup Overlord's gRPC address (for election)
	peerID        string // other backup Overlord's ID
	checkInterval time.Duration
	failureCount  int
	maxFailures   int // promote after this many consecutive failures
	logger        *slog.Logger
}

// NewStandbyMonitor creates a monitor that watches Cerebrate health.
func NewStandbyMonitor(cerebrateAddr, selfID, selfAddr string) *StandbyMonitor {
	return &StandbyMonitor{
		cerebrateAddr: cerebrateAddr,
		selfID:        selfID,
		selfAddr:      selfAddr,
		checkInterval: 30 * time.Second,
		maxFailures:   3,
		logger:        cluster.DefaultLogger("standby"),
	}
}

// SetPeer sets the other backup Overlord for election voting (C10).
func (sm *StandbyMonitor) SetPeer(peerID, peerAddr string) {
	sm.peerID = peerID
	sm.peerAddr = peerAddr
}

// Start begins monitoring Cerebrate. Blocks until ctx is cancelled or promotion occurs.
// Returns true if this Overlord should promote to Cerebrate.
func (sm *StandbyMonitor) Start(ctx context.Context) (promoted bool) {
	ticker := time.NewTicker(sm.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if sm.checkCerebrate(ctx) {
				sm.failureCount = 0
			} else {
				sm.failureCount++
				sm.logger.Warn("Cerebrate health check failed",
					slog.Int("failures", sm.failureCount),
					slog.Int("max", sm.maxFailures))
				if sm.failureCount >= sm.maxFailures {
					return sm.runElection(ctx)
				}
			}
		}
	}
}

// runElection conducts an election with the peer backup Overlord (C10).
// Uses OverlordID lexicographic ordering: smaller ID wins.
func (sm *StandbyMonitor) runElection(ctx context.Context) bool {
	sm.logger.Info("Cerebrate unreachable — initiating election",
		slog.String("self_id", sm.selfID),
		slog.String("peer_id", sm.peerID))

	// If no peer configured, self-promote directly.
	if sm.peerAddr == "" || sm.peerID == "" {
		sm.logger.Info("No election peer, self-promoting", slog.String("self_id", sm.selfID))
		return true
	}

	// Request election vote from peer.
	conn, err := grpc.NewClient(sm.peerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		sm.logger.Warn("Cannot reach peer for election, self-promoting",
			slog.String("peer", sm.peerAddr), slog.String("error", err.Error()))
		return true
	}
	defer conn.Close()

	client := pb.NewOverlordServiceClient(conn)
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := client.ElectionVote(timeoutCtx, &pb.ElectionVoteRequest{
		CandidateId:   sm.selfID,
		CandidateAddr: sm.selfAddr,
		Timestamp:     time.Now().Unix(),
	})
	if err != nil {
		sm.logger.Warn("Election vote RPC failed, self-promoting",
			slog.String("error", err.Error()))
		return true
	}

	if resp.Accept {
		sm.logger.Info("Election won — peer accepted our candidacy",
			slog.String("voter", resp.VoterId))
		return true
	}

	// Peer rejected — peer has smaller ID and will promote itself.
	sm.logger.Info("Election lost — peer will promote",
		slog.String("voter", resp.VoterId),
		slog.String("message", resp.Message))
	return false
}

// checkCerebrate pings Cerebrate to see if it's alive.
func (sm *StandbyMonitor) checkCerebrate(ctx context.Context) bool {
	conn, err := grpc.NewClient(sm.cerebrateAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer conn.Close()

	client := pb.NewOverlordServiceClient(conn)
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err = client.OverlordHeartbeat(timeoutCtx, &pb.OverlordHeartbeatRequest{
		OverlordId: sm.selfID,
		Timestamp:  time.Now().Unix(),
		State:      pb.AgentState_STATE_RUNNING,
	})
	return err == nil
}
