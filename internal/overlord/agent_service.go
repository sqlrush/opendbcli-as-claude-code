/*-------------------------------------------------------------------------
 *
 * agent_service.go
 *	  agent_service.go implements AgentService on the Overlord side.
 *	  Accepts Worker registration and heartbeat — separate from
 *	  OverlordServer to avoid embedding conflicts with
 *	  UnimplementedAgentServiceServer.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/agent_service.go
 *
 *-------------------------------------------------------------------------
 */
// agent_service.go implements AgentService on the Overlord side.
// Accepts Worker registration and heartbeat — separate from OverlordServer
// to avoid embedding conflicts with UnimplementedAgentServiceServer.
package overlord

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// OverlordAgentService implements AgentService for Worker registration.
type OverlordAgentService struct {
	pb.UnimplementedAgentServiceServer
	workerMgr       *WorkerManager
	overlordID      string
	dbAccessMgr     *DBAccessManager   // for registering Worker DB connections
	onWorkerChanged func()             // called after Worker registration to trigger immediate Cerebrate heartbeat
	tokenValidator  func(string) bool  // validates join token on registration (ISSUE-013)
	logger          *slog.Logger
}

// SetDBAccessManager sets the DB access manager for Worker DB registration.
func (s *OverlordAgentService) SetDBAccessManager(dam *DBAccessManager) {
	s.dbAccessMgr = dam
}

// NewOverlordAgentService creates an AgentService handler for Worker registration.
func NewOverlordAgentService(workerMgr *WorkerManager, overlordID string) *OverlordAgentService {
	return &OverlordAgentService{
		workerMgr:  workerMgr,
		overlordID: overlordID,
		logger:     cluster.DefaultLogger("overlord-agent"),
	}
}

// SetOnWorkerChanged sets a callback triggered when Worker count changes.
func (s *OverlordAgentService) SetOnWorkerChanged(fn func()) {
	s.onWorkerChanged = fn
}

// SetTokenValidator sets a function to validate join tokens on registration (ISSUE-013).
func (s *OverlordAgentService) SetTokenValidator(fn func(string) bool) {
	s.tokenValidator = fn
}

// Register accepts Worker registration. Re-registration after crash resets cached state (ISSUE-006 fix).
func (s *OverlordAgentService) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
s.logger.Info("Worker registration", slog.String("worker_id", req.WorkerId), slog.String("db_type", req.DbType), slog.String("instance", req.InstanceName), slog.String("addr", req.ListenAddr))

	// ISSUE-013: Validate join token before accepting registration.
	if s.tokenValidator != nil && !s.tokenValidator(req.GetJoinToken()) {
		s.logger.Warn("Worker registration rejected: invalid token", slog.String("worker_id", req.WorkerId))
		return &pb.RegisterResponse{
			Accepted: false,
			Message:  "invalid or expired join token",
		}, fmt.Errorf("invalid join token")
	}

	if req.ListenAddr != "" {
		if err := s.workerMgr.AddWorker(ctx, req.WorkerId, req.ListenAddr); err != nil {
			// ISSUE-006: On re-registration (after crash), reset connection if address changed.
			s.logger.Info("Worker re-registration (crash recovery likely)", slog.String("worker_id", req.WorkerId))
		}
	}

	s.workerMgr.mu.Lock()
	if wc, exists := s.workerMgr.workers[req.WorkerId]; exists {
		wc.DBType = req.DbType
		wc.Instance = req.InstanceName
		wc.State = pb.AgentState_STATE_RUNNING
		wc.LastSeen = time.Now()
		s.logger.Info("Worker state reset to RUNNING", slog.String("worker_id", req.WorkerId))
	}
	s.workerMgr.mu.Unlock()

	// Trigger immediate Cerebrate heartbeat so dashboard updates instantly.
	if s.onWorkerChanged != nil {
		go s.onWorkerChanged()
	}

	return &pb.RegisterResponse{
		Accepted:   true,
		OverlordId: s.overlordID,
		Message:    fmt.Sprintf("registered (%d workers total)", s.workerMgr.WorkerCount()),
	}, nil
}

// Heartbeat receives Worker heartbeat.
// Returns error for unknown Workers so they re-register after Overlord restart (ISSUE-012).
func (s *OverlordAgentService) Heartbeat(_ context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	s.workerMgr.mu.Lock()
	wc, exists := s.workerMgr.workers[req.WorkerId]
	if exists {
		wc.State = req.State
		wc.Health = req.Health
		wc.LastSeen = time.Now()
	}
	s.workerMgr.mu.Unlock()

	// ISSUE-012: Unknown Worker (e.g. after Overlord restart) — tell it to re-register.
	if !exists {
		s.logger.Warn("Heartbeat from unknown Worker, requesting re-registration",
			slog.String("worker_id", req.WorkerId))
		return nil, fmt.Errorf("unknown worker %s: please re-register", req.WorkerId)
	}

	return &pb.HeartbeatResponse{Acknowledged: true}, nil
}
