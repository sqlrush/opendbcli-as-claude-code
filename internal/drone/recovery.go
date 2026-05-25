/*-------------------------------------------------------------------------
 *
 * recovery.go
 *	  recovery.go implements the daemon startup recovery procedure. On
 *	  start, scan local memory files + database alert logs to determine
 *	  if any recovery action is needed from a previous crash. Design
 *	  principle: recovery IS the first diagnosis — reuse LLM
 *	  diagnostic capability.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/recovery.go
 *
 *-------------------------------------------------------------------------
 */
// recovery.go implements the daemon startup recovery procedure.
// On start, scan local memory files + database alert logs to determine if any
// recovery action is needed from a previous crash.
// Design principle: recovery IS the first diagnosis — reuse LLM diagnostic capability.
package drone

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
	"github.com/sqlrush/opendb/internal/skill"
)

// RecoveryConfig holds parameters for the startup recovery procedure.
type RecoveryConfig struct {
	BaseDir  string // ~/.opendb
	Instance string // database instance name
	Executor *skill.Executor
}

// RunRecovery executes the startup recovery procedure.
// Returns nil if no recovery needed or recovery succeeded.
func RunRecovery(ctx context.Context, cfg RecoveryConfig) error {
	logger := cluster.DefaultLogger("recovery")
	logger.Info("启动恢复流程")

	// Step 1: Check for recent memory files (within last hour).
	recentMemories := scanRecentMemories(cfg.BaseDir, cfg.Instance, 1*time.Hour)
	if len(recentMemories) > 0 {
		logger.Info("发现最近的记忆文件", slog.Int("count", len(recentMemories)))
		for _, m := range recentMemories {
			logger.Info("记忆文件", slog.String("name", m.name), slog.String("modified", m.modTime.Format("15:04:05")))
		}
	}

	// Step 2: Check if there was a recent crash (PID file exists but process not running).
	// This is already handled by PID management — if we got here, we're starting fresh.

	// Step 3: Trigger a startup diagnosis via LLM if executor is available.
	if cfg.Executor != nil && len(recentMemories) > 0 {
		logger.Info("调用 LLM 分析启动前状态")

		// Build a recovery question based on recent memories.
		question := buildRecoveryQuestion(recentMemories)
		result, err := cfg.Executor.Execute(ctx, "llm", skill.ParamsFromMap(map[string]any{
			"args": question,
			"mode": "assist", // assist mode: read-only analysis, no write operations
		}))
		if err != nil {
			logger.Warn("LLM 恢复分析失败（跳过恢复，进入正常运行）", slog.String("error", err.Error()))
			return nil
		}
		if result != nil && result.Summary != "" {
			logger.Info("LLM 恢复建议", slog.String("summary", result.Summary))
		}
	} else {
		logger.Info("无需恢复（没有最近的记忆文件或 LLM 不可用）")
	}

	logger.Info("恢复流程完成")
	return nil
}

// memoryFile represents a memory file with its metadata.
type memoryFile struct {
	name    string
	path    string
	modTime time.Time
}

// scanRecentMemories finds memory files modified within the given duration.
func scanRecentMemories(baseDir, instance string, within time.Duration) []memoryFile {
	memDir := filepath.Join(baseDir, "memory", instance)
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return nil
	}

	cutoff := time.Now().Add(-within)
	var recent []memoryFile

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			recent = append(recent, memoryFile{
				name:    entry.Name(),
				path:    filepath.Join(memDir, entry.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	// Sort by mod time, newest first.
	sort.Slice(recent, func(i, j int) bool {
		return recent[i].modTime.After(recent[j].modTime)
	})

	return recent
}

// buildRecoveryQuestion creates a recovery question for the LLM based on recent memories.
func buildRecoveryQuestion(memories []memoryFile) string {
	var sb strings.Builder
	sb.WriteString("Agent 刚重启，请检查启动前是否有未完成的操作需要恢复。")
	sb.WriteString("以下是最近修改的记忆文件：\n")
	for _, m := range memories {
		// Read first few lines for context.
		content, err := os.ReadFile(m.path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")
		preview := strings.Join(truncateLines(lines, 5), "\n")
		sb.WriteString(fmt.Sprintf("\n--- %s ---\n%s\n", m.name, preview))
	}
	sb.WriteString("\n请分析：是否有半完成的修复操作需要恢复？如果不确定，回复'无需恢复'。")
	return sb.String()
}

// truncateLines returns up to n lines.
func truncateLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[:n]
}
