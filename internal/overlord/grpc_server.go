/*-------------------------------------------------------------------------
 *
 * grpc_server.go
 *	  grpc_server.go implements the Overlord gRPC server. Cerebrate
 *	  connects to this server to pull region status, push policies, and
 *	  stream events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/grpc_server.go
 *
 *-------------------------------------------------------------------------
 */
// grpc_server.go implements the Overlord gRPC server.
// Cerebrate connects to this server to pull region status, push policies, and stream events.
package overlord

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/odberr"
)

// OverlordServer implements OverlordService (for Cerebrate).
// Also implements AgentService.Register and Heartbeat (for Worker registration).
type OverlordServer struct {
	pb.UnimplementedOverlordServiceServer

	mu         sync.RWMutex
	overlordID string
	region     string
	startTime  time.Time
	state      pb.AgentState
	workerMgr  *WorkerManager

	// outEvents for Overlord → Cerebrate event push.
	outEvents chan *pb.OverlordEvent

	// Event handlers wired by startup code.
	onWorkerAlert  func(evt *pb.AlertEvent)
	onMemorySync   func(evt *pb.MemorySyncEvent)
	onCorrelation  func(results []CorrelationResult)
	onPolicyUpdate func(policyType, content string) // #4: hot config update callback

	logger *slog.Logger
}

// NewOverlordServer creates a new Overlord gRPC server.
func NewOverlordServer(overlordID, region string, workerMgr *WorkerManager) *OverlordServer {
	return &OverlordServer{
		overlordID: overlordID,
		region:     region,
		startTime:  time.Now(),
		state:      pb.AgentState_STATE_RUNNING,
		workerMgr:  workerMgr,
		outEvents:  make(chan *pb.OverlordEvent, 64),
		logger:     cluster.DefaultLogger("overlord-grpc"),
	}
}

// PushEvent sends an event to Cerebrate via the outbound stream.
func (s *OverlordServer) PushEvent(evt *pb.OverlordEvent) {
	select {
	case s.outEvents <- evt:
	default:
		s.logger.Warn("event channel full, dropping event")
	}
}

// ---- gRPC Service Methods ----

// RegisterOverlord handles registration from Cerebrate.
func (s *OverlordServer) RegisterOverlord(_ context.Context, req *pb.RegisterOverlordRequest) (*pb.RegisterOverlordResponse, error) {
	s.logger.Info("Registered with Cerebrate", slog.String("overlord_id", req.OverlordId), slog.String("region", req.Region))
	return &pb.RegisterOverlordResponse{
		Accepted:    true,
		CerebrateId: "cerebrate",
		Message:     "registered",
	}, nil
}

// OverlordHeartbeat handles heartbeat.
func (s *OverlordServer) OverlordHeartbeat(_ context.Context, req *pb.OverlordHeartbeatRequest) (*pb.OverlordHeartbeatResponse, error) {
	return &pb.OverlordHeartbeatResponse{Acknowledged: true}, nil
}

// GetRegionStatus returns the region status including all worker summaries.
func (s *OverlordServer) GetRegionStatus(ctx context.Context, _ *pb.RegionStatusRequest) (*pb.RegionStatusResponse, error) {
	statuses := s.workerMgr.GetAllWorkerStatus(ctx)

	workers := make([]*pb.WorkerSummary, 0, len(statuses))
	for _, st := range statuses {
		workers = append(workers, &pb.WorkerSummary{
			WorkerId:     st.WorkerId,
			DbType:       st.DbType,
			InstanceName: st.InstanceName,
			State:        st.State,
			Health:       st.Health,
			Addr:         s.workerMgr.GetWorkerAddr(st.WorkerId),
		})
	}

	// Calculate region health as average of worker health scores.
	var totalScore int32
	for _, w := range workers {
		if w.Health != nil {
			totalScore += w.Health.Score
		}
	}
	avgScore := int32(0)
	if len(workers) > 0 {
		avgScore = totalScore / int32(len(workers))
	}

	return &pb.RegionStatusResponse{
		OverlordId:   s.overlordID,
		Region:       s.region,
		WorkerCount:  int32(s.workerMgr.WorkerCount()),
		WorkerOnline: int32(s.workerMgr.OnlineCount()),
		RegionHealth: &pb.HealthScore{Score: avgScore, Summary: fmt.Sprintf("%d/%d workers online", s.workerMgr.OnlineCount(), s.workerMgr.WorkerCount())},
		Workers:      workers,
	}, nil
}

// SetOnPolicyUpdate sets the callback for policy updates.
func (s *OverlordServer) SetOnPolicyUpdate(fn func(string, string)) { s.onPolicyUpdate = fn }

// PushPolicy receives a policy from Cerebrate and distributes to Workers.
func (s *OverlordServer) PushPolicy(ctx context.Context, req *pb.PolicyRequest) (*pb.PolicyResponse, error) {
	s.logger.Info("Received policy", slog.String("type", req.PolicyType), slog.String("action", req.Action))

	// Apply locally first (#4: HotConfig update).
	if s.onPolicyUpdate != nil {
		s.onPolicyUpdate(req.PolicyType, string(req.Content))
	}

	// Distribute to all Workers via broadcast.
	workerIDs := s.workerMgr.WorkerIDs()
	cmd := &pb.CommandRequest{
		CommandId:   fmt.Sprintf("policy-%s-%d", req.PolicyId, time.Now().UnixMilli()),
		CommandType: "apply_policy",
		Payload:     string(req.Content),
	}

	results := s.workerMgr.BroadcastCommand(ctx, workerIDs, cmd)
	updated := 0
	for _, r := range results {
		if r.Success {
			updated++
		}
	}

	return &pb.PolicyResponse{
		Success:        true,
		WorkersUpdated: int32(updated),
		Message:        fmt.Sprintf("policy distributed to %d/%d workers", updated, len(workerIDs)),
	}, nil
}

// OverlordEventStream implements the bidirectional streaming RPC.
func (s *OverlordServer) OverlordEventStream(stream pb.OverlordService_OverlordEventStreamServer) error {
	ctx := stream.Context()

	// Goroutine: send outbound events to Cerebrate.
	odberr.SafeGo(odberr.ErrClusterRPC, func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-s.outEvents:
				if !ok {
					return
				}
				if err := stream.Send(evt); err != nil {
					s.logger.Error("send event error", slog.String("error", err.Error()))
					return
				}
			}
		}
	})

	// Main: receive inbound events from Cerebrate.
	for {
		evt, err := stream.Recv()
		if err != nil {
			return err
		}
		s.handleCerebrateEvent(evt)
	}
}

// handleCerebrateEvent processes events received from Cerebrate.
func (s *OverlordServer) handleCerebrateEvent(evt *pb.OverlordEvent) {
	switch p := evt.Payload.(type) {
	case *pb.OverlordEvent_PolicyPush:
		s.logger.Info("Received policy push", slog.String("type", p.PolicyPush.PolicyType))
	case *pb.OverlordEvent_Maintenance:
		s.logger.Info("Received maintenance plan", slog.String("description", p.Maintenance.Description))
	default:
		s.logger.Warn("Received unknown event type")
	}
}

// ElectionVote handles election requests from peer backup Overlords (C10).
// Accepts the candidate if their ID is lexicographically smaller than ours.
func (s *OverlordServer) ElectionVote(_ context.Context, req *pb.ElectionVoteRequest) (*pb.ElectionVoteResponse, error) {
	s.logger.Info("Election vote request received",
		slog.String("candidate_id", req.CandidateId),
		slog.String("self_id", s.overlordID))

	// Lexicographic comparison: smaller ID wins.
	if req.CandidateId < s.overlordID {
		s.logger.Info("Accepting candidate (smaller ID)",
			slog.String("candidate", req.CandidateId),
			slog.String("self", s.overlordID))
		return &pb.ElectionVoteResponse{
			Accept:  true,
			VoterId: s.overlordID,
			Message: "accepted: candidate has priority (smaller ID)",
		}, nil
	}

	s.logger.Info("Rejecting candidate (larger ID, self will promote)",
		slog.String("candidate", req.CandidateId),
		slog.String("self", s.overlordID))
	return &pb.ElectionVoteResponse{
		Accept:  false,
		VoterId: s.overlordID,
		Message: "rejected: voter has priority (smaller ID)",
	}, nil
}

// HandleWorkerEvent processes an event received from a Worker's EventStream.
// This is the main event dispatch point — routes alerts, memory syncs, etc.
func (s *OverlordServer) HandleWorkerEvent(evt *pb.AgentEvent) {
	switch p := evt.Payload.(type) {
	case *pb.AgentEvent_Alert:
		s.logger.Warn("Worker alert", slog.String("worker_id", p.Alert.WorkerId), slog.String("description", p.Alert.Description))
		if s.onWorkerAlert != nil {
			s.onWorkerAlert(p.Alert)
		}
	case *pb.AgentEvent_MemorySync:
		s.logger.Info("Memory sync", slog.String("worker_id", p.MemorySync.WorkerId), slog.String("file", p.MemorySync.FileName), slog.String("action", p.MemorySync.Action))
		if s.onMemorySync != nil {
			s.onMemorySync(p.MemorySync)
		}
	case *pb.AgentEvent_Audit:
		s.logger.Info("Audit", slog.String("worker_id", p.Audit.WorkerId), slog.String("operation", p.Audit.Operation), slog.String("target", p.Audit.Target))
	default:
		s.logger.Warn("Unknown worker event")
	}
}

// SetOnWorkerAlert sets the handler for Worker alert events.
func (s *OverlordServer) SetOnWorkerAlert(fn func(*pb.AlertEvent)) { s.onWorkerAlert = fn }

// SetOnMemorySync sets the handler for Worker memory sync events.
func (s *OverlordServer) SetOnMemorySync(fn func(*pb.MemorySyncEvent)) { s.onMemorySync = fn }

// SetOnCorrelation sets the handler for correlation detection results.
func (s *OverlordServer) SetOnCorrelation(fn func([]CorrelationResult)) { s.onCorrelation = fn }

// ---- Server Lifecycle ----

// StartOverlordGRPCServer starts the Overlord gRPC server.
func StartOverlordGRPCServer(ctx context.Context, srv *OverlordServer, agentSvc *OverlordAgentService, listenAddr string) error {
	grpcServer := grpc.NewServer()
	pb.RegisterOverlordServiceServer(grpcServer, srv)
	pb.RegisterAgentServiceServer(grpcServer, agentSvc)

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	srv.logger.Info("Listening", slog.String("addr", listenAddr))

	// Graceful shutdown.
	odberr.SafeGo(odberr.ErrClusterRPC, func() {
		<-ctx.Done()
		srv.logger.Info("Shutting down")
		grpcServer.GracefulStop()
	})

	return grpcServer.Serve(lis)
}

// Close cleans up the Overlord server resources.
func (s *OverlordServer) Close() {
	close(s.outEvents)
}

// Hostname returns the OS hostname.
func Hostname() string {
	h, _ := os.Hostname()
	return h
}
