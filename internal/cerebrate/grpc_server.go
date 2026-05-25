/*-------------------------------------------------------------------------
 *
 * grpc_server.go
 *	  grpc_server.go implements the Cerebrate gRPC server. Accepts
 *	  Overlord registrations and provides fleet-level RPC endpoints.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/grpc_server.go
 *
 *-------------------------------------------------------------------------
 */
// grpc_server.go implements the Cerebrate gRPC server.
// Accepts Overlord registrations and provides fleet-level RPC endpoints.
package cerebrate

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"

	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/odberr"
)

func timeNow() time.Time        { return time.Now() }
func timeNowUnixMilli() int64   { return time.Now().UnixMilli() }

// CerebrateGRPCServer wraps OverlordService to accept Overlord registrations.
type CerebrateGRPCServer struct {
	pb.UnimplementedOverlordServiceServer

	cerebrateID string
	fleet       *FleetManager
	grpcServer  *grpc.Server
	logger      *slog.Logger
	timeline    *TimelineStore
	reports     *ReportStore

	// haMgr is set after construction to auto-designate backup Overlords (C10).
	haMgr *HAManager

	// tokenValidator validates join tokens on Overlord registration (ISSUE-013).
	tokenValidator func(string) bool
}

// SetHAManager sets the HA manager for auto-backup designation (C10).
func (s *CerebrateGRPCServer) SetHAManager(ha *HAManager) { s.haMgr = ha }

// SetTimeline sets the timeline store for event tracking.
func (s *CerebrateGRPCServer) SetTimeline(ts *TimelineStore) { s.timeline = ts }

// SetReports sets the report store for report caching.
func (s *CerebrateGRPCServer) SetReports(rs *ReportStore) { s.reports = rs }

// SetTokenValidator sets a function to validate join tokens on registration (ISSUE-013).
func (s *CerebrateGRPCServer) SetTokenValidator(fn func(string) bool) { s.tokenValidator = fn }

// NewCerebrateGRPCServer creates a gRPC server for Cerebrate.
func NewCerebrateGRPCServer(cerebrateID string, fleet *FleetManager) *CerebrateGRPCServer {
	return &CerebrateGRPCServer{
		cerebrateID: cerebrateID,
		fleet:       fleet,
		logger:      cluster.DefaultLogger("cerebrate-grpc"),
	}
}

// Start launches the gRPC server. Blocks until ctx is cancelled.
func (s *CerebrateGRPCServer) Start(ctx context.Context, listenAddr string) error {
	s.grpcServer = grpc.NewServer()
	pb.RegisterOverlordServiceServer(s.grpcServer, s)

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	s.logger.Info("Listening", slog.String("addr", listenAddr))

	odberr.SafeGo(odberr.ErrClusterRPC, func() {
		<-ctx.Done()
		s.logger.Info("Shutting down")
		s.grpcServer.GracefulStop()
	})

	return s.grpcServer.Serve(lis)
}

// RegisterOverlord handles Overlord registration.
// Automatically adds the Overlord to FleetManager.
func (s *CerebrateGRPCServer) RegisterOverlord(ctx context.Context, req *pb.RegisterOverlordRequest) (*pb.RegisterOverlordResponse, error) {
s.logger.Info("Overlord registration", slog.String("overlord_id", req.OverlordId), slog.String("region", req.Region), slog.String("addr", req.ListenAddr))

	// ISSUE-013: Validate join token before accepting registration.
	if s.tokenValidator != nil && !s.tokenValidator(req.GetJoinToken()) {
		s.logger.Warn("Overlord registration rejected: invalid token", slog.String("overlord_id", req.OverlordId))
		return &pb.RegisterOverlordResponse{
			Accepted: false,
			Message:  "invalid or expired join token",
		}, fmt.Errorf("invalid join token")
	}

	// Add to FleetManager (connects back to Overlord).
	if req.ListenAddr != "" {
		if err := s.fleet.AddOverlord(ctx, req.OverlordId, req.Region, req.ListenAddr); err != nil {
			// Re-registration is OK.
			s.logger.Info("Overlord re-registration", slog.String("error", err.Error()))
		}
	}

	// Push timeline event for registration.
	if s.timeline != nil {
		s.timeline.Add(TimelineEvent{
			ID:          fmt.Sprintf("reg-%d", timeNowUnixMilli()),
			Timestamp:   timeNow(),
			OverlordID:  req.OverlordId,
			EventType:   "registration",
			Severity:    "info",
			Description: fmt.Sprintf("Overlord %s (%s) registered", req.OverlordId, req.Region),
		})
	}

	// C10: Auto-designate first 2 Overlords as Cerebrate backups.
	if s.haMgr != nil && req.ListenAddr != "" && s.haMgr.BackupCount() < 2 {
		if err := s.haMgr.AddBackup(ctx, req.OverlordId, req.ListenAddr); err != nil {
			s.logger.Info("Auto-backup designation skipped", slog.String("error", err.Error()))
		} else {
			s.logger.Info("Auto-designated Cerebrate backup (C10)",
				slog.String("overlord_id", req.OverlordId),
				slog.Int("backup_count", s.haMgr.BackupCount()))
		}
	}

	return &pb.RegisterOverlordResponse{
		Accepted:    true,
		CerebrateId: s.cerebrateID,
		Message:     fmt.Sprintf("registered in fleet (total overlords: %d)", s.fleet.OverlordCount()),
	}, nil
}

// OverlordHeartbeat handles Overlord heartbeat.
// Returns error for unknown Overlords so they re-register after Cerebrate restart (ISSUE-008).
func (s *CerebrateGRPCServer) OverlordHeartbeat(_ context.Context, req *pb.OverlordHeartbeatRequest) (*pb.OverlordHeartbeatResponse, error) {
	// Update fleet state from heartbeat data.
	s.fleet.mu.Lock()
	oc, exists := s.fleet.overlords[req.OverlordId]
	if exists {
		oc.State = req.State
		oc.WorkerCount = req.WorkerCount
		oc.OnlineCount = req.WorkerOnline
		oc.Health = req.RegionHealth
		oc.LastSeen = time.Now()
	}
	s.fleet.mu.Unlock()

	// ISSUE-008: Unknown Overlord (e.g. after Cerebrate restart) — tell it to re-register.
	if !exists {
		s.logger.Warn("Heartbeat from unknown Overlord, requesting re-registration",
			slog.String("overlord_id", req.OverlordId))
		return nil, fmt.Errorf("unknown overlord %s: please re-register", req.OverlordId)
	}

	// Push timeline event if health degraded.
	if s.timeline != nil && req.RegionHealth != nil && req.RegionHealth.Score < 70 {
		s.timeline.Add(TimelineEvent{
			ID:          fmt.Sprintf("hb-%d", timeNowUnixMilli()),
			Timestamp:   timeNow(),
			OverlordID:  req.OverlordId,
			EventType:   "degraded",
			Severity:    "warning",
			Description: fmt.Sprintf("Health %d/100: %s", req.RegionHealth.Score, req.RegionHealth.Summary),
		})
	}

	return &pb.OverlordHeartbeatResponse{Acknowledged: true}, nil
}

// GetRegionStatus returns region status by querying the Overlord via FleetManager.
func (s *CerebrateGRPCServer) GetRegionStatus(ctx context.Context, req *pb.RegionStatusRequest) (*pb.RegionStatusResponse, error) {
	return s.fleet.GetRegionStatus(ctx, req.OverlordId)
}

// PushPolicy distributes policy to the target Overlord.
func (s *CerebrateGRPCServer) PushPolicy(ctx context.Context, req *pb.PolicyRequest) (*pb.PolicyResponse, error) {
	// Push to all Overlords.
	results := s.fleet.PushPolicyToAll(ctx, req)
	totalUpdated := int32(0)
	for _, r := range results {
		if r.Success {
			totalUpdated += r.WorkersUpdated
		}
	}
	return &pb.PolicyResponse{
		Success:        true,
		WorkersUpdated: totalUpdated,
		Message:        fmt.Sprintf("distributed to %d overlords", len(results)),
	}, nil
}
