/*-------------------------------------------------------------------------
 *
 * quota.go
 *	  Enforce checks total size of sessions/ + memory/ and deletes
 *	  oldest files until under the quota. Policies are never deleted and
 *	  don't count toward quota.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/quota/quota.go
 *
 *-------------------------------------------------------------------------
 */
package quota

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/engine/memory"
)

// DefaultQuotaBytes is the default shared quota for sessions + memory (10GB).
const DefaultQuotaBytes int64 = 10 * 1024 * 1024 * 1024

// fileInfo holds path and size for quota accounting.
type fileInfo struct {
	path  string
	size  int64
	mtime time.Time
}

// Enforce checks total size of sessions/ + memory/ and deletes oldest files
// until under the quota. Policies are never deleted and don't count toward quota.
//
// Protected files (never deleted):
//   - MEMORY.md  (index, will be rebuilt)
//   - PROFILE.md (instance profile)
//
// After deletion, affected MEMORY.md indexes are rebuilt.
func Enforce(baseDir string, quotaBytes int64) {
	if quotaBytes <= 0 {
		quotaBytes = DefaultQuotaBytes
	}

	sessionsDir := filepath.Join(baseDir, "sessions")
	memoryDir := filepath.Join(baseDir, "memory")

	// Collect all files from sessions/ and memory/
	var allFiles []fileInfo
	allFiles = append(allFiles, collectFiles(sessionsDir)...)
	allFiles = append(allFiles, collectFiles(memoryDir)...)

	if len(allFiles) == 0 {
		return
	}

	// Calculate total size
	var totalSize int64
	for _, f := range allFiles {
		totalSize += f.size
	}

	if totalSize <= quotaBytes {
		return // Within quota
	}

	// Separate protected files from deletable files
	var deletable []fileInfo
	for _, f := range allFiles {
		base := filepath.Base(f.path)
		if base == memory.IndexFile || base == memory.ProfileFile {
			continue // Never delete these
		}
		deletable = append(deletable, f)
	}

	// Sort deletable by mtime ascending (oldest first)
	sort.Slice(deletable, func(i, j int) bool {
		return deletable[i].mtime.Before(deletable[j].mtime)
	})

	// Track which instance memory dirs need index rebuild
	affectedDirs := make(map[string]bool)

	// Delete oldest deletable files until under quota
	for totalSize > quotaBytes && len(deletable) > 0 {
		f := deletable[0]
		deletable = deletable[1:]

		// Track if this is a memory file (for index rebuild)
		if strings.Contains(f.path, string(filepath.Separator)+"memory"+string(filepath.Separator)) {
			affectedDirs[filepath.Dir(f.path)] = true
		}

		if err := os.Remove(f.path); err != nil {
			log.Printf("quota: failed to remove %s: %v", f.path, err)
			continue
		}
		totalSize -= f.size
	}

	// Rebuild MEMORY.md indexes for affected instance dirs
	for dir := range affectedDirs {
		if err := memory.RebuildIndex(dir); err != nil {
			log.Printf("quota: failed to rebuild index in %s: %v", dir, err)
		}
	}
}

// collectFiles recursively collects all regular files in a directory tree.
func collectFiles(dir string) []fileInfo {
	var files []fileInfo

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}
		// Skip .tmp files (atomic write temporaries)
		if strings.HasSuffix(path, ".tmp") {
			return nil
		}
		files = append(files, fileInfo{
			path:  path,
			size:  info.Size(),
			mtime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil
	}
	return files
}
