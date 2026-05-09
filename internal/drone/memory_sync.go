/*-------------------------------------------------------------------------
 *
 * memory_sync.go
 *	  memory_sync.go implements incremental memory/policy sync from
 *	  Worker to Overlord. Scans ~/.opendb/memory/{instance}/ every
 *	  minute for new/modified files, pushes delta to Overlord.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/memory_sync.go
 *
 *-------------------------------------------------------------------------
 */
// memory_sync.go implements incremental memory/policy sync from Worker to Overlord.
// Scans ~/.opendb/memory/{instance}/ every minute for new/modified files, pushes delta to Overlord.
package drone

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// MemorySyncer watches local memory/policy files and syncs changes to Overlord.
type MemorySyncer struct {
	baseDir    string
	instance   string
	interval   time.Duration
	grpcServer *DroneServer // push events to Overlord via gRPC stream
	logger     *slog.Logger

	mu       sync.Mutex
	lastSync map[string]time.Time // filePath → last mod time synced
}

// NewMemorySyncer creates a memory syncer for the given instance.
func NewMemorySyncer(baseDir, instance string, interval time.Duration, grpcServer *DroneServer) *MemorySyncer {
	return &MemorySyncer{
		baseDir:    baseDir,
		instance:   instance,
		interval:   interval,
		grpcServer: grpcServer,
		logger:     cluster.DefaultLogger("memory-sync"),
		lastSync:   make(map[string]time.Time),
	}
}

// Start begins periodic sync. Blocks until ctx is cancelled.
func (ms *MemorySyncer) Start(ctx context.Context) {
	// Initial scan to populate lastSync baseline.
	ms.scanAndSync(ctx)

	ticker := time.NewTicker(ms.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ms.scanAndSync(ctx)
		}
	}
}

// scanAndSync scans memory and policy directories, pushes changed files to Overlord.
func (ms *MemorySyncer) scanAndSync(ctx context.Context) {
	dirs := []string{
		filepath.Join(ms.baseDir, "memory", ms.instance),
		filepath.Join(ms.baseDir, "policies"),
		filepath.Join(ms.baseDir, "rules", ms.instance),
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	synced := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // directory may not exist yet
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}

			modTime := info.ModTime()
			lastMod, seen := ms.lastSync[path]

			if !seen || modTime.After(lastMod) {
				// File is new or modified — sync it.
				if err := ms.pushFile(ctx, path, entry.Name(), dir); err != nil {
					ms.logger.Warn("push failed", slog.String("file", entry.Name()), slog.String("error", err.Error()))
					continue
				}
				ms.lastSync[path] = modTime
				synced++
			}
		}
	}

	// Check for deleted files.
	for path := range ms.lastSync {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			ms.pushDelete(ctx, filepath.Base(path))
			delete(ms.lastSync, path)
			synced++
		}
	}

	if synced > 0 {
		ms.logger.Info("synced files to Overlord", slog.Int("count", synced))
	}
}

// pushFile reads a file and pushes it to Overlord via gRPC event stream.
func (ms *MemorySyncer) pushFile(_ context.Context, path, fileName, dir string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	action := "update"
	if _, seen := ms.lastSync[path]; !seen {
		action = "create"
	}

	// Determine category from directory.
	category := "memory"
	if filepath.Base(filepath.Dir(dir)) == "policies" || filepath.Base(dir) == "policies" {
		category = "policy"
	} else if filepath.Base(filepath.Dir(dir)) == "rules" || filepath.Base(dir) == "rules" {
		category = "rule"
	}

	ms.grpcServer.PushEvent(&pb.AgentEvent{
		EventId:   fmt.Sprintf("memsync-%d", time.Now().UnixMilli()),
		Timestamp: time.Now().Unix(),
		Payload: &pb.AgentEvent_MemorySync{
			MemorySync: &pb.MemorySyncEvent{
				WorkerId:     ms.grpcServer.workerID,
				InstanceName: ms.instance,
				FileName:     fmt.Sprintf("%s/%s", category, fileName),
				Content:      content,
				Action:       action,
			},
		},
	})

	return nil
}

// pushDelete notifies Overlord that a file was deleted.
func (ms *MemorySyncer) pushDelete(_ context.Context, fileName string) {
	ms.grpcServer.PushEvent(&pb.AgentEvent{
		EventId:   fmt.Sprintf("memsync-del-%d", time.Now().UnixMilli()),
		Timestamp: time.Now().Unix(),
		Payload: &pb.AgentEvent_MemorySync{
			MemorySync: &pb.MemorySyncEvent{
				WorkerId:     ms.grpcServer.workerID,
				InstanceName: ms.instance,
				FileName:     fileName,
				Action:       "delete",
			},
		},
	})
}
