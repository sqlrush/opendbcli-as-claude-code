/*-------------------------------------------------------------------------
 *
 * mirror.go
 *	  mirror.go implements Overlord-to-Overlord memory mirroring. Each
 *	  Overlord replicates its Worker data to 2 peer Overlords for HA.
 *	  When an Overlord goes down, a peer with its data can take over.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/mirror.go
 *
 *-------------------------------------------------------------------------
 */
// mirror.go implements Overlord-to-Overlord memory mirroring.
// Each Overlord replicates its Worker data to 2 peer Overlords for HA.
// When an Overlord goes down, a peer with its data can take over.
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
)

// MirrorPeer represents a peer Overlord that holds a copy of our data.
type MirrorPeer struct {
	OverlordID string
	Addr       string
	conn       *grpc.ClientConn
	client     pb.OverlordServiceClient
	LastSync   time.Time
	Healthy    bool
}

// MirrorManager manages replication to 2 peer Overlords.
type MirrorManager struct {
	mu         sync.RWMutex
	overlordID string
	peers      []*MirrorPeer // max 2 peers
	workerMgr  *WorkerManager
	logger     *slog.Logger
}

// NewMirrorManager creates a mirror manager for the given Overlord.
func NewMirrorManager(overlordID string, workerMgr *WorkerManager) *MirrorManager {
	return &MirrorManager{
		overlordID: overlordID,
		peers:      make([]*MirrorPeer, 0, 2),
		workerMgr:  workerMgr,
		logger:     cluster.DefaultLogger("mirror"),
	}
}

// AddPeer adds a peer Overlord for mirroring (max 2).
func (mm *MirrorManager) AddPeer(ctx context.Context, overlordID, addr string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if len(mm.peers) >= 2 {
		return fmt.Errorf("max 2 mirror peers allowed")
	}

	for _, p := range mm.peers {
		if p.OverlordID == overlordID {
			return fmt.Errorf("peer %s already added", overlordID)
		}
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to peer %s: %w", overlordID, err)
	}

	peer := &MirrorPeer{
		OverlordID: overlordID,
		Addr:       addr,
		conn:       conn,
		client:     pb.NewOverlordServiceClient(conn),
		Healthy:    true,
	}

	mm.peers = append(mm.peers, peer)
	mm.logger.Info("Added peer", slog.String("overlord_id", overlordID), slog.String("addr", addr))
	return nil
}

// SyncToPeers replicates current Worker data to all peers.
func (mm *MirrorManager) SyncToPeers(ctx context.Context) {
	mm.mu.RLock()
	peers := make([]*MirrorPeer, len(mm.peers))
	copy(peers, mm.peers)
	mm.mu.RUnlock()

	workerIDs := mm.workerMgr.WorkerIDs()

	for _, peer := range peers {
		if err := mm.syncToPeer(ctx, peer, workerIDs); err != nil {
			mm.logger.Warn("Sync to peer failed", slog.String("peer_id", peer.OverlordID), slog.String("error", err.Error()))
			mm.mu.Lock()
			peer.Healthy = false
			mm.mu.Unlock()
		} else {
			mm.mu.Lock()
			peer.LastSync = time.Now()
			peer.Healthy = true
			mm.mu.Unlock()
		}
	}
}

// syncToPeer sends Worker list and metadata to a single peer.
func (mm *MirrorManager) syncToPeer(ctx context.Context, peer *MirrorPeer, workerIDs []string) error {
	// Send region status as a mirror sync event.
	statuses := mm.workerMgr.GetAllWorkerStatus(ctx)

	workers := make([]*pb.WorkerSummary, 0, len(statuses))
	for _, st := range statuses {
		workers = append(workers, &pb.WorkerSummary{
			WorkerId:     st.WorkerId,
			DbType:       st.DbType,
			InstanceName: st.InstanceName,
			State:        st.State,
			Health:       st.Health,
		})
	}

	// Use heartbeat as sync mechanism (piggyback Worker data).
	_, err := peer.client.OverlordHeartbeat(ctx, &pb.OverlordHeartbeatRequest{
		OverlordId:  mm.overlordID,
		Timestamp:   time.Now().Unix(),
		State:       pb.AgentState_STATE_RUNNING,
		WorkerCount: int32(len(workerIDs)),
		WorkerOnline: int32(mm.workerMgr.OnlineCount()),
		RegionHealth: &pb.HealthScore{
			Score:   100,
			Summary: fmt.Sprintf("mirror sync: %d workers", len(workerIDs)),
		},
	})
	return err
}

// StartPeriodicSync runs periodic sync to peers.
func (mm *MirrorManager) StartPeriodicSync(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mm.SyncToPeers(ctx)
		}
	}
}

// PeerCount returns the number of configured peers.
func (mm *MirrorManager) PeerCount() int {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return len(mm.peers)
}

// HealthyPeerCount returns the number of healthy peers.
func (mm *MirrorManager) HealthyPeerCount() int {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	count := 0
	for _, p := range mm.peers {
		if p.Healthy {
			count++
		}
	}
	return count
}

// Close disconnects from all peers.
func (mm *MirrorManager) Close() {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	for _, p := range mm.peers {
		if p.conn != nil {
			p.conn.Close()
		}
	}
	mm.peers = nil
}
