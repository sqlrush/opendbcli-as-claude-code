/*-------------------------------------------------------------------------
 *
 * upgrade.go
 *	  upgrade.go implements gRPC-based rolling upgrade. Cerebrate pushes
 *	  new binary to Overlords → Overlords push to Workers. Each node:
 *	  receive → verify checksum → drain → replace → restart →
 *	  health check.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/upgrade.go
 *
 *-------------------------------------------------------------------------
 */
// upgrade.go implements gRPC-based rolling upgrade.
// Cerebrate pushes new binary to Overlords → Overlords push to Workers.
// Each node: receive → verify checksum → drain → replace → restart → health check.
package cluster

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// UpgradeConfig holds parameters for a rolling upgrade.
type UpgradeConfig struct {
	BinaryPath string        // path to new binary
	Strategy   string        // "rolling" (default) or "parallel"
	BatchSize  int           // workers per batch (default 50)
	BatchDelay time.Duration // delay between batches (default 10s)
}

// UpgradePhase tracks the state of an upgrade across the cluster.
type UpgradePhase struct {
	Phase       string          `json:"phase"`       // "workers", "overlords", "cerebrate"
	Total       int             `json:"total"`
	Completed   int             `json:"completed"`
	Failed      int             `json:"failed"`
	NodeResults []UpgradeResult `json:"node_results"`
}

// UpgradeResult holds the result for a single node upgrade.
type UpgradeResult struct {
	NodeID    string `json:"node_id"`
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
}

// BinaryPayload holds a binary with its checksum for transfer.
type BinaryPayload struct {
	Data     []byte `json:"data"`
	Checksum string `json:"checksum"` // SHA-256
	Version  string `json:"version"`
	Size     int64  `json:"size"`
}

// PrepareBinaryPayload reads and checksums the binary file.
func PrepareBinaryPayload(binaryPath, version string) (*BinaryPayload, error) {
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("read binary: %w", err)
	}

	hash := sha256.Sum256(data)
	return &BinaryPayload{
		Data:     data,
		Checksum: fmt.Sprintf("%x", hash),
		Version:  version,
		Size:     int64(len(data)),
	}, nil
}

// VerifyChecksum verifies the binary payload integrity.
func (bp *BinaryPayload) VerifyChecksum() bool {
	hash := sha256.Sum256(bp.Data)
	return fmt.Sprintf("%x", hash) == bp.Checksum
}

// CreateUpgradeCommand creates a gRPC command for node upgrade.
func CreateUpgradeCommand(payload *BinaryPayload) *pb.CommandRequest {
	return &pb.CommandRequest{
		CommandId:   fmt.Sprintf("upgrade-%d", time.Now().UnixMilli()),
		CommandType: "binary_upgrade",
		Payload: fmt.Sprintf(`{"version":"%s","checksum":"%s","size":%d}`,
			payload.Version, payload.Checksum, payload.Size),
		Priority: 2, // urgent
	}
}

// PlanRollingUpgrade creates an upgrade plan for the cluster.
// Order: Workers (batched) → Overlords (one at a time) → Cerebrate (last).
func PlanRollingUpgrade(cfg UpgradeConfig, overlordCount, workerCount int) []UpgradePhase {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	phases := []UpgradePhase{
		{
			Phase: "workers",
			Total: workerCount,
		},
		{
			Phase: "overlords",
			Total: overlordCount,
		},
		{
			Phase: "cerebrate",
			Total: 1,
		},
	}

	return phases
}

// FormatUpgradePlan returns a human-readable upgrade plan.
func FormatUpgradePlan(phases []UpgradePhase, batchSize int) string {
	result := "Rolling Upgrade Plan:\n"
	for i, phase := range phases {
		if phase.Phase == "workers" {
			batches := (phase.Total + batchSize - 1) / batchSize
			result += fmt.Sprintf("  Phase %d: %s (%d nodes, %d batches of %d)\n", i+1, phase.Phase, phase.Total, batches, batchSize)
		} else {
			result += fmt.Sprintf("  Phase %d: %s (%d nodes, rolling one at a time)\n", i+1, phase.Phase, phase.Total)
		}
	}
	result += fmt.Sprintf("\nOrder: Workers → Overlords → Cerebrate (last)\n")
	result += fmt.Sprintf("Rollback: Cerebrate → Overlords → Workers (reverse)\n")
	return result
}

// HandleUpgradeCommand is called on the receiving node to apply an upgrade.
// It writes the new binary, verifies checksum, and signals for restart.
func HandleUpgradeCommand(ctx context.Context, binaryData []byte, checksum, installPath string) error {
	// Verify checksum.
	hash := sha256.Sum256(binaryData)
	if fmt.Sprintf("%x", hash) != checksum {
		return fmt.Errorf("checksum mismatch")
	}

	// Write to temp file first (atomic replace).
	tmpPath := installPath + ".new"
	if err := os.WriteFile(tmpPath, binaryData, 0755); err != nil {
		return fmt.Errorf("write new binary: %w", err)
	}

	// Replace old binary.
	backupPath := installPath + ".bak"
	os.Rename(installPath, backupPath) // backup old
	if err := os.Rename(tmpPath, installPath); err != nil {
		os.Rename(backupPath, installPath) // rollback
		return fmt.Errorf("replace binary: %w", err)
	}

	fmt.Printf("[upgrade] Binary replaced: %s (checksum: %s)\n", installPath, checksum[:12])
	fmt.Println("[upgrade] Restart required to activate new version")

	return nil
}
