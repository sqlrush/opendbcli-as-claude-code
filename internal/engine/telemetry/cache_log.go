/*-------------------------------------------------------------------------
 *
 * cache_log.go
 *	  Package telemetry records per-call prompt cache statistics to a
 *	  local JSONL log, so operators can monitor cache hit rate without
 *	  polluting interactive output.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/telemetry/cache_log.go
 *
 *-------------------------------------------------------------------------
 */
// Package telemetry records per-call prompt cache statistics to a local
// JSONL log, so operators can monitor cache hit rate without polluting
// interactive output.
package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// LogPath returns the default path for the cache telemetry log.
func LogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".opendb", "telemetry", "cache.log")
}

// CacheEvent is one entry in the cache telemetry log.
type CacheEvent struct {
	Time         time.Time `json:"time"`
	Product      string    `json:"product,omitempty"`
	Model        string    `json:"model,omitempty"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CacheRead    int       `json:"cache_read"`
	CacheMiss    int       `json:"cache_miss"`
	CacheCreate  int       `json:"cache_create"`
	HitRate      float64   `json:"hit_rate"`
}

// Recorder is a safe, append-only JSONL writer for CacheEvent.
// The zero value is not usable; construct with NewRecorder.
type Recorder struct {
	mu   sync.Mutex
	path string
}

// NewRecorder creates a Recorder writing to path. Empty path disables logging.
func NewRecorder(path string) *Recorder {
	return &Recorder{path: path}
}

// FromUsage builds a CacheEvent from a provider.Usage snapshot.
func FromUsage(product, model string, u provider.Usage) CacheEvent {
	return CacheEvent{
		Time:         time.Now().UTC(),
		Product:      product,
		Model:        model,
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CacheRead:    u.CacheReadTokens,
		CacheMiss:    u.CacheMissTokens,
		CacheCreate:  u.CacheCreationTokens,
		HitRate:      u.HitRate(),
	}
}

// Record appends one event to the log. Does nothing when path is empty or
// the event carries no cache tokens (avoid noise for providers without cache).
func (r *Recorder) Record(ev CacheEvent) error {
	if r == nil || r.path == "" {
		return nil
	}
	if ev.CacheRead == 0 && ev.CacheMiss == 0 && ev.CacheCreate == 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
