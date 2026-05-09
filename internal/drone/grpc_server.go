/*-------------------------------------------------------------------------
 *
 * grpc_server.go
 *	  grpc_server.go implements the Worker Agent gRPC server. Overlord
 *	  connects to this server to pull status, push commands, and stream
 *	  events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/grpc_server.go
 *
 *-------------------------------------------------------------------------
 */
// grpc_server.go implements the Worker Agent gRPC server.
// Overlord connects to this server to pull status, push commands, and stream events.
package drone

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/odberr"
)

// RedirectFunc is called when the Worker is instructed to switch to a new Overlord.
// It receives the new Overlord address and ID, disconnects from current, and reconnects.
type RedirectFunc func(ctx context.Context, newAddr, newID, reason string) error

// DroneServer implements the AgentService gRPC server for Worker Agent.
type DroneServer struct {
	pb.UnimplementedAgentServiceServer

	mu         sync.RWMutex
	workerID   string
	hostname   string
	dbType     string
	instance   string
	startTime  time.Time
	state      pb.AgentState
	health     *pb.HealthScore
	registered bool

	// Event channels for bidirectional streaming.
	outEvents chan *pb.AgentEvent // Worker → Overlord

	// RedirectFn is called when Cerebrate/Overlord instructs this Worker to switch.
	redirectFn RedirectFunc

	logger *slog.Logger
}

// NewDroneServer creates a new gRPC server for the Worker Agent.
func NewDroneServer(workerID, dbType, instance string) *DroneServer {
	hostname, _ := os.Hostname()
	return &DroneServer{
		workerID:  workerID,
		hostname:  hostname,
		dbType:    dbType,
		instance:  instance,
		startTime: time.Now(),
		state:     pb.AgentState_STATE_RUNNING,
		health:    &pb.HealthScore{Score: 100, Summary: "healthy"},
		outEvents: make(chan *pb.AgentEvent, 64),
		logger:    cluster.DefaultLogger("grpc"),
	}
}

// SetRedirectFunc registers the callback for Worker redirect (C09).
func (s *DroneServer) SetRedirectFunc(fn RedirectFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redirectFn = fn
}

// SetState updates the agent state.
func (s *DroneServer) SetState(state pb.AgentState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// SetHealth updates the health score.
func (s *DroneServer) SetHealth(score int32, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = &pb.HealthScore{Score: score, Summary: summary}
}

// PushEvent sends an event to the outbound stream (Worker → Overlord).
func (s *DroneServer) PushEvent(evt *pb.AgentEvent) {
	select {
	case s.outEvents <- evt:
	default:
		s.logger.Warn("event channel full, dropping event")
	}
}

// ---- gRPC Service Methods ----

// Register handles Worker registration from Overlord.
func (s *DroneServer) Register(_ context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	s.mu.Lock()
	s.registered = true
	s.mu.Unlock()

	s.logger.Info("Registered with Overlord", slog.String("worker_id", req.WorkerId))
	return &pb.RegisterResponse{
		Accepted: true,
		Message:  "registered",
	}, nil
}

// Heartbeat handles heartbeat from Worker (called by Overlord to check liveness).
func (s *DroneServer) Heartbeat(_ context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	return &pb.HeartbeatResponse{Acknowledged: true}, nil
}

// GetStatus returns the current Worker status.
func (s *DroneServer) GetStatus(_ context.Context, _ *pb.StatusRequest) (*pb.StatusResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &pb.StatusResponse{
		WorkerId:      s.workerID,
		State:         s.state,
		DbType:        s.dbType,
		InstanceName:  s.instance,
		Health:        s.health,
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
	}, nil
}

// PushCommand receives a command from Overlord and executes it.
func (s *DroneServer) PushCommand(_ context.Context, req *pb.CommandRequest) (*pb.CommandResponse, error) {
	s.logger.Info("Received command", slog.String("type", req.CommandType), slog.String("id", req.CommandId))

	// TODO: dispatch command to skill executor
	return &pb.CommandResponse{
		CommandId: req.CommandId,
		Success:   true,
		Result:    `{"status": "received"}`,
	}, nil
}

// PushDiagnosis handles Overlord pushing a diagnosis conclusion back to this Worker.
// When overrides_local=true, replaces the local diagnosis report; otherwise appends.
func (s *DroneServer) PushDiagnosis(_ context.Context, req *pb.PushDiagnosisRequest) (*pb.PushDiagnosisResponse, error) {
	s.logger.Info("PushDiagnosis received",
		slog.String("alert_id", req.AlertId),
		slog.String("source", req.Source),
		slog.Bool("overrides", req.OverridesLocal))

	// TODO: integrate with report system — replace or append to the fault report
	// identified by alert_id. For now, log it.
	action := "appended"
	if req.OverridesLocal {
		action = "overridden"
	}
	s.logger.Info("Diagnosis from Overlord "+action,
		slog.String("diagnosis_preview", truncate(req.Diagnosis, 100)),
		slog.String("recommended_action", req.RecommendedAction))

	return &pb.PushDiagnosisResponse{
		Accepted: true,
		Message:  fmt.Sprintf("diagnosis %s (source=%s)", action, req.Source),
	}, nil
}

// truncate returns the first n characters of s, or s if shorter.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// RedirectWorker handles the instruction to switch to a new Overlord (C09).
func (s *DroneServer) RedirectWorker(ctx context.Context, req *pb.RedirectWorkerRequest) (*pb.RedirectWorkerResponse, error) {
	s.mu.RLock()
	fn := s.redirectFn
	s.mu.RUnlock()

	s.logger.Info("RedirectWorker request received",
		slog.String("new_overlord", req.NewOverlordAddr),
		slog.String("new_id", req.NewOverlordId),
		slog.String("reason", req.Reason))

	if fn == nil {
		return &pb.RedirectWorkerResponse{
			Success: false,
			Message: "redirect not supported (no redirect function registered)",
		}, nil
	}

	if err := fn(ctx, req.NewOverlordAddr, req.NewOverlordId, req.Reason); err != nil {
		s.logger.Error("Redirect failed", slog.String("error", err.Error()))
		return &pb.RedirectWorkerResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	s.logger.Info("Redirect successful", slog.String("new_overlord", req.NewOverlordAddr))
	return &pb.RedirectWorkerResponse{
		Success: true,
		Message: "redirected to " + req.NewOverlordAddr,
	}, nil
}

// EventStream implements the bidirectional streaming RPC.
func (s *DroneServer) EventStream(stream pb.AgentService_EventStreamServer) error {
	ctx := stream.Context()

	// Goroutine: send outbound events to Overlord.
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

	// Main: receive inbound events from Overlord.
	for {
		evt, err := stream.Recv()
		if err != nil {
			return err
		}
		s.handleInboundEvent(evt)
	}
}

// handleInboundEvent processes events received from Overlord.
func (s *DroneServer) handleInboundEvent(evt *pb.AgentEvent) {
	switch p := evt.Payload.(type) {
	case *pb.AgentEvent_Command:
		s.logger.Info("Received streamed command", slog.String("type", p.Command.CommandType))
		// TODO: dispatch to executor
	default:
		s.logger.Warn("Received unknown event type")
	}
}

// ---- Server Lifecycle ----

// StartGRPCServer starts the gRPC server on TCP and optionally Unix socket.
func StartGRPCServer(ctx context.Context, srv *DroneServer, listenAddr string) error {
	grpcServer := grpc.NewServer()
	pb.RegisterAgentServiceServer(grpcServer, srv)

	// Start TCP listener.
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", listenAddr, err)
	}
	srv.logger.Info("Listening on TCP", slog.String("addr", listenAddr))

	// Start Unix socket listener (for local CLI connection).
	sockPath := unixSocketPath()
	os.Remove(sockPath) // clean up stale socket
	if err := os.MkdirAll(filepath.Dir(sockPath), 0755); err == nil {
		unixLis, err := net.Listen("unix", sockPath)
		if err == nil {
			srv.logger.Info("Listening on Unix socket", slog.String("path", sockPath))
			go grpcServer.Serve(unixLis)
		}
	}

	// Graceful shutdown on context cancel.
	odberr.SafeGo(odberr.ErrClusterRPC, func() {
		<-ctx.Done()
		srv.logger.Info("Shutting down gRPC server")
		grpcServer.GracefulStop()
		os.Remove(sockPath)
	})

	return grpcServer.Serve(lis)
}

// unixSocketPath returns the Unix socket path for local CLI connection.
func unixSocketPath() string {
	return filepath.Join(defaultPIDDir(), "agent.sock")
}
