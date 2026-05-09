/*-------------------------------------------------------------------------
 *
 * registration.go
 *	  registration.go implements the Overlord-side Worker registration
 *	  handler. When a Worker calls Register via gRPC, Overlord adds it
 *	  to the WorkerManager and starts monitoring it.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/registration.go
 *
 *-------------------------------------------------------------------------
 */
// registration.go implements the Overlord-side Worker registration handler.
// When a Worker calls Register via gRPC, Overlord adds it to the WorkerManager
// and starts monitoring it.
package overlord

import (
	"context"
	"log/slog"

	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// RegistrationHandler handles Worker registration on the Overlord side.
type RegistrationHandler struct {
	workerMgr *WorkerManager
	// tokenValidator validates join tokens. nil = accept all.
	tokenValidator func(token string) bool
	logger         *slog.Logger
}

// NewRegistrationHandler creates a new registration handler.
func NewRegistrationHandler(wm *WorkerManager, validator func(string) bool) *RegistrationHandler {
	return &RegistrationHandler{
		workerMgr:      wm,
		tokenValidator: validator,
		logger:         cluster.DefaultLogger("registration"),
	}
}

// HandleRegister processes a Worker registration request.
// Called from the OverlordServer when it receives an AgentService.Register RPC
// via a reverse registration flow (Worker connects to Overlord's registration endpoint).
func (rh *RegistrationHandler) HandleRegister(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	// Validate join token if validator is configured.
	if rh.tokenValidator != nil && !rh.tokenValidator(req.JoinToken) {
		return &pb.RegisterResponse{
			Accepted: false,
			Message:  "invalid join token",
		}, nil
	}

	// Check capacity.
	if rh.workerMgr.WorkerCount() >= 200 {
		return &pb.RegisterResponse{
			Accepted: false,
			Message:  "region capacity reached (max 200 workers)",
		}, nil
	}

	// Add Worker to manager using its listen address.
	if req.ListenAddr != "" {
		if err := rh.workerMgr.AddWorker(ctx, req.WorkerId, req.ListenAddr); err != nil {
			// Already connected is OK (re-registration after reconnect).
			rh.logger.Info("Worker re-registration", slog.String("worker_id", req.WorkerId), slog.String("error", err.Error()))
		}
	}

	// Update Worker metadata.
	rh.workerMgr.mu.Lock()
	if wc, exists := rh.workerMgr.workers[req.WorkerId]; exists {
		wc.DBType = req.DbType
		wc.Instance = req.InstanceName
		wc.State = pb.AgentState_STATE_RUNNING
	}
	rh.workerMgr.mu.Unlock()

rh.logger.Info("Worker registered", slog.String("worker_id", req.WorkerId), slog.String("db_type", req.DbType), slog.String("instance", req.InstanceName), slog.String("addr", req.ListenAddr))

	return &pb.RegisterResponse{
		Accepted:   true,
		OverlordId: rh.workerMgr.overlordID,
		Message:    "registered",
	}, nil
}

// HandleMemorySync processes a memory sync event from a Worker.
// Stores the memory file locally as a backup.
func (rh *RegistrationHandler) HandleMemorySync(evt *pb.MemorySyncEvent) {
rh.logger.Info("Memory sync", slog.String("worker_id", evt.WorkerId), slog.String("file", evt.FileName), slog.String("action", evt.Action), slog.Int("bytes", len(evt.Content)))

	// TODO: persist to local storage for backup
	// Path: ~/.opendb/overlord/worker_data/{worker_id}/memory/{file_name}
}

// HandleAlertEvent processes an alert event from a Worker.
func (rh *RegistrationHandler) HandleAlertEvent(evt *pb.AlertEvent) {
	severity := evt.Severity
	if severity == "" {
		severity = "info"
	}

	status := "detected"
	if evt.SelfHealed {
		status = "self-healed"
	}

rh.logger.Warn("Alert from worker", slog.String("worker_id", evt.WorkerId), slog.String("severity", severity), slog.String("description", evt.Description), slog.String("cause", evt.CauseName), slog.String("status", status))

	// TODO: trigger correlation analysis if multiple workers report similar issues
}
