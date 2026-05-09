/*-------------------------------------------------------------------------
 *
 * cache_log_test.go
 *	  Test cases for cache_log.go (telemetry package):
 *	  TestRecorder_Record_CreatesFileAndLogsEntry,
 *	  TestRecorder_Record_SkipsNoCache,
 *	  TestRecorder_Record_EmptyPathIsNoop.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/telemetry/cache_log_test.go
 *
 *-------------------------------------------------------------------------
 */
package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

func TestRecorder_Record_CreatesFileAndLogsEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "cache.log")
	r := NewRecorder(path)

	ev := FromUsage("opengauss", "glm-5", provider.Usage{
		InputTokens:     100,
		OutputTokens:    50,
		CacheReadTokens: 40,
		CacheMissTokens: 60,
	})
	if err := r.Record(ev); err != nil {
		t.Fatalf("Record returned error: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("log not created: %v", err)
	}
	defer f.Close()

	scan := bufio.NewScanner(f)
	if !scan.Scan() {
		t.Fatal("no line written")
	}
	var got CacheEvent
	if err := json.Unmarshal(scan.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Product != "opengauss" || got.Model != "glm-5" {
		t.Errorf("product/model lost: %+v", got)
	}
	if got.CacheRead != 40 || got.CacheMiss != 60 {
		t.Errorf("cache tokens wrong: %+v", got)
	}
	if got.HitRate < 0.39 || got.HitRate > 0.41 {
		t.Errorf("hit_rate = %v, want ~0.4", got.HitRate)
	}
}

func TestRecorder_Record_SkipsNoCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.log")
	r := NewRecorder(path)

	ev := FromUsage("mysql", "glm-5", provider.Usage{
		InputTokens:  100,
		OutputTokens: 50,
	})
	if err := r.Record(ev); err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("log file should not be created when cache is unused")
	}
}

func TestRecorder_Record_EmptyPathIsNoop(t *testing.T) {
	r := NewRecorder("")
	ev := FromUsage("", "", provider.Usage{CacheReadTokens: 10})
	if err := r.Record(ev); err != nil {
		t.Fatalf("empty path should be no-op, got: %v", err)
	}
}
